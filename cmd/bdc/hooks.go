package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
	"github.com/brianevanmiller/beadcrumbs/internal/store/dolt"
)

// Hooks are the optional half of Beadcrumbs, and they are optional in a precise
// sense: nothing in the documented workflow depends on one, and a hook that
// cannot do its job declines rather than failing the operation that triggered
// it. Three rules follow from that and are enforced here rather than in shell:
//
//   - `hooks run` exits 0 for every runtime condition — a busy ledger, no
//     ledger, a failed harvest — and says on stderr what it skipped. A hook
//     that fails `git push` is worse than no hook; a hook that swallows the
//     miss silently is how a harvest gets lost.
//   - The shims chain. `bd hooks install` may already own `pre-push`, so an
//     existing hook is preserved, run first, and its exit status is the shim's.
//   - Automatic harvesting is opt-in per repository. With `harvest.auto` off —
//     the default — a trigger reports what is outstanding and writes nothing.
//     `bdc hooks install --auto-harvest` is the opt-in; `bdc hooks uninstall`
//     is the opt-out, and neither touches anything already persisted.

// gitHooks are the two durable-completion points a repository actually has. No
// agent harness has a PR-merge hook, and these are what fire on the real event.
var gitHooks = []string{"pre-push", "post-merge"}

// hookTriggers is every trigger `bdc hooks run` recognises, mapped to whether
// an automatic harvest is its action when the repository has opted in. The
// false entries report and never write, whatever the configuration says.
var hookTriggers = map[string]bool{
	"pre-push":    true,
	"post-merge":  true,
	"pre-compact": true,
	"session-end": true,
	"stop":        false,
}

const (
	// hookMarker identifies a shim as ours. Uninstall refuses to remove a hook
	// that does not carry it, and install refuses to discard one.
	hookMarker = "# beadcrumbs-hook v1"

	// priorSuffix names the pre-existing hook the shim chains to.
	priorSuffix = ".beadcrumbs-prior"
)

// hookMaxOpenWait is the lock budget a hook that may harvest allows. It is four
// times the ordinary 15s because such a hook fires exactly when another `bdc` is
// most likely to hold the engine — a harvest triggered by the same push — and
// losing the harvest is the cost of giving up early (plan §4.2). A var so a test
// can shorten it; production never changes it.
var hookMaxOpenWait = 60 * time.Second

// hookReportWait is the budget for a trigger that only reports. `stop` fires at
// every agent turn end and writes nothing, so there is nothing for the wait to
// save: it would buy a minute of dead time to print a reminder.
var hookReportWait = 2 * time.Second

// hookWait narrows `hooks run`'s lock budget from the trigger it was given,
// which the static annotation cannot express because it is read before the
// argument is known.
func hookWait(cmd *cobra.Command, args []string) (time.Duration, bool) {
	if commandName(cmd) != "hooks.run" || len(args) != 1 {
		return 0, false
	}
	if mayHarvest, known := hookTriggers[args[0]]; known && !mayHarvest {
		return hookReportWait, true
	}
	return 0, false
}

// hookShim is the installed script. It runs the pre-existing hook first, so
// that hook gets the stdin git supplies, and exits with *its* status: bdc's own
// result may never decide whether the git operation proceeds.
const hookShim = `#!/bin/sh
` + hookMarker + `
# Installed by ` + "`bdc hooks install`" + `. Remove with ` + "`bdc hooks uninstall`" + `.
prior="$(dirname "$0")/%[1]s` + priorSuffix + `"
status=0
if [ -x "$prior" ]; then
	"$prior" "$@" || status=$?
fi
if command -v bdc >/dev/null 2>&1; then
	bdc hooks run %[1]s || true
fi
exit $status
`

func (a *app) newHooksCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install, remove, and run the optional git hooks",
		Long: "Beadcrumbs' durable-completion points are git hooks and CI, not agent session " +
			"hooks: no harness has a hook for a pull request being merged.\n\n" +
			"`bdc hooks install` writes chained pre-push and post-merge shims that call " +
			"`bdc hooks run`. An existing hook is preserved, runs first, and keeps deciding " +
			"whether the git operation proceeds.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(a.newHooksInstallCommand(), a.newHooksUninstallCommand(), a.newHooksRunCommand())
	return cmd
}

// hookState is one hook's path and what installing or removing did to it.
type hookState struct {
	Hook   string `json:"hook"`
	Path   string `json:"path"`
	Action string `json:"action"`
}

// hooksData is the `{hooks[], chained[]}` from the CLI contract, plus the
// resulting harvesting policy: install is the opt-in, so the caller has to be
// able to see which way it left the repository without a second command.
type hooksData struct {
	Hooks       []hookState `json:"hooks"`
	Chained     []string    `json:"chained"`
	AutoHarvest bool        `json:"auto_harvest"`
}

func (a *app) newHooksInstallCommand() *cobra.Command {
	var force, auto bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write the chained pre-push and post-merge shims",
		Args:  cobra.NoArgs,
		// The shims are files, and uninstall has to work against a ledger that
		// is gone. Only --auto-harvest needs the engine.
		Annotations: map[string]string{ledgerAnnotation: string(ledgerOptional)},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"replace a saved pre-existing hook that is already parked beside the shim")
	cmd.Flags().BoolVar(&auto, "auto-harvest", false,
		"opt this repository into automatic harvesting at the installed trigger points")

	cmd.RunE = a.handle(func(cmd *cobra.Command, _ []string) (result, error) {
		dir, err := a.hooksDir(cmd.Context())
		if err != nil {
			return result{}, err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return result{}, ledger.FailWith(ledger.ErrIntegrity, "storage_hooks_dir", err,
				"cannot create the hooks directory %s", dir)
		}

		data := hooksData{Hooks: []hookState{}, Chained: []string{}}
		for _, hook := range gitHooks {
			state, chained, err := installHook(dir, hook, force)
			if err != nil {
				return result{}, err
			}
			data.Hooks = append(data.Hooks, state)
			if chained {
				data.Chained = append(data.Chained, hook)
			}
		}

		if auto {
			if err := a.setHarvestAuto(cmd.Context(), true); err != nil {
				return result{}, err
			}
			data.AutoHarvest = true
		} else if data.AutoHarvest, err = a.harvestAuto(cmd.Context()); err != nil {
			return result{}, err
		}
		return result{Data: data, Human: func(w io.Writer) { renderHooks(w, data) }}, nil
	})
	return cmd
}

func (a *app) newHooksUninstallCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the shims, restore any hook they replaced, and opt out of automatic harvesting",
		Args:  cobra.NoArgs,
		Annotations: map[string]string{
			ledgerAnnotation: string(ledgerOptional),
		},
	}
	// --force is accepted for symmetry with install and is deliberately inert:
	// there is no state in which forcing the removal of a hook we do not own
	// would be the right answer.
	cmd.Flags().Bool("force", false, "accepted for symmetry; a hook bdc does not own is never removed")

	cmd.RunE = a.handle(func(cmd *cobra.Command, _ []string) (result, error) {
		dir, err := a.hooksDir(cmd.Context())
		if err != nil {
			return result{}, err
		}
		data := hooksData{Hooks: []hookState{}, Chained: []string{}}
		for _, hook := range gitHooks {
			state, restored, err := uninstallHook(dir, hook)
			if err != nil {
				return result{}, err
			}
			if state.Action == "foreign" {
				a.out.warn("hook_not_ours", fmt.Sprintf(
					"%s was not installed by bdc and was left alone", state.Path))
			}
			data.Hooks = append(data.Hooks, state)
			if restored {
				data.Chained = append(data.Chained, hook)
			}
		}

		// Opting out is a policy change, not a data change: nothing already
		// captured, harvested, or promoted is touched.
		if a.store != nil {
			if err := a.setHarvestAuto(cmd.Context(), false); err != nil {
				return result{}, err
			}
		} else {
			a.out.warn("no_ledger", "no ledger here, so harvest.auto was not cleared")
		}
		return result{Data: data, Human: func(w io.Writer) { renderHooks(w, data) }}, nil
	})
	return cmd
}

// installHook writes one shim. The three cases differ in kind, so they are
// three branches: nothing there, our shim already there, and somebody else's
// hook — which is preserved rather than replaced, because `bd hooks install`
// may legitimately own the same file.
func installHook(dir, hook string, force bool) (hookState, bool, error) {
	path := filepath.Join(dir, hook)
	prior := path + priorSuffix
	state := hookState{Hook: hook, Path: path}

	existing, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		state.Action = "installed"
	case err != nil:
		return state, false, ledger.FailWith(ledger.ErrIntegrity, "storage_hook_read", err,
			"cannot read the existing %s hook", hook)
	case strings.Contains(string(existing), hookMarker):
		state.Action = "unchanged"
	default:
		if _, err := os.Stat(prior); err == nil && !force {
			return state, false, ledger.Fail(ledger.ErrInvalidInput, "invalid_usage",
				"%s already holds a saved hook; rerun with --force to replace it", prior)
		}
		if err := os.Rename(path, prior); err != nil {
			return state, false, ledger.FailWith(ledger.ErrIntegrity, "storage_hook_chain", err,
				"cannot preserve the existing %s hook", hook)
		}
		state.Action = "chained"
	}

	if err := os.WriteFile(path, []byte(fmt.Sprintf(hookShim, hook)), 0o755); err != nil {
		return state, false, ledger.FailWith(ledger.ErrIntegrity, "storage_hook_write", err,
			"cannot write the %s hook", hook)
	}
	return state, state.Action == "chained", nil
}

// uninstallHook removes one shim and restores whatever it displaced. A hook
// that is not ours is reported and left exactly as it is: this command's job is
// to undo what install did, and nothing else.
func uninstallHook(dir, hook string) (hookState, bool, error) {
	path := filepath.Join(dir, hook)
	prior := path + priorSuffix
	state := hookState{Hook: hook, Path: path}

	existing, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		state.Action = "absent"
		return state, false, nil
	case err != nil:
		return state, false, ledger.FailWith(ledger.ErrIntegrity, "storage_hook_read", err,
			"cannot read the %s hook", hook)
	case !strings.Contains(string(existing), hookMarker):
		state.Action = "foreign"
		return state, false, nil
	}

	if err := os.Remove(path); err != nil {
		return state, false, ledger.FailWith(ledger.ErrIntegrity, "storage_hook_remove", err,
			"cannot remove the %s hook", hook)
	}
	if _, err := os.Stat(prior); err == nil {
		if err := os.Rename(prior, path); err != nil {
			return state, false, ledger.FailWith(ledger.ErrIntegrity, "storage_hook_restore", err,
				"the %s shim was removed but the hook it replaced could not be restored from %s",
				hook, prior)
		}
		state.Action = "restored"
		return state, true, nil
	}
	state.Action = "removed"
	return state, false, nil
}

// harvestAuto reports the standing policy without changing it, which is what an
// install that was not asked to opt in must do: the flag is the repository's
// answer, not this command's.
func (a *app) harvestAuto(ctx context.Context) (bool, error) {
	if a.store == nil {
		return false, nil
	}
	led, err := a.ledger(ctx)
	if err != nil {
		return false, err
	}
	return led.Config().HarvestAuto, nil
}

// setHarvestAuto is the opt-in and the opt-out.
func (a *app) setHarvestAuto(ctx context.Context, on bool) error {
	if a.store == nil && a.openErr == nil {
		return ledger.Fail(ledger.ErrNoLedger, "no_ledger",
			"automatic harvesting is a policy recorded in the ledger; run `bdc init` first")
	}
	led, err := a.ledger(ctx)
	if err != nil {
		return err
	}
	return led.SetHarvestAuto(ctx, on)
}

// hooksDir resolves where git will look for hooks, which is core.hooksPath when
// it is set and <git-common-dir>/hooks otherwise. Guessing the second would put
// a shim somewhere git never reads.
func (a *app) hooksDir(ctx context.Context) (string, error) {
	if a.loc.GitCommon == "" {
		return "", ledger.Fail(ledger.ErrNoLedger, "no_ledger",
			"hooks are installed into a Git repository, and this is not one")
	}
	git := exec.CommandContext(ctx, "git", "-C", a.loc.RepoRoot,
		"config", "--get", "core.hooksPath")
	// `-C` does not override an inherited GIT_DIR, so a `bdc hooks install`
	// run from inside a hook or a submodule would read core.hooksPath out of a
	// different repository.
	git.Env = dolt.HermeticGitEnv()
	out, err := git.Output()
	configured := strings.TrimSpace(string(out))
	var exitErr *exec.ExitError
	switch {
	case err == nil && configured != "":
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 1, err == nil:
		// Exit 1 is `git config --get`'s answer for an unset key, which is not
		// a failure. Anything else is: guessing the default path would put the
		// shim where git does not look and report success for doing it.
		return filepath.Join(a.loc.GitCommon, "hooks"), nil
	default:
		return "", ledger.FailWith(ledger.ErrIntegrity, "storage_hooks_path", err,
			"cannot read core.hooksPath for %s", a.loc.RepoRoot)
	}
	if filepath.IsAbs(configured) {
		return configured, nil
	}
	// git resolves a relative core.hooksPath against the top of the worktree.
	return filepath.Join(a.loc.RepoRoot, configured), nil
}

// hookRunData is the `{hook, action, result}` from the CLI contract. Action is
// what was done — harvest, remind, or skipped — and result says why.
type hookRunData struct {
	Hook   string `json:"hook"`
	Action string `json:"action"`
	Result string `json:"result"`
}

const (
	hookActionHarvest = "harvest"
	hookActionRemind  = "remind"
	hookActionSkipped = "skipped"
)

func (a *app) newHooksRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <hook>",
		Short: "Run the Beadcrumbs action for one trigger point",
		Long: "Run the Beadcrumbs action for one trigger point: " +
			strings.Join(triggerNames(), ", ") + ".\n\n" +
			"This command never fails the operation that triggered it. A busy ledger, a missing " +
			"ledger, or a harvest that could not run all exit 0 with one line on stderr naming " +
			"what was skipped.\n\n" +
			"Whether it harvests or only reports is the repository's policy: with harvest.auto " +
			"off — the default — it counts what is outstanding and writes nothing.",
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			ledgerAnnotation: string(ledgerOptional),
			waitAnnotation:   hookMaxOpenWait.String(),
		},
	}
	cmd.RunE = a.handle(func(cmd *cobra.Command, args []string) (result, error) {
		hook := args[0]
		mayHarvest, known := hookTriggers[hook]
		if !known {
			// A trigger name that is not in the table is a configuration
			// mistake in whoever called us, and it is worth failing on: the
			// shim's `|| true` keeps it from reaching the git operation.
			return result{}, ledger.Fail(ledger.ErrInvalidInput, "invalid_hook",
				"%q is not a hook trigger; expected one of %s", hook, strings.Join(triggerNames(), ", "))
		}
		return a.runHook(cmd.Context(), hook, mayHarvest), nil
	})
	return cmd
}

// runHook is the whole decision, and it returns no error: every failure below
// is a reason to decline, not a reason to fail the git or session operation
// that triggered this. Each declined path leaves a warning, which is what the
// caller sees on stderr.
func (a *app) runHook(ctx context.Context, hook string, mayHarvest bool) result {
	data := hookRunData{Hook: hook, Action: hookActionSkipped}
	decline := func(format string, args ...any) result {
		data.Result = fmt.Sprintf(format, args...)
		a.out.warn("hook_skipped", fmt.Sprintf("%s: %s", hook, data.Result))
		return result{Data: data, Human: func(w io.Writer) { renderHookRun(w, data) }}
	}

	led, err := a.ledger(ctx)
	if err != nil {
		return decline("%s", errorMessage(err))
	}
	page, err := led.Crumbs(ctx, ledger.CrumbQuery{States: []ledger.ReviewState{ledger.StateCandidate}})
	if err != nil {
		return decline("the outstanding Crumbs could not be read: %s", errorMessage(err))
	}
	if page.Total == 0 {
		data.Result = "no unharvested Crumbs"
		return result{Data: data, Human: func(w io.Writer) { renderHookRun(w, data) }}
	}

	if !mayHarvest || !led.Config().HarvestAuto {
		data.Action = hookActionRemind
		data.Result = fmt.Sprintf("%d unharvested Crumb(s); run `bdc harvest` before this session ends", page.Total)
		// The reminder is the whole point of the trigger, so it goes to stderr
		// even in --json mode: a caller that never sees it loses the harvest.
		a.out.warn("unharvested_crumbs", data.Result)
		return result{Data: data, Human: func(w io.Writer) { renderHookRun(w, data) }}
	}

	res, err := led.CompleteHarvest(ctx, ledger.CompleteHarvest{
		Mode:  ledger.HarvestAutomatic,
		Since: earliestCapture(page.Crumbs),
	})
	if err != nil {
		return decline("the automatic harvest did not run: %s", errorMessage(err))
	}
	data.Action = hookActionHarvest
	data.Result = fmt.Sprintf("%s weighed %d Crumb(s)", res.Harvest.ID, res.Harvest.CrumbsConsidered)
	return result{Data: data, Human: func(w io.Writer) { renderHookRun(w, data) }}
}

// earliestCapture is the window an automatic harvest sweeps: everything still
// outstanding, and nothing older than the oldest thing outstanding. Choosing a
// fixed window instead would either miss Crumbs or invent a retention rule.
func earliestCapture(crumbs []ledger.Crumb) time.Time {
	earliest := time.Time{}
	for _, c := range crumbs {
		if earliest.IsZero() || c.CapturedAt.Before(earliest) {
			earliest = c.CapturedAt
		}
	}
	return earliest
}

func errorMessage(err error) string {
	_, message, _ := errorBody(err)
	return message
}

func triggerNames() []string {
	names := make([]string, 0, len(hookTriggers))
	for name := range hookTriggers {
		names = append(names, name)
	}
	// A map iterates in a random order and this string reaches --help.
	slices.Sort(names)
	return names
}

func renderHooks(w io.Writer, d hooksData) {
	for _, h := range d.Hooks {
		fmt.Fprintf(w, "%s %s (%s)\n", h.Action, h.Hook, h.Path)
		switch h.Action {
		case "chained":
			fmt.Fprintf(w, "  the pre-existing hook runs first, from %s%s\n", h.Hook, priorSuffix)
		case "restored":
			fmt.Fprintln(w, "  the hook it replaced is back in place")
		case "foreign":
			fmt.Fprintln(w, "  bdc did not write this hook and left it alone")
		}
	}
	if d.AutoHarvest {
		fmt.Fprintln(w, "automatic harvesting is on for this repository")
		return
	}
	fmt.Fprintln(w, "automatic harvesting is off; the hooks report and write nothing")
}

func renderHookRun(w io.Writer, d hookRunData) {
	fmt.Fprintf(w, "%s: %s — %s\n", d.Hook, d.Action, d.Result)
}
