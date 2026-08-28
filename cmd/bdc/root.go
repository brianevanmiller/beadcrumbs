package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
	"github.com/brianevanmiller/beadcrumbs/internal/redact"
	"github.com/brianevanmiller/beadcrumbs/internal/store/dolt"
)

// version is the bdc release version reported in every envelope's meta.
const version = "1.0.0"

// ledgerMode declares what a command needs before its body runs. Cobra has no
// typed per-command metadata, so it travels as an annotation; an unrecognised
// value panics because it can only be a coding mistake.
type ledgerMode string

const (
	ledgerRequired ledgerMode = "required" // the default: open or fail
	ledgerOptional ledgerMode = "optional" // doctor: report whatever it finds
	ledgerAbsent   ledgerMode = "none"     // init, restore: need the Location, not the engine
	ledgerDetached ledgerMode = "detached" // version: needs neither
)

const (
	ledgerAnnotation = "bdc.ledger"
	waitAnnotation   = "bdc.maxOpenWait"
	holdAnnotation   = "bdc.maxOpenHold"
)

// app is one invocation. It is the only place the ledger is opened and closed,
// which is what makes "one short-lived engine per command" structural rather
// than a rule every command body has to remember.
type app struct {
	out *emitter

	cwd     string
	loc     dolt.Location
	store   *dolt.Store
	openErr error
	led     *ledger.Ledger

	// Recorded by the command body so main can emit exactly one envelope.
	command string
	result  result
	bodyRan bool

	jsonOut   bool
	quiet     bool
	directory string
	actor     string
	actorKind string
	model     string
	session   string
	noEnrich  bool
}

func newApp(stdout, stderr io.Writer) *app {
	return &app{out: newEmitter(stdout, stderr), command: "unknown"}
}

// closer exposes the open store as an io.Closer without leaking a typed nil,
// which would make a nil check on the interface pass and Close panic.
func (a *app) closer() io.Closer {
	if a.store == nil {
		return nil
	}
	return a.store
}

func (a *app) newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "bdc",
		Short:         "Beadcrumbs — a repository-local reasoning ledger",
		SilenceUsage:  true,
		SilenceErrors: true,
		// `bdc` and `bdc --help` must work outside a repository, so the root
		// itself needs nothing from the ledger.
		Annotations: map[string]string{ledgerAnnotation: string(ledgerDetached)},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageError(fmt.Errorf("unknown command %q; run `bdc --help`", args[0]))
			}
			return usageError(errors.New("no command given; run `bdc --help`"))
		},
		PersistentPreRunE: a.prepare,
	}

	f := root.PersistentFlags()
	f.BoolVar(&a.jsonOut, "json", false, "emit the JSON envelope on stdout")
	f.BoolVar(&a.quiet, "quiet", false, "suppress warnings on stderr")
	f.StringVarP(&a.directory, "directory", "C", "", "run as if bdc were started in this directory")
	f.StringVar(&a.actor, "actor", defaultActor(), "who is acting (recorded as provenance)")
	f.StringVar(&a.actorKind, "actor-kind", envOr("BDC_ACTOR_KIND", "human"), "human or agent")
	f.StringVar(&a.model, "model", os.Getenv("BDC_ACTOR_MODEL"), "acting agent's model identifier")
	f.StringVar(&a.session, "session", os.Getenv("BDC_SESSION"), "session identifier for grouping provenance")
	f.BoolVar(&a.noEnrich, "no-enrich", false, "skip optional tracker enrichment")

	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError(err) })

	root.AddCommand(
		a.newVersionCommand(),
		a.newInitCommand(),
		a.newDoctorCommand(),
		a.newBackupCommand(),
		a.newRestoreCommand(),
		a.newGCCommand(),
		a.newCaptureCommand(),
		a.newCrumbCommand(),
		a.newHarvestCommand(),
		a.newInsightCommand(),
		a.newReferenceCommand(),
		a.newValidateCommand(),
		a.newAuthorityCommand(),
		a.newPromoteCommand(),
	)
	return root
}

// prepare resolves the ledger for the command about to run. It is the only
// caller of dolt.Discover and dolt.Open.
func (a *app) prepare(cmd *cobra.Command, _ []string) error {
	a.command = commandName(cmd)
	a.out.jsonMode = a.jsonOut
	a.out.quiet = a.quiet
	a.out.schema = func() int { return dolt.CurrentSchemaVersion() }

	if a.actorKind != "human" && a.actorKind != "agent" {
		return ledger.Fail(ledger.ErrInvalidInput, "invalid_actor_kind",
			"--actor-kind must be human or agent, got %q", a.actorKind)
	}

	cwd := a.directory
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return ledger.FailWith(ledger.ErrInvalidInput, "invalid_directory", err,
				"cannot determine the current directory")
		}
		cwd = wd
	}
	a.cwd = cwd

	mode := ledgerModeOf(cmd)
	loc, discoverErr := dolt.Discover(cmd.Context(), cwd)
	a.loc = loc
	switch {
	case discoverErr == nil:
	case mode == ledgerDetached:
		return nil
	case mode == ledgerRequired:
		return discoverErr
	case errors.Is(discoverErr, ledger.ErrNoLedger):
		// init creates it; doctor and restore report or replace it.
		a.openErr = discoverErr
		if mode == ledgerAbsent {
			a.openErr = nil
		}
		return nil
	default:
		return discoverErr
	}

	if mode == ledgerDetached || mode == ledgerAbsent {
		return nil
	}

	store, err := dolt.Open(cmd.Context(), loc, dolt.Config{
		MaxOpenWait: durationAnnotation(cmd, waitAnnotation),
		MaxOpenHold: durationAnnotation(cmd, holdAnnotation),
		Command:     a.command,
		ActorKind:   a.actorKind,
	})
	if err != nil {
		if mode == ledgerOptional {
			a.openErr = err
			return nil
		}
		return err
	}
	a.store = store

	schema, err := store.SchemaVersion(cmd.Context())
	if err != nil {
		return err
	}
	a.out.schema = func() int { return schema }
	return nil
}

// ledger builds the domain module for the command about to run, once per
// invocation. It lives here because the Redactor is constructed from
// repo_config, which has to be read from the open store *before* the Ledger
// that injects it exists — so this is the one place that ordering is expressed.
func (a *app) ledger(ctx context.Context) (*ledger.Ledger, error) {
	if a.led != nil {
		return a.led, nil
	}
	if a.store == nil {
		if a.openErr != nil {
			return nil, a.openErr
		}
		return nil, ledger.Fail(ledger.ErrNoLedger, "no_ledger",
			"this command needs an open ledger; run `bdc init` first")
	}
	cfg, err := ledger.LoadRepoConfig(ctx, a.store)
	if err != nil {
		return nil, err
	}
	redactor, err := redact.New(redact.Config{
		Version:  cfg.RedactionVersion,
		Patterns: cfg.RedactPatterns,
	})
	if err != nil {
		return nil, err
	}
	actor := ledger.Provenance{
		ActorID:    a.actor,
		ActorKind:  ledger.ActorKind(a.actorKind),
		ActorModel: a.model,
		SessionID:  a.session,
	}
	if err := actor.Validate(); err != nil {
		return nil, err
	}
	a.led = ledger.New(a.store, ledger.Options{Actor: actor, Redactor: redactor, Config: cfg})
	return a.led, nil
}

// warnRedaction reports that stored text was rewritten. A caller has to be able
// to see that its content changed before it was persisted; the finding names the
// rule and the position and never the secret.
func (a *app) warnRedaction(findings []ledger.Finding) {
	for _, f := range findings {
		a.out.warn("redacted", fmt.Sprintf("rule %s replaced %d bytes at offset %d",
			f.Rule, f.Length, f.Offset))
	}
}

// handle adapts a command body to Cobra while recording the outcome, so main
// emits exactly one envelope no matter where the failure came from.
func (a *app) handle(fn func(*cobra.Command, []string) (result, error)) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		res, err := fn(cmd, args)
		a.bodyRan = true
		a.command = commandName(cmd)
		a.result = res
		return err
	}
}

// commandName is the dotted envelope command: `bdc crumb list` -> "crumb.list".
func commandName(cmd *cobra.Command) string {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) <= 1 {
		return "bdc"
	}
	return strings.Join(parts[1:], ".")
}

func ledgerModeOf(cmd *cobra.Command) ledgerMode {
	raw, ok := cmd.Annotations[ledgerAnnotation]
	if !ok {
		return ledgerRequired
	}
	switch mode := ledgerMode(raw); mode {
	case ledgerRequired, ledgerOptional, ledgerAbsent, ledgerDetached:
		return mode
	default:
		panic(fmt.Sprintf("bdc: command %q declares unknown ledger mode %q", cmd.CommandPath(), raw))
	}
}

func durationAnnotation(cmd *cobra.Command, key string) time.Duration {
	raw, ok := cmd.Annotations[key]
	if !ok {
		return 0 // dolt.Config applies its default
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		panic(fmt.Sprintf("bdc: command %q declares %s=%q, which is not a duration", cmd.CommandPath(), key, raw))
	}
	return d
}

func defaultActor() string {
	if actor := os.Getenv("BDC_ACTOR"); actor != "" {
		return actor
	}
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	return "unknown"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
