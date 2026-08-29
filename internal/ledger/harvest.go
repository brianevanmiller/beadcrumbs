package ledger

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Harvest synthesis.
//
// CompleteHarvest is one operation that does two things, and that is the point:
// it persists new candidate Crumbs *and* synthesises selected Crumbs into an
// Insight revision. Split in two, a caller could persist candidates without the
// policy and redaction versions that judged them attached, which is exactly the
// invariant the operation exists to hold.
//
// Three rules hold across this file:
//
//   - Crumbs are never consumed. Harvest inputs are many-to-many, roles are
//     considered versus selected, and a Crumb stays available to every later
//     Harvest and Insight. Nothing here mutates a Crumb.
//   - The `failed` and `aborted` outcomes are unreachable from inside the
//     transaction, because the transaction rolls back entirely. They are
//     written afterwards, by a second transaction that carries the outcome, the
//     failure code, the counts, and no content of any kind.
//   - Redaction runs before the transaction opens. A redaction abort therefore
//     records `failure_code='redaction_failed'` and persists nothing else —
//     which is what exit code 7 promises.

// maxRevisionChars mirrors ck_rev_size. Synthesised content is a document
// rather than a fragment, so it is bounded two orders of magnitude above a
// Crumb — but it is still bounded.
const maxRevisionChars = 262144

// The failure codes a Harvest records. They are a closed set here rather than
// free text because `bdc doctor` and the skill both branch on them, and a
// failure code assembled from an error string would change under any wording
// change.
const (
	failureRedaction = "redaction_failed"
	failureCancelled = "cancelled"
	failureDryRun    = "dry_run"
	failureNotFound  = "crumb_not_found"
	failureInvalid   = "invalid_input"
	failureBusy      = "ledger_busy"
	failureIntegrity = "integrity_error"
	failureStorage   = "storage_error"
)

// CompleteHarvest is one synthesis run.
//
// Captures are new fragments from bounded session material and Crumbs are
// existing ones the caller named; both are selected when the Harvest concludes
// something and merely considered when it does not. Since widens the considered
// set to every candidate captured at or after that time, and those are never
// selected: selecting a Crumb is a judgement, and a time window is not one.
//
// Title, Content, and Class travel together: all three present means the
// Harvest synthesises an Insight, all three absent means it records what it
// weighed and stops. That second form is the durable-completion step a session
// runs before compaction, when there is nothing to conclude yet.
type CompleteHarvest struct {
	Mode       HarvestMode
	Captures   []CaptureCrumb
	Crumbs     []CrumbID
	Since      time.Time
	Title      string
	Content    string
	Class      string
	Confidence float64
	DryRun     bool
}

// HarvestResult is the `{harvest, insight, revision, crumbs_captured[]}` the CLI
// contract promises. Insight and Revision are nil when the Harvest weighed
// Crumbs without concluding anything, and on a dry run — a preview that minted
// ids would be reporting records that do not exist.
type HarvestResult struct {
	Harvest        Harvest          `json:"harvest"`
	Insight        *Insight         `json:"insight"`
	Revision       *InsightRevision `json:"revision"`
	CrumbsCaptured []Crumb          `json:"crumbs_captured"`
	Findings       []Finding        `json:"-"`
}

// synthesises reports whether this Harvest concludes something. The three
// fields are validated as a group, so any one of them means all three.
func (c CompleteHarvest) synthesises() bool {
	return c.Title != "" || c.Content != "" || c.Class != ""
}

func (c CompleteHarvest) validate() error {
	switch c.Mode {
	case HarvestManual, HarvestAutomatic:
	default:
		return Fail(ErrInvalidInput, "invalid_harvest_mode",
			"a harvest is manual or automatic, got %q", c.Mode)
	}
	if !c.synthesises() {
		if c.Since.IsZero() && len(c.Crumbs) == 0 && len(c.Captures) == 0 {
			return Fail(ErrInvalidInput, "invalid_selection",
				"a harvest needs something to weigh: pass --crumb or --since")
		}
		return nil
	}
	if c.Title == "" || c.Content == "" || c.Class == "" {
		return Fail(ErrInvalidInput, "invalid_usage",
			"a synthesis needs --title, --content, and --class together")
	}
	if len(c.Crumbs) == 0 && len(c.Captures) == 0 {
		return Fail(ErrInvalidInput, "invalid_selection",
			"an Insight is synthesised from at least one Crumb: pass --crumb")
	}
	if err := ValidateClass(c.Class); err != nil {
		return err
	}
	return ValidateConfidence(c.Confidence)
}

// harvestRun is what the failure path needs to know. It is threaded through the
// operation so a `harvests` row written after a rollback still reports the id,
// the mode, and what the run had counted when it stopped.
type harvestRun struct {
	id         HarvestID
	mode       HarvestMode
	started    time.Time
	considered int
	selected   int
}

// CompleteHarvest persists new candidate Crumbs and synthesises the selected
// ones into revision 1 of a new Insight.
func (l *Ledger) CompleteHarvest(ctx context.Context, c CompleteHarvest) (HarvestResult, error) {
	if err := l.actor.Validate(); err != nil {
		return HarvestResult{}, err
	}
	if c.Mode == "" {
		c.Mode = HarvestManual
	}
	if err := c.validate(); err != nil {
		return HarvestResult{}, err
	}
	run := harvestRun{id: NewHarvestID(), mode: c.Mode, started: l.clock()}

	// Redaction first, outside the transaction. A finding the redactor cannot
	// resolve aborts here, with nothing persisted but the failure row.
	prepared, err := l.prepareHarvest(run, c)
	if err != nil {
		return HarvestResult{}, l.harvestFailed(ctx, run, err)
	}

	if c.DryRun {
		return l.previewHarvest(ctx, run, c, prepared)
	}

	var out HarvestResult
	err = l.store.Write(ctx, func(tx Tx) error {
		// Rebuilt per attempt: Write may roll back, and a caller must never see
		// a result assembled from a transaction that did not commit.
		out = HarvestResult{CrumbsCaptured: []Crumb{}, Findings: prepared.findings}
		return l.writeHarvest(tx, &run, c, prepared, &out)
	})
	if err != nil {
		return HarvestResult{}, l.harvestFailed(ctx, run, err)
	}
	return out, nil
}

// preparedHarvest is everything redaction produced, held between the redaction
// pass and the transaction so no free text crosses the transaction boundary
// unredacted.
type preparedHarvest struct {
	crumbs   []Crumb
	refs     [][]RefSpec
	title    string
	content  string
	findings []Finding
}

func (l *Ledger) prepareHarvest(run harvestRun, c CompleteHarvest) (preparedHarvest, error) {
	var p preparedHarvest
	for _, capture := range c.Captures {
		// A Crumb captured by a Harvest carries the Harvest and the policy
		// version that judged it; ck_crumbs_harvest_policy enforces the pair.
		// Automatic mode also refuses transcript-shaped input, which redaction
		// does not and cannot do.
		capture.HarvestID = run.id
		capture.PolicyVersion = l.config.PolicyVersion
		capture.Automatic = capture.Automatic || run.mode == HarvestAutomatic
		crumb, findings, err := l.prepareCrumb(capture)
		if err != nil {
			return preparedHarvest{}, err
		}
		p.crumbs = append(p.crumbs, crumb)
		p.refs = append(p.refs, capture.References)
		p.findings = append(p.findings, findings...)
	}
	if !c.synthesises() {
		return p, nil
	}

	title, findings, err := l.redactField("insight title", strings.TrimSpace(c.Title))
	if err != nil {
		return preparedHarvest{}, err
	}
	p.findings = append(p.findings, findings...)
	content, findings, err := l.redactField("insight content", strings.TrimSpace(c.Content))
	if err != nil {
		return preparedHarvest{}, err
	}
	p.findings = append(p.findings, findings...)
	if err := validateRevisionText(title, content); err != nil {
		return preparedHarvest{}, err
	}
	l.assertRedacted("insight_revisions.title", title)
	l.assertRedacted("insight_revisions.content", content)
	p.title, p.content = title, content
	return p, nil
}

func validateRevisionText(title, content string) error {
	switch {
	case title == "":
		return Fail(ErrInvalidInput, "invalid_content", "an Insight needs a title")
	case utf8.RuneCountInString(title) > 512:
		return Fail(ErrInvalidInput, "invalid_content_size", "an Insight title is at most 512 characters")
	case content == "":
		return Fail(ErrInvalidInput, "invalid_content", "an Insight needs content")
	case utf8.RuneCountInString(content) > maxRevisionChars:
		return Fail(ErrInvalidInput, "invalid_content_size",
			"synthesised content is at most %d characters", maxRevisionChars)
	}
	return nil
}

// writeHarvest is the whole successful path, in the one order the foreign keys
// admit: the Harvest row, then the Crumbs it captured, then their roles, then
// the revision they support.
func (l *Ledger) writeHarvest(tx Tx, run *harvestRun, c CompleteHarvest, p preparedHarvest, out *HarvestResult) error {
	named, window, err := l.resolveHarvestCrumbs(tx, c)
	if err != nil {
		return err
	}

	// Deduplicate the captures against this session before counting them: two
	// identical captures, or a capture of text already held, resolve to one
	// Crumb and therefore to one role.
	captured := make([]Crumb, 0, len(p.crumbs))
	fresh := make([]int, 0, len(p.crumbs))
	for i, crumb := range p.crumbs {
		existing, duplicate, err := l.sessionDuplicate(tx, crumb.ContentHash)
		if err != nil {
			return err
		}
		if duplicate {
			captured = append(captured, existing)
			continue
		}
		captured = append(captured, crumb)
		fresh = append(fresh, i)
	}

	// Every Crumb the run touched has a role. A named or captured Crumb is
	// selected when the Harvest concludes something — it was weighed for this
	// conclusion — and considered when there is nothing to select it into.
	roles := map[CrumbID]HarvestRole{}
	inputs := make([]CrumbID, 0, len(named)+len(captured))
	inputs = append(inputs, named...)
	for _, crumb := range captured {
		inputs = append(inputs, crumb.ID)
	}
	for _, id := range window {
		roles[id] = RoleConsidered
	}
	for _, id := range inputs {
		roles[id] = RoleConsidered
	}
	var selected []CrumbID
	if c.synthesises() {
		selected = dedupeCrumbIDs(inputs)
		for _, id := range selected {
			roles[id] = RoleSelected
		}
	}
	run.considered, run.selected = 0, 0
	for _, role := range roles {
		run.considered++
		if role == RoleSelected {
			run.selected++
		}
	}

	harvest := l.harvestRow(*run, HarvestCompleted, "")
	// Only the Crumbs that already exist can be linked with the Harvest row;
	// the ones it captures do not exist yet.
	existingLinks := make([]HarvestCrumb, 0, len(roles))
	freshIDs := map[CrumbID]struct{}{}
	for _, i := range fresh {
		freshIDs[p.crumbs[i].ID] = struct{}{}
	}
	for id, role := range roles {
		if _, isFresh := freshIDs[id]; !isFresh {
			existingLinks = append(existingLinks, HarvestCrumb{CrumbID: id, Role: role})
		}
	}
	if err := tx.InsertHarvest(harvest, sortHarvestLinks(existingLinks)); err != nil {
		return err
	}

	freshLinks := make([]HarvestCrumb, 0, len(fresh))
	for _, i := range fresh {
		crumb := p.crumbs[i]
		if err := insertCrumb(tx, crumb, p.refs[i]); err != nil {
			return err
		}
		freshLinks = append(freshLinks, HarvestCrumb{CrumbID: crumb.ID, Role: roles[crumb.ID]})
	}
	if err := tx.AppendHarvestCrumbs(run.id, sortHarvestLinks(freshLinks)); err != nil {
		return err
	}

	out.Harvest = harvest
	out.CrumbsCaptured = captured
	if !c.synthesises() {
		return nil
	}

	insight := Insight{
		ID: NewInsightID(), HeadRevision: 1, CreatedAt: harvest.FinishedAt, Provenance: l.actor,
	}
	revision := InsightRevision{
		ID: NewRevisionID(), InsightID: insight.ID, Revision: 1,
		Title: p.title, Content: p.content, ContentHash: hashContent(p.content),
		Class: c.Class, Confidence: c.Confidence,
		HarvestID: run.id, CreatedAt: harvest.FinishedAt, Provenance: l.actor,
	}
	if err := tx.InsertRevision(revision, selected); err != nil {
		return err
	}
	out.Insight, out.Revision = &insight, &revision
	return nil
}

// resolveHarvestCrumbs reads the existing Crumbs this run weighed: the ones the
// caller named, and the ones its window swept up. Every named Crumb must exist
// — a harvest that silently dropped an id would synthesise an Insight from
// evidence the caller believes it cited.
func (l *Ledger) resolveHarvestCrumbs(snap Snapshot, c CompleteHarvest) (named, window []CrumbID, err error) {
	if len(c.Crumbs) > 0 {
		named = dedupeCrumbIDs(c.Crumbs)
		found, err := snap.Crumbs(CrumbQuery{IDs: named})
		if err != nil {
			return nil, nil, err
		}
		present := make(map[CrumbID]struct{}, len(found))
		for _, crumb := range found {
			present[crumb.ID] = struct{}{}
		}
		for _, id := range named {
			if _, ok := present[id]; !ok {
				return nil, nil, NotFound("crumb", string(id))
			}
		}
	}
	if !c.Since.IsZero() {
		// Candidates only: a reviewed Crumb has already had its judgement, and
		// a window is a sweep of what is still outstanding.
		swept, err := snap.Crumbs(CrumbQuery{States: []ReviewState{StateCandidate}, Since: c.Since})
		if err != nil {
			return nil, nil, err
		}
		for _, crumb := range swept {
			window = append(window, crumb.ID)
		}
	}
	return named, window, nil
}

// previewHarvest is `--dry-run`: everything except the writes that would matter.
// It still records an `aborted` Harvest, because "we looked and did not commit"
// is the outcome the ledger exists to remember.
func (l *Ledger) previewHarvest(ctx context.Context, run harvestRun, c CompleteHarvest, p preparedHarvest) (HarvestResult, error) {
	if err := l.store.Read(ctx, func(snap Snapshot) error {
		named, window, err := l.resolveHarvestCrumbs(snap, c)
		if err != nil {
			return err
		}
		inputs := len(dedupeCrumbIDs(named)) + len(p.crumbs)
		run.considered = len(dedupeCrumbIDs(append(append([]CrumbID{}, window...), named...))) + len(p.crumbs)
		if c.synthesises() {
			run.selected = inputs
		}
		return nil
	}); err != nil {
		return HarvestResult{}, l.harvestFailed(ctx, run, err)
	}

	harvest, err := l.recordHarvestOutcome(ctx, run, HarvestAborted, failureDryRun)
	if err != nil {
		return HarvestResult{}, err
	}
	return HarvestResult{Harvest: harvest, CrumbsCaptured: []Crumb{}, Findings: p.findings}, nil
}

// harvestFailed records the outcome and returns the original error. The failure
// row is best effort by construction: it needs the engine, and "the engine is
// unavailable" is one of the things that lands here. Losing the row is
// acceptable; masking why the harvest failed is not.
func (l *Ledger) harvestFailed(ctx context.Context, run harvestRun, cause error) error {
	outcome := HarvestFailed
	code := harvestFailureCode(cause)
	if code == failureCancelled {
		outcome = HarvestAborted
	}
	if _, err := l.recordHarvestOutcome(ctx, run, outcome, code); err != nil {
		return cause
	}
	var le *Error
	if errors.As(cause, &le) {
		if le.Details == nil {
			le.Details = map[string]any{}
		}
		le.Details["harvest_id"] = string(run.id)
	}
	return cause
}

// recordHarvestOutcome writes the `harvests` row for an outcome the main
// transaction cannot reach, because that transaction rolled back. It writes the
// row and nothing else: no Crumb, no revision, no content of any kind.
//
// The context is detached because a cancelled context is one of the reasons a
// harvest ends up here, and a failure row that cannot be written on
// cancellation is a failure row that never exists.
func (l *Ledger) recordHarvestOutcome(ctx context.Context, run harvestRun, outcome HarvestOutcome, code string) (Harvest, error) {
	harvest := l.harvestRow(run, outcome, code)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := l.store.Write(ctx, func(tx Tx) error {
		return tx.InsertHarvest(harvest, nil)
	}); err != nil {
		return Harvest{}, err
	}
	return harvest, nil
}

func (l *Ledger) harvestRow(run harvestRun, outcome HarvestOutcome, code string) Harvest {
	return Harvest{
		ID:               run.id,
		Mode:             run.mode,
		Outcome:          outcome,
		FailureCode:      code,
		CrumbsConsidered: run.considered,
		CrumbsSelected:   run.selected,
		PolicyVersion:    l.config.PolicyVersion,
		RedactionVersion: l.redactor.Version(),
		StartedAt:        run.started,
		FinishedAt:       l.clock(),
		Provenance:       l.actor,
	}
}

// harvestFailureCode maps an error to the closed set of codes the `harvests`
// row records. Nothing here reads the error's text.
func harvestFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrRedaction):
		return failureRedaction
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return failureCancelled
	case errors.Is(err, ErrBusy):
		return failureBusy
	case errors.Is(err, ErrNotFound):
		return failureNotFound
	case errors.Is(err, ErrInvalidInput):
		return failureInvalid
	case errors.Is(err, ErrIntegrity):
		return failureIntegrity
	default:
		return failureStorage
	}
}

// dedupeCrumbIDs keeps first-seen order. A caller that passes the same Crumb
// twice means it once, and insight_crumbs has a composite primary key that
// would otherwise reject the second row and abort the whole synthesis.
func dedupeCrumbIDs(ids []CrumbID) []CrumbID {
	seen := make(map[CrumbID]struct{}, len(ids))
	out := make([]CrumbID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// sortHarvestLinks orders by id so a Harvest's rows land in a deterministic
// order. Map iteration is randomised, and a nondeterministic write order makes
// two identical runs produce two different Dolt diffs.
func sortHarvestLinks(links []HarvestCrumb) []HarvestCrumb {
	slices.SortFunc(links, func(a, b HarvestCrumb) int {
		return strings.Compare(string(a.CrumbID), string(b.CrumbID))
	})
	return links
}
