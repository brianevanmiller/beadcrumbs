package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The three bounded readings of the ledger: `bdc context`, `bdc handoff`, and
// `bdc prime`. All three answer "what may I rely on" rather than "what is
// stored", so they share one type and one budget discipline and differ only in
// which sections they populate.
//
// Two rules hold across this file:
//
//   - Standing is computed in exactly one place. A reader must never have to
//     re-derive "may I act on this" from a verdict and an authority level,
//     because two callers deriving it differently is how an advisory note
//     becomes a rule. standingOf is that place.
//   - A budget drops the least load-bearing section first, and says so. Silent
//     truncation would make a partial answer indistinguishable from a complete
//     one, which for `prime` means an agent believing there are no mandatory
//     rules because they did not fit.

type NarrativeMode string

const (
	ModeContext NarrativeMode = "context"
	ModeHandoff NarrativeMode = "handoff"
	ModePrime   NarrativeMode = "prime"
)

// DefaultBudgetTokens is what `--budget` defaults to: enough for `context` and
// `prime` to render whole against a mid-sized ledger, and a fraction of a
// session's window. A caller that wants the whole answer passes `--budget 0`.
const DefaultBudgetTokens = 4000

const (
	// bytesPerToken is the approximation the budget is stated in. JSON of this
	// shape — short ASCII keys, identifiers, prose — runs close to four bytes
	// per token on every current tokenizer, and the flag says "approximate".
	bytesPerToken = 4

	// Excerpt caps. A rule an agent must follow gets more room than a Crumb it
	// is only being reminded of; neither is the record itself, and both name
	// the id to read in full.
	maxExcerpt     = 240
	maxRuleExcerpt = 480

	defaultNarrativeLimit = 10
)

// Standing is what a reader may do with an Insight. It is the whole of the
// interpretation contract: mandatory must be followed, a working default is
// settled unless a new review says otherwise, advisory is citable but not
// settled, and unusable means exactly that — a new review is required before
// the conclusion may be relied on again.
type Standing string

const (
	StandingMandatory      Standing = "mandatory"
	StandingWorkingDefault Standing = "working-default"
	StandingAdvisory       Standing = "advisory"
	StandingUnusable       Standing = "unusable"
)

// standingOf resolves the two independent judgement axes into the one answer a
// reader acts on. A disqualifying verdict beats any authority level: an Insight
// that was mandatory and is now disputed is unusable, not mandatory, because
// the authority was granted over a conclusion the latest review no longer
// supports.
func standingOf(v Verdict, a AuthorityLevel) Standing {
	switch v {
	case VerdictDisputed, VerdictRejected, VerdictSuperseded:
		return StandingUnusable
	}
	switch a {
	case AuthorityMandatory:
		return StandingMandatory
	case AuthorityDefault:
		return StandingWorkingDefault
	default:
		return StandingAdvisory
	}
}

// NarrativeQuery is one bounded read. Budget is in approximate tokens; zero
// means the caller wants the whole answer and will bound it itself.
type NarrativeQuery struct {
	Mode      NarrativeMode
	Since     time.Time
	InsightID InsightID // context only: narrow to one Insight
	Limit     int       // context only: how many Insights and Crumbs to consider
	Budget    int
}

// Narrative is all three shapes in one type, marshalled to exactly the keys the
// CLI contract declares for the mode that produced it. A single struct with
// omitempty would emit `handoff`'s keys from `prime`, and a caller cannot tell
// an absent section from an empty one.
type Narrative struct {
	Mode    NarrativeMode `json:"-"`
	Summary string        `json:"summary"`

	// context
	Insights      []NarrativeInsight   `json:"-"`
	OpenQuestions []NarrativeQuestion  `json:"-"`
	RecentCrumbs  []NarrativeCrumb     `json:"-"`
	Promotions    []NarrativePromotion `json:"-"`

	// handoff
	State            NarrativeState       `json:"-"`
	UnreviewedCrumbs int                  `json:"-"`
	OpenProposals    []NarrativePromotion `json:"-"`
	Workspace        NarrativeWorkspace   `json:"-"`

	// prime
	WorkingDefaults []NarrativeInsight `json:"-"`
	Mandatory       []NarrativeInsight `json:"-"`
	Cautions        []NarrativeCaution `json:"-"`

	// Notices travel beside the data as the CLI's warnings[]: a truncated
	// narrative is still a correct one, but the reader has to know it happened.
	Notices []Notice `json:"-"`
}

func (n Narrative) MarshalJSON() ([]byte, error) {
	switch n.Mode {
	case ModeContext:
		return json.Marshal(struct {
			Summary       string               `json:"summary"`
			Insights      []NarrativeInsight   `json:"insights"`
			OpenQuestions []NarrativeQuestion  `json:"open_questions"`
			RecentCrumbs  []NarrativeCrumb     `json:"recent_crumbs"`
			Promotions    []NarrativePromotion `json:"promotions"`
		}{n.Summary, n.Insights, n.OpenQuestions, n.RecentCrumbs, n.Promotions})
	case ModeHandoff:
		return json.Marshal(struct {
			Summary          string               `json:"summary"`
			State            NarrativeState       `json:"state"`
			UnreviewedCrumbs int                  `json:"unreviewed_crumbs"`
			OpenProposals    []NarrativePromotion `json:"open_proposals"`
			Workspace        NarrativeWorkspace   `json:"workspace"`
		}{n.Summary, n.State, n.UnreviewedCrumbs, n.OpenProposals, n.Workspace})
	case ModePrime:
		return json.Marshal(struct {
			Summary         string             `json:"summary"`
			WorkingDefaults []NarrativeInsight `json:"working_defaults"`
			Mandatory       []NarrativeInsight `json:"mandatory"`
			Cautions        []NarrativeCaution `json:"cautions"`
		}{n.Summary, n.WorkingDefaults, n.Mandatory, n.Cautions})
	default:
		return nil, fmt.Errorf("beadcrumbs: narrative has no mode; nothing can say which shape to emit")
	}
}

// NarrativeInsight is an Insight at its head revision, with the standing a
// reader acts on. Excerpt is a bounded quotation, never the record: the id is
// there so `bdc insight show` can produce the rest.
type NarrativeInsight struct {
	ID InsightID `json:"id"`

	// RevisionID is the head revision, and is the id `bdc validate` and
	// `bdc authority` take: judgements attach to a revision, never to the
	// Insight, so a narrative that reported only the Insight id would name a
	// record no follow-up command accepts.
	RevisionID RevisionID `json:"revision_id"`

	Revision   int            `json:"revision"`
	Title      string         `json:"title"`
	Class      string         `json:"class"`
	Confidence float64        `json:"confidence"`
	Verdict    Verdict        `json:"verdict"`
	Authority  AuthorityLevel `json:"authority"`
	Standing   Standing       `json:"standing"`
	Excerpt    string         `json:"excerpt,omitempty"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// NarrativeCaution is an Insight a reader must not act on. Reason is the code a
// script matches; Detail is the sentence a human needs, and says what changed
// rather than only what is wrong.
type NarrativeCaution struct {
	ID         InsightID  `json:"id"`
	RevisionID RevisionID `json:"revision_id"`

	Revision  int            `json:"revision"`
	Title     string         `json:"title"`
	Class     string         `json:"class"`
	Verdict   Verdict        `json:"verdict"`
	Authority AuthorityLevel `json:"authority"`
	Reason    string         `json:"reason"`
	Detail    string         `json:"detail"`
}

// NarrativeQuestion is a decision the ledger is waiting on. Every kind is a
// thing a person or agent can close; a question nobody can act on is noise.
type NarrativeQuestion struct {
	Kind     string    `json:"kind"`
	Subject  RecordRef `json:"subject,omitempty"`
	Question string    `json:"question"`
	Detail   string    `json:"detail,omitempty"`
}

type NarrativeCrumb struct {
	ID         CrumbID     `json:"id"`
	State      ReviewState `json:"state"`
	Confidence float64     `json:"confidence"`
	CapturedAt time.Time   `json:"captured_at"`
	Excerpt    string      `json:"excerpt"`
}

// NarrativePromotion is one proposal as a narrative shows it: where it would
// land, what it is waiting on, and whether anything durable came of it.
type NarrativePromotion struct {
	ID                ProposalID           `json:"id"`
	InsightID         InsightID            `json:"insight_id"`
	Class             string               `json:"class"`
	Destination       string               `json:"destination"`
	Status            PromotionStatus      `json:"status"`
	AuthorityRequired AuthorityRequirement `json:"authority_required"`
	AuthorityHeld     bool                 `json:"authority_held"`
	Attempts          int                  `json:"attempts"`
	Durable           bool                 `json:"durable"`
	ReceiptLocator    string               `json:"receipt_locator,omitempty"`
	CreatedAt         time.Time            `json:"created_at"`
}

// NarrativeState is the standing shape of the whole ledger, never windowed by
// --since: a reader taking over needs the totals, not the last hour of them.
type NarrativeState struct {
	Crumbs         map[ReviewState]int     `json:"crumbs"`
	Insights       int                     `json:"insights"`
	Revisions      int                     `json:"revisions"`
	Harvests       int                     `json:"harvests"`
	References     int                     `json:"references"`
	Proposals      int                     `json:"proposals"`
	Promotions     map[PromotionStatus]int `json:"promotions"`
	Mandatory      int                     `json:"mandatory"`
	Unusable       int                     `json:"unusable"`
	LastActivityAt *time.Time              `json:"last_activity_at,omitempty"`
}

// NarrativeWorkspace is enrichment freshness. It exists so a reader can tell
// live tracker state from a cache: every label a Reference carries was observed
// at some past moment, and handoff never refreshes — a handoff that made
// network calls would fail exactly when the session is ending.
type NarrativeWorkspace struct {
	Enrichment string             `json:"enrichment"`
	References []NarrativeRefKind `json:"references"`
}

// NarrativeRefKind is one adapter namespace's cache. Cached and Never are
// counts of the same set of References, split by whether anything was ever
// observed for them.
type NarrativeRefKind struct {
	Kind     string     `json:"kind"`
	Count    int        `json:"count"`
	Cached   int        `json:"cached"`
	Never    int        `json:"never"`
	Enricher string     `json:"enricher,omitempty"`
	OldestAt *time.Time `json:"oldest_fetched_at,omitempty"`
	NewestAt *time.Time `json:"newest_fetched_at,omitempty"`
}

// Narrative reads the ledger once and renders the mode's sections from that one
// snapshot: three commands assembled from three snapshots could report a Crumb
// count that no single moment ever had.
func (l *Ledger) Narrative(ctx context.Context, q NarrativeQuery) (Narrative, error) {
	switch q.Mode {
	case ModeContext, ModeHandoff, ModePrime:
	default:
		return Narrative{}, Fail(ErrInvalidInput, "invalid_narrative_mode",
			"%q is not a narrative mode; expected context, handoff, or prime", q.Mode)
	}
	if q.InsightID != "" {
		if _, err := ParseID(PrefixInsight, string(q.InsightID)); err != nil {
			return Narrative{}, err
		}
	}
	if q.Limit <= 0 {
		q.Limit = defaultNarrativeLimit
	}

	n := Narrative{Mode: q.Mode}
	err := l.store.Read(ctx, func(snap Snapshot) error {
		switch q.Mode {
		case ModeContext:
			return l.narrateContext(snap, q, &n)
		case ModeHandoff:
			return l.narrateHandoff(snap, q, &n)
		default:
			return l.narratePrime(snap, q, &n)
		}
	})
	if err != nil {
		return Narrative{}, err
	}
	notices, err := n.fit(q.Budget)
	if err != nil {
		return Narrative{}, err
	}
	n.Notices = notices
	return n, nil
}

// narrateContext answers "what is going on here". Its sections are ordered by
// how much a reader loses without them, which is also the order the budget
// drops them in.
func (l *Ledger) narrateContext(snap Snapshot, q NarrativeQuery, n *Narrative) error {
	iq := InsightQuery{Since: q.Since, Limit: q.Limit}
	if q.InsightID != "" {
		iq = InsightQuery{IDs: []InsightID{q.InsightID}}
	}
	insights, err := narrativeInsights(snap, iq, maxExcerpt)
	if err != nil {
		return err
	}
	if q.InsightID != "" && len(insights) == 0 {
		return NotFound("insight", string(q.InsightID))
	}
	n.Insights = insights

	// With --insight the Crumbs that matter are the ones that revision was
	// written from, not whatever was captured most recently.
	cq := CrumbQuery{Since: q.Since, Limit: q.Limit}
	if q.InsightID != "" {
		head, err := headRevisionID(snap, q.InsightID)
		if err != nil {
			return err
		}
		cq = CrumbQuery{RevisionIDs: []RevisionID{head}}
	}
	crumbs, err := snap.Crumbs(cq)
	if err != nil {
		return err
	}
	sortCrumbsNewestFirst(crumbs)
	if len(crumbs) > q.Limit {
		crumbs = crumbs[:q.Limit]
	}
	n.RecentCrumbs = make([]NarrativeCrumb, 0, len(crumbs))
	for _, c := range crumbs {
		n.RecentCrumbs = append(n.RecentCrumbs, NarrativeCrumb{
			ID: c.ID, State: c.ReviewState, Confidence: c.Confidence,
			CapturedAt: c.CapturedAt, Excerpt: excerpt(c.Content, maxExcerpt),
		})
	}

	promotions, err := narrativePromotions(snap, PromotionQuery{InsightID: q.InsightID})
	if err != nil {
		return err
	}
	n.Promotions = promotions

	counts, err := snap.Counts(CountQuery{})
	if err != nil {
		return err
	}
	n.OpenQuestions = openQuestions(insights, promotions, counts.CrumbsByState[StateCandidate])
	n.Summary = contextSummary(insights, n.RecentCrumbs, promotions, n.OpenQuestions, q)
	return nil
}

// narrateHandoff answers "where does the next session pick this up". State is
// the whole ledger; --since narrows only the new work being handed over.
func (l *Ledger) narrateHandoff(snap Snapshot, q NarrativeQuery, n *Narrative) error {
	total, err := snap.Counts(CountQuery{})
	if err != nil {
		return err
	}
	windowed, err := snap.Counts(CountQuery{Since: q.Since})
	if err != nil {
		return err
	}
	insights, err := narrativeInsights(snap, InsightQuery{}, 0)
	if err != nil {
		return err
	}

	state := NarrativeState{
		Crumbs: total.CrumbsByState, Insights: total.Insights, Revisions: total.Revisions,
		Harvests: total.Harvests, References: total.References, Proposals: total.Proposals,
		Promotions: total.PromotionsByStatus,
	}
	if state.Crumbs == nil {
		state.Crumbs = map[ReviewState]int{}
	}
	if state.Promotions == nil {
		state.Promotions = map[PromotionStatus]int{}
	}
	for _, i := range insights {
		switch i.Standing {
		case StandingMandatory:
			state.Mandatory++
		case StandingUnusable:
			state.Unusable++
		}
	}

	proposals, err := narrativePromotions(snap, PromotionQuery{})
	if err != nil {
		return err
	}
	open := make([]NarrativePromotion, 0, len(proposals))
	for _, p := range proposals {
		if !p.CreatedAt.Before(q.Since) && isOpen(p.Status) {
			open = append(open, p)
		}
	}

	state.LastActivityAt = lastActivity(insights, proposals)
	n.State = state
	n.UnreviewedCrumbs = windowed.CrumbsByState[StateCandidate]
	n.OpenProposals = open

	workspace, err := l.narrativeWorkspace(snap)
	if err != nil {
		return err
	}
	n.Workspace = workspace
	n.Summary = handoffSummary(state, n.UnreviewedCrumbs, open, workspace, q)
	return nil
}

// narratePrime answers "what may I rely on before I start". It is the only
// mode whose omissions are dangerous, which is why mandatory is never dropped.
func (l *Ledger) narratePrime(snap Snapshot, _ NarrativeQuery, n *Narrative) error {
	insights, err := narrativeInsights(snap, InsightQuery{}, maxRuleExcerpt)
	if err != nil {
		return err
	}
	n.WorkingDefaults = []NarrativeInsight{}
	n.Mandatory = []NarrativeInsight{}
	n.Cautions = []NarrativeCaution{}
	for _, i := range insights {
		switch i.Standing {
		case StandingMandatory:
			// Repeated in both lists on purpose: mandatory is a subset of the
			// working defaults, and an agent that reads only one of the two
			// must still be correct.
			n.Mandatory = append(n.Mandatory, i)
			n.WorkingDefaults = append(n.WorkingDefaults, i)
		case StandingWorkingDefault:
			n.WorkingDefaults = append(n.WorkingDefaults, i)
		case StandingUnusable:
			n.Cautions = append(n.Cautions, cautionFor(i))
		}
	}
	n.Summary = primeSummary(n.WorkingDefaults, n.Mandatory, n.Cautions, len(insights))
	return nil
}

// narrativeInsights lists Insights with their standing, and quotes the head
// revision when the caller asked for an excerpt. `prime` and `handoff` pass no
// limit on purpose — an agent that cannot see a mandatory rule will break it —
// so the excerpt content is read in one query for every Insight at once rather
// than one query each.
func narrativeInsights(snap Snapshot, q InsightQuery, excerptLimit int) ([]NarrativeInsight, error) {
	limit := q.Limit
	q.Limit = 0
	rows, err := snap.Insights(q)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}

	targets := make([]RecordRef, 0, len(rows))
	for _, row := range rows {
		targets = append(targets, RecordRef{Kind: KindRevision, ID: string(row.HeadRevisionID)})
	}
	verdicts, authorities, err := latestJudgements(snap, targets)
	if err != nil {
		return nil, err
	}

	content := map[RevisionID]string{}
	if excerptLimit > 0 && len(rows) > 0 {
		ids := make([]InsightID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		revisions, err := snap.Revisions(ids...)
		if err != nil {
			return nil, err
		}
		for _, r := range revisions {
			content[r.ID] = r.Content
		}
	}

	out := make([]NarrativeInsight, 0, len(rows))
	for _, row := range rows {
		item := NarrativeInsight{
			ID: row.ID, RevisionID: row.HeadRevisionID,
			Revision: row.HeadRevision, Title: row.Title, Class: row.Class,
			Confidence: row.Confidence, UpdatedAt: row.UpdatedAt,
			Verdict:   verdicts[string(row.HeadRevisionID)],
			Authority: authorities[string(row.HeadRevisionID)],
		}
		item.Standing = standingOf(item.Verdict, item.Authority)
		if excerptLimit > 0 {
			item.Excerpt = excerpt(content[row.HeadRevisionID], excerptLimit)
		}
		out = append(out, item)
	}
	return out, nil
}

// narrativePromotions folds each proposal's attempts into the one line a
// narrative shows. AuthorityHeld is read rather than assumed: "a human must
// decide" and "a human has decided" are different facts, and a reader that
// cannot tell them apart cannot know whether the proposal is waiting on them.
func narrativePromotions(snap Snapshot, q PromotionQuery) ([]NarrativePromotion, error) {
	proposals, err := snap.Proposals(q)
	if err != nil {
		return nil, err
	}
	ids := make([]ProposalID, 0, len(proposals))
	for _, p := range proposals {
		ids = append(ids, p.ID)
	}
	attempts, receipts, err := snap.Attempts(ids)
	if err != nil {
		return nil, err
	}
	byProposal := map[ProposalID][]Promotion{}
	for _, a := range attempts {
		byProposal[a.ProposalID] = append(byProposal[a.ProposalID], a)
	}
	byPromotion := map[PromotionID]Receipt{}
	for _, r := range receipts {
		byPromotion[r.PromotionID] = r
	}

	out := make([]NarrativePromotion, 0, len(proposals))
	for _, p := range proposals {
		item := NarrativePromotion{
			ID: p.ID, InsightID: p.InsightID, Class: p.Class,
			Destination:       p.DestKind + ":" + p.DestLocator,
			Status:            PromotionProposed,
			AuthorityRequired: AuthorityRequiredFor(p.Class, p.Capabilities, p.RequestedAuthority),
			Attempts:          len(byProposal[p.ID]),
			CreatedAt:         p.CreatedAt,
		}
		for _, a := range byProposal[p.ID] {
			item.Status = a.Status
			if r, ok := byPromotion[a.ID]; ok {
				item.ReceiptLocator = r.Locator
				item.Durable = durable(p.Capabilities, r)
			}
		}
		if item.AuthorityRequired == RequireHuman {
			held, err := humanAuthorityHeld(snap, gateFor(p))
			if err != nil {
				return nil, err
			}
			item.AuthorityHeld = held
		} else {
			item.AuthorityHeld = true
		}
		out = append(out, item)
	}
	return out, nil
}

// narrativeWorkspace summarises the observed cache per adapter namespace. v1
// ships no Enricher, so the honest answer is usually "none configured, every
// label is the locator" — which is exactly what a reader needs to know before
// trusting a label.
func (l *Ledger) narrativeWorkspace(snap Snapshot) (NarrativeWorkspace, error) {
	refs, err := snap.References(ReferenceQuery{})
	if err != nil {
		return NarrativeWorkspace{}, err
	}
	enrichment := "none"
	if l.enricher != nil {
		enrichment = l.enricher.Kind()
	}
	byKind := map[string]*NarrativeRefKind{}
	for _, ref := range refs {
		k, ok := byKind[ref.Kind]
		if !ok {
			k = &NarrativeRefKind{Kind: ref.Kind}
			if l.enricher != nil && l.enricher.Kind() == ref.Kind {
				k.Enricher = ref.Kind
			}
			byKind[ref.Kind] = k
		}
		k.Count++
		if ref.FetchedAt.IsZero() {
			k.Never++
			continue
		}
		k.Cached++
		at := ref.FetchedAt
		if k.OldestAt == nil || at.Before(*k.OldestAt) {
			k.OldestAt = &at
		}
		if k.NewestAt == nil || at.After(*k.NewestAt) {
			k.NewestAt = &at
		}
	}
	out := NarrativeWorkspace{Enrichment: enrichment, References: []NarrativeRefKind{}}
	for _, k := range byKind {
		out.References = append(out.References, *k)
	}
	sort.Slice(out.References, func(i, j int) bool { return out.References[i].Kind < out.References[j].Kind })
	return out, nil
}

// openQuestions is every decision the ledger is waiting on, ordered so the one
// blocking the most work comes first.
func openQuestions(insights []NarrativeInsight, promotions []NarrativePromotion, unreviewed int) []NarrativeQuestion {
	out := []NarrativeQuestion{}
	if unreviewed > 0 {
		out = append(out, NarrativeQuestion{
			Kind:     "unreviewed_crumbs",
			Question: fmt.Sprintf("%d Crumb(s) are waiting on review", unreviewed),
			Detail:   "run `bdc crumb list --state candidate`, then `bdc crumb review <id>... --state accepted|rejected --rationale ...`",
		})
	}
	for _, p := range promotions {
		if p.AuthorityRequired == RequireHuman && !p.AuthorityHeld && isOpen(p.Status) {
			out = append(out, NarrativeQuestion{
				Kind:     "authority_required",
				Subject:  RecordRef{Kind: KindProposal, ID: string(p.ID)},
				Question: fmt.Sprintf("promoting to %s needs a human decision", p.Destination),
				Detail:   "a human grants it with `bdc authority " + string(p.ID) + " --level default|mandatory --rationale ...`",
			})
		}
		if p.Status == PromotionFailed {
			out = append(out, NarrativeQuestion{
				Kind:     "failed_promotion",
				Subject:  RecordRef{Kind: KindProposal, ID: string(p.ID)},
				Question: fmt.Sprintf("the last attempt at %s did not land", p.Destination),
				Detail:   "retry with `bdc promote record`, or close it with `bdc promote reject`",
			})
		}
	}
	for _, i := range insights {
		if i.Standing != StandingUnusable {
			continue
		}
		out = append(out, NarrativeQuestion{
			Kind:     "unusable_insight",
			Subject:  RecordRef{Kind: KindRevision, ID: string(i.RevisionID)},
			Question: fmt.Sprintf("%q is %s and cannot be relied on", i.Title, i.Verdict),
			Detail: "resolve it with `bdc validate " + string(i.RevisionID) +
				" --verdict ... --rationale ...`, or revise the Insight",
		})
	}
	return out
}

func cautionFor(i NarrativeInsight) NarrativeCaution {
	c := NarrativeCaution{
		ID: i.ID, RevisionID: i.RevisionID, Revision: i.Revision, Title: i.Title, Class: i.Class,
		Verdict: i.Verdict, Authority: i.Authority, Reason: string(i.Verdict),
		Detail: fmt.Sprintf("%s at revision %d; unusable without a new review", i.Verdict, i.Revision),
	}
	// An Insight that carried force and lost it is the caution that matters
	// most: something downstream may still be following it.
	if i.Authority == AuthorityMandatory || i.Authority == AuthorityDefault {
		c.Detail = fmt.Sprintf("%s at revision %d, and still carries %s authority; "+
			"work that followed it may need revisiting", i.Verdict, i.Revision, i.Authority)
	}
	return c
}

func contextSummary(insights []NarrativeInsight, crumbs []NarrativeCrumb,
	promotions []NarrativePromotion, questions []NarrativeQuestion, q NarrativeQuery) string {
	var mandatory, unusable int
	for _, i := range insights {
		switch i.Standing {
		case StandingMandatory:
			mandatory++
		case StandingUnusable:
			unusable++
		}
	}
	scope := "the ledger"
	if q.InsightID != "" {
		scope = string(q.InsightID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d Insight(s) from %s%s: %d mandatory, %d unusable. ",
		len(insights), scope, sinceClause(q.Since), mandatory, unusable)
	fmt.Fprintf(&b, "%d recent Crumb(s), %d proposal(s), %d open question(s).",
		len(crumbs), len(promotions), len(questions))
	return b.String()
}

func handoffSummary(state NarrativeState, unreviewed int, open []NarrativePromotion,
	workspace NarrativeWorkspace, q NarrativeQuery) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d Insight(s) over %d revision(s), %d mandatory, %d unusable. ",
		state.Insights, state.Revisions, state.Mandatory, state.Unusable)
	fmt.Fprintf(&b, "%d Crumb(s) awaiting review%s, %d open proposal(s). ",
		unreviewed, sinceClause(q.Since), len(open))
	if workspace.Enrichment == "none" {
		fmt.Fprintf(&b, "No enricher is configured: every reference label is its own locator, "+
			"never live tracker state.")
	} else {
		fmt.Fprintf(&b, "Reference labels were observed by the %s enricher at the times shown "+
			"and are a cache, not live tracker state.", workspace.Enrichment)
	}
	return b.String()
}

func primeSummary(defaults, mandatory []NarrativeInsight, cautions []NarrativeCaution, total int) string {
	return fmt.Sprintf(
		"%d working default(s) of %d Insight(s), %d of them mandatory, and %d caution(s). "+
			"Mandatory must be followed. A working default is settled unless a new review says "+
			"otherwise. Anything not listed is advisory: citable, not settled. Disputed, "+
			"rejected, and superseded Insights are unusable without a new review.",
		len(defaults), total, len(mandatory), len(cautions))
}

func sinceClause(since time.Time) string {
	if since.IsZero() {
		return ""
	}
	return " since " + since.UTC().Format(time.RFC3339)
}

// isOpen reports whether a proposal is still waiting on someone. Rejected and
// superseded are decisions; applied is done; proposed and failed are work.
func isOpen(s PromotionStatus) bool {
	return s == PromotionProposed || s == PromotionFailed
}

func lastActivity(insights []NarrativeInsight, promotions []NarrativePromotion) *time.Time {
	var latest time.Time
	for _, i := range insights {
		if i.UpdatedAt.After(latest) {
			latest = i.UpdatedAt
		}
	}
	for _, p := range promotions {
		if p.CreatedAt.After(latest) {
			latest = p.CreatedAt
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}

func headRevisionID(snap Snapshot, id InsightID) (RevisionID, error) {
	rows, err := snap.Insights(InsightQuery{IDs: []InsightID{id}})
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", NotFound("insight", string(id))
	}
	return rows[0].HeadRevisionID, nil
}

func sortCrumbsNewestFirst(crumbs []Crumb) {
	sort.SliceStable(crumbs, func(i, j int) bool {
		if crumbs[i].CapturedAt.Equal(crumbs[j].CapturedAt) {
			return crumbs[i].ID > crumbs[j].ID
		}
		return crumbs[i].CapturedAt.After(crumbs[j].CapturedAt)
	})
}

// excerpt bounds a quotation to whole characters and marks that it was cut. It
// never returns a prefix that reads like the complete text.
func excerpt(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= limit {
		return s
	}
	return string([]rune(s)[:limit-1]) + "…"
}

// fit drops the least load-bearing sections until the encoded narrative fits
// the budget, and reports what it dropped. Two rules make this safe to rely on:
// the summary is never dropped, so an empty section is always accompanied by a
// sentence saying how many there were; and prime's mandatory list is never
// dropped, because an agent that cannot see a rule will break it.
func (n *Narrative) fit(budget int) ([]Notice, error) {
	if budget <= 0 {
		return nil, nil
	}
	limit := budget * bytesPerToken
	size, err := n.size()
	if err != nil {
		return nil, err
	}
	dropped := map[string]int{}
	// The running total is exact for a JSON array, so the document is re-encoded
	// only when it says the narrative fits, rather than once per dropped element.
	for size > limit {
		name, freed, err := n.dropOne()
		if err != nil {
			return nil, err
		}
		if name == "" {
			if size, err = n.size(); err != nil {
				return nil, err
			}
			if size <= limit {
				break
			}
			return []Notice{{
				Code: "budget_exceeded",
				Message: fmt.Sprintf("%s is about %d tokens, over the ~%d budget, and nothing "+
					"further may be dropped: the summary and any mandatory Insights are always reported",
					n.Mode, tokensIn(size), budget),
			}}, nil
		}
		dropped[name]++
		size -= freed
		if size <= limit {
			// The running total says it fits; prove it, because only the
			// encoder knows what the separators actually cost.
			if size, err = n.size(); err != nil {
				return nil, err
			}
		}
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(dropped))
	for name := range dropped {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%d %s", dropped[name], name))
	}
	return []Notice{{
		Code: "budget_truncated",
		Message: fmt.Sprintf("%s was trimmed to ~%d tokens; dropped %s. Raise --budget, or "+
			"pass --budget 0, for the whole answer", n.Mode, budget, strings.Join(parts, ", ")),
	}}, nil
}

// size is the encoded narrative in bytes; the budget is stated in tokens, so
// the comparison happens in bytes and only the reported figure is converted.
func (n *Narrative) size() (int, error) {
	encoded, err := json.Marshal(n)
	if err != nil {
		return 0, err
	}
	return len(encoded), nil
}

func tokensIn(bytes int) int { return (bytes + bytesPerToken - 1) / bytesPerToken }

// dropOne removes the last element of the lowest-priority non-empty section,
// names it, and reports the bytes its removal frees. The priority order is the
// contract: what a reader loses least by losing goes first. An empty name means
// nothing further may be dropped.
func (n *Narrative) dropOne() (name string, freed int, err error) {
	switch n.Mode {
	case ModeContext:
		switch {
		case len(n.RecentCrumbs) > 0:
			freed, err = dropLast(&n.RecentCrumbs)
			return "recent crumb(s)", freed, err
		case len(n.Promotions) > 0:
			freed, err = dropLast(&n.Promotions)
			return "promotion(s)", freed, err
		case len(n.OpenQuestions) > 0:
			freed, err = dropLast(&n.OpenQuestions)
			return "open question(s)", freed, err
		case len(n.Insights) > 0:
			freed, err = dropLast(&n.Insights)
			return "insight(s)", freed, err
		}
	case ModeHandoff:
		switch {
		case len(n.OpenProposals) > 0:
			freed, err = dropLast(&n.OpenProposals)
			return "open proposal(s)", freed, err
		case len(n.Workspace.References) > 0:
			freed, err = dropLast(&n.Workspace.References)
			return "workspace reference kind(s)", freed, err
		}
	case ModePrime:
		switch {
		case len(n.Cautions) > 0:
			freed, err = dropLast(&n.Cautions)
			return "caution(s)", freed, err
		case len(n.WorkingDefaults) > len(n.Mandatory):
			// Only the non-mandatory tail is droppable; mandatory Insights
			// appear in both lists and are never removed from either.
			freed, err = dropLastNonMandatory(&n.WorkingDefaults)
			return "working default(s)", freed, err
		}
	}
	return "", 0, nil
}

// dropLast removes the last element and reports what its removal takes out of
// the encoded document: the element itself, plus the comma that separated it
// from the one before.
func dropLast[T any](items *[]T) (int, error) {
	s := *items
	freed, err := elementBytes(s[len(s)-1], len(s))
	if err != nil {
		return 0, err
	}
	*items = s[:len(s)-1]
	return freed, nil
}

func dropLastNonMandatory(items *[]NarrativeInsight) (int, error) {
	s := *items
	for i := len(s) - 1; i >= 0; i-- {
		if s[i].Standing == StandingMandatory {
			continue
		}
		freed, err := elementBytes(s[i], len(s))
		if err != nil {
			return 0, err
		}
		*items = append(s[:i:i], s[i+1:]...)
		return freed, nil
	}
	return 0, nil
}

func elementBytes(v any, length int) (int, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return 0, err
	}
	if length > 1 {
		return len(encoded) + 1, nil // the separating comma goes with it
	}
	return len(encoded), nil
}
