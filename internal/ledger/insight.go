package ledger

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Insight revision lifecycle and reads.
//
// An Insight is identity plus a materialised head; its revisions are the truth.
// Two rules hold across this file:
//
//   - A revision is immutable. Revising appends a row; it never rewrites the
//     earlier one, so the reasoning that produced a superseded conclusion stays
//     readable. Revision 2 and later require both a parent and a rationale, and
//     ck_rev_lineage is the live assertion behind that.
//   - Evidence accumulates. A revision inherits its parent's supporting Crumbs
//     and may add more; it cannot drop any. Derivation links are RESTRICT in the
//     schema precisely so evidence lineage cannot be pruned away, and a revision
//     that could silently shed evidence would route around that.

// ReviseInsight appends the next revision. Title, Class, and Confidence carry
// forward from the head when unset — a revision that restates unchanged
// metadata is a revision that can get it wrong. Content and Rationale are
// always required: a revision with neither new content nor a stated reason is
// not a revision.
type ReviseInsight struct {
	InsightID  InsightID
	Title      string
	Content    string
	Class      string
	Confidence *float64
	Rationale  string
	Crumbs     []CrumbID
}

// RevisionResult is the `{insight, revision}` the CLI contract promises. The
// Insight is read back rather than assembled: its head has just moved, and the
// stored row is the only thing that can say so.
type RevisionResult struct {
	Insight  Insight         `json:"insight"`
	Revision InsightRevision `json:"revision"`
	Findings []Finding       `json:"-"`
}

func (l *Ledger) ReviseInsight(ctx context.Context, c ReviseInsight) (RevisionResult, error) {
	if err := l.actor.Validate(); err != nil {
		return RevisionResult{}, err
	}
	if _, err := ParseID(PrefixInsight, string(c.InsightID)); err != nil {
		return RevisionResult{}, err
	}
	if strings.TrimSpace(c.Rationale) == "" {
		return RevisionResult{}, Fail(ErrInvalidInput, "invalid_rationale",
			"a revision needs a rationale; the earlier revision stays readable and has to be explainable")
	}
	if c.Class != "" {
		if err := ValidateClass(c.Class); err != nil {
			return RevisionResult{}, err
		}
	}
	if c.Confidence != nil {
		if err := ValidateConfidence(*c.Confidence); err != nil {
			return RevisionResult{}, err
		}
	}

	title, findings, err := l.redactField("insight title", strings.TrimSpace(c.Title))
	if err != nil {
		return RevisionResult{}, err
	}
	content, contentFindings, err := l.redactField("insight content", strings.TrimSpace(c.Content))
	if err != nil {
		return RevisionResult{}, err
	}
	findings = append(findings, contentFindings...)
	rationale, rationaleFindings, err := l.redactField("rationale", strings.TrimSpace(c.Rationale))
	if err != nil {
		return RevisionResult{}, err
	}
	findings = append(findings, rationaleFindings...)
	l.assertRedacted("insight_revisions.title", title)
	l.assertRedacted("insight_revisions.content", content)
	l.assertRedacted("insight_revisions.rationale", rationale)

	out := RevisionResult{Findings: findings}
	err = l.store.Write(ctx, func(tx Tx) error {
		revisions, err := tx.Revisions(c.InsightID)
		if err != nil {
			return err
		}
		if len(revisions) == 0 {
			return NotFound("insight", string(c.InsightID))
		}
		head := revisions[len(revisions)-1]

		next := InsightRevision{
			ID:               NewRevisionID(),
			InsightID:        c.InsightID,
			Revision:         head.Revision + 1,
			Title:            head.Title,
			Content:          content,
			Class:            head.Class,
			Confidence:       head.Confidence,
			Rationale:        rationale,
			ParentRevisionID: head.ID,
			CreatedAt:        l.clock(),
			Provenance:       l.actor,
		}
		if title != "" {
			next.Title = title
		}
		if c.Class != "" {
			next.Class = c.Class
		}
		if c.Confidence != nil {
			next.Confidence = *c.Confidence
		}
		if err := validateRevisionText(next.Title, next.Content); err != nil {
			return err
		}
		next.ContentHash = hashContent(next.Content)

		crumbs, err := l.inheritCrumbs(tx, head.ID, c.Crumbs)
		if err != nil {
			return err
		}
		if err := tx.InsertRevision(next, crumbs); err != nil {
			return err
		}
		if err := tx.SetInsightHead(c.InsightID, next.Revision); err != nil {
			return err
		}

		insight, err := readInsight(tx, c.InsightID)
		if err != nil {
			return err
		}
		out.Insight, out.Revision = insight, next
		return nil
	})
	if err != nil {
		return RevisionResult{}, err
	}
	return out, nil
}

// inheritCrumbs is the "preserves prior evidence" rule as code: the parent's
// supporting set, plus whatever the caller added, and nothing removed.
func (l *Ledger) inheritCrumbs(snap Snapshot, parent RevisionID, added []CrumbID) ([]CrumbID, error) {
	inherited, err := snap.Crumbs(CrumbQuery{RevisionIDs: []RevisionID{parent}})
	if err != nil {
		return nil, err
	}
	out := make([]CrumbID, 0, len(inherited)+len(added))
	for _, crumb := range inherited {
		out = append(out, crumb.ID)
	}
	if len(added) > 0 {
		named := dedupeCrumbIDs(added)
		found, err := snap.Crumbs(CrumbQuery{IDs: named})
		if err != nil {
			return nil, err
		}
		present := make(map[CrumbID]struct{}, len(found))
		for _, crumb := range found {
			present[crumb.ID] = struct{}{}
		}
		for _, id := range named {
			if _, ok := present[id]; !ok {
				return nil, NotFound("crumb", string(id))
			}
		}
		out = append(out, named...)
	}
	return dedupeCrumbIDs(out), nil
}

// InsightView is an Insight as a listing shows it: the head revision's title,
// class, and confidence, plus the two independent judgement axes read at their
// latest event. Absence means unreviewed and advisory, which is the same
// reading every other part of the product gives an empty history.
type InsightView struct {
	InsightRow
	Verdict   Verdict        `json:"verdict"`
	Authority AuthorityLevel `json:"authority"`
}

// InsightPage is one page plus the total the filter matched, so a caller can
// tell "these are all of them" from "these are the first twenty".
type InsightPage struct {
	Insights []InsightView `json:"insights"`
	Total    int           `json:"total"`
}

func (l *Ledger) Insights(ctx context.Context, q InsightQuery) (InsightPage, error) {
	for _, class := range q.Classes {
		if err := ValidateClass(class); err != nil {
			return InsightPage{}, err
		}
	}
	// Limit and Offset are applied here rather than in SQL because Total must
	// count the whole filtered set, and a LIMIT query cannot report what it did
	// not return.
	limit, offset := q.Limit, q.Offset
	q.Limit, q.Offset = 0, 0

	var page InsightPage
	err := l.store.Read(ctx, func(snap Snapshot) error {
		rows, err := snap.Insights(q)
		if err != nil {
			return err
		}
		page.Total = len(rows)
		if offset > len(rows) {
			offset = len(rows)
		}
		rows = rows[offset:]
		if limit > 0 && limit < len(rows) {
			rows = rows[:limit]
		}

		targets := make([]RecordRef, 0, len(rows))
		for _, row := range rows {
			targets = append(targets, RecordRef{Kind: KindRevision, ID: string(row.HeadRevisionID)})
		}
		verdicts, authorities, err := latestJudgements(snap, targets)
		if err != nil {
			return err
		}
		page.Insights = make([]InsightView, 0, len(rows))
		for _, row := range rows {
			page.Insights = append(page.Insights, InsightView{
				InsightRow: row,
				Verdict:    verdicts[string(row.HeadRevisionID)],
				Authority:  authorities[string(row.HeadRevisionID)],
			})
		}
		return nil
	})
	if err != nil {
		return InsightPage{}, err
	}
	if page.Insights == nil {
		page.Insights = []InsightView{}
	}
	return page, nil
}

// latestJudgements reads the current verdict and authority for each target.
// Events arrive oldest first, so the last row of each kind wins; a target with
// no history takes the meaning of absence.
func latestJudgements(snap Snapshot, targets []RecordRef) (map[string]Verdict, map[string]AuthorityLevel, error) {
	verdicts := map[string]Verdict{}
	authorities := map[string]AuthorityLevel{}
	for _, t := range targets {
		verdicts[t.ID] = VerdictUnreviewed
		authorities[t.ID] = AuthorityAdvisory
	}
	if len(targets) == 0 {
		return verdicts, authorities, nil
	}
	events, err := snap.Events(EventQuery{Targets: targets})
	if err != nil {
		return nil, nil, err
	}
	for _, e := range events {
		switch e.Kind {
		case EventValidation:
			verdicts[e.Target.ID] = Verdict(e.Summary)
		case EventAuthority:
			authorities[e.Target.ID] = AuthorityLevel(e.Summary)
		}
	}
	return verdicts, authorities, nil
}

// InsightOptions selects which revision `bdc insight show` reports and whether
// it renders the derivation chain. Revision 0 means the head.
type InsightOptions struct {
	Revision int
	Lineage  bool
}

// InsightDetail is the `{insight, revision, revisions[], crumbs[], references[],
// validations[], authorities[], proposals[]}` shape from the CLI contract.
// Lineage is present only when it was asked for.
type InsightDetail struct {
	Insight     Insight           `json:"insight"`
	Revision    InsightRevision   `json:"revision"`
	Revisions   []InsightRevision `json:"revisions"`
	Crumbs      []Crumb           `json:"crumbs"`
	References  []CrumbReference  `json:"references"`
	Validations []EventRow        `json:"validations"`
	Authorities []EventRow        `json:"authorities"`
	Proposals   []Proposal        `json:"proposals"`
	Lineage     []LineageStep     `json:"lineage,omitempty"`
}

// LineageStep is one link in the derivation chain: which revision, from which
// parent, out of which Harvest, on which evidence. It is what makes "where did
// this conclusion come from" answerable without reading every revision's prose.
type LineageStep struct {
	Revision   int        `json:"revision"`
	RevisionID RevisionID `json:"revision_id"`
	ParentID   RevisionID `json:"parent_revision_id,omitempty"`
	HarvestID  HarvestID  `json:"harvest_id,omitempty"`
	Rationale  string     `json:"rationale,omitempty"`
	Crumbs     []CrumbID  `json:"crumbs"`
	CreatedAt  time.Time  `json:"created_at"`
	Provenance
}

func (l *Ledger) Insight(ctx context.Context, id InsightID, o InsightOptions) (InsightDetail, error) {
	if _, err := ParseID(PrefixInsight, string(id)); err != nil {
		return InsightDetail{}, err
	}
	if o.Revision < 0 {
		return InsightDetail{}, Fail(ErrInvalidInput, "invalid_revision",
			"a revision number is 1 or greater, got %d", o.Revision)
	}

	var detail InsightDetail
	err := l.store.Read(ctx, func(snap Snapshot) error {
		insight, err := readInsight(snap, id)
		if err != nil {
			return err
		}
		detail.Insight = insight

		detail.Revisions, err = snap.Revisions(id)
		if err != nil {
			return err
		}
		if len(detail.Revisions) == 0 {
			// insights and insight_revisions are written by one Tx method
			// precisely so this cannot happen.
			return Fail(ErrIntegrity, "integrity_insight_without_revision",
				"insight %s has no revisions", id)
		}
		detail.Revision = detail.Revisions[len(detail.Revisions)-1]
		if o.Revision > 0 {
			found := false
			for _, rev := range detail.Revisions {
				if rev.Revision == o.Revision {
					detail.Revision, found = rev, true
				}
			}
			if !found {
				return NotFound("revision", string(id)+"@"+strconv.Itoa(o.Revision))
			}
		}

		record := RecordRef{Kind: KindRevision, ID: string(detail.Revision.ID)}
		if detail.Crumbs, err = snap.Crumbs(CrumbQuery{RevisionIDs: []RevisionID{detail.Revision.ID}}); err != nil {
			return err
		}
		if detail.References, err = readReferences(snap, record); err != nil {
			return err
		}
		events, err := snap.Events(EventQuery{Targets: []RecordRef{record}})
		if err != nil {
			return err
		}
		for _, e := range events {
			switch e.Kind {
			case EventValidation:
				detail.Validations = append(detail.Validations, e)
			case EventAuthority:
				detail.Authorities = append(detail.Authorities, e)
			}
		}
		if detail.Proposals, err = snap.Proposals(PromotionQuery{InsightID: id}); err != nil {
			return err
		}
		if o.Lineage {
			detail.Lineage, err = readLineage(snap, detail.Revisions)
		}
		return err
	})
	if err != nil {
		return InsightDetail{}, err
	}
	return withEmptyInsightSlices(detail), nil
}

// readInsight resolves one Insight by id. It goes through the Insights listing
// because that is the only read the storage port offers, and the join to the
// head revision is what makes an Insight legible at all.
func readInsight(snap Snapshot, id InsightID) (Insight, error) {
	rows, err := snap.Insights(InsightQuery{IDs: []InsightID{id}})
	if err != nil {
		return Insight{}, err
	}
	if len(rows) == 0 {
		return Insight{}, NotFound("insight", string(id))
	}
	return rows[0].Insight, nil
}

// readLineage walks each revision's supporting Crumbs. It is one query per
// revision rather than one for all of them because the answer is per revision:
// a Crumb feeding three revisions would otherwise be indistinguishable from
// three Crumbs feeding one.
func readLineage(snap Snapshot, revisions []InsightRevision) ([]LineageStep, error) {
	steps := make([]LineageStep, 0, len(revisions))
	for _, rev := range revisions {
		crumbs, err := snap.Crumbs(CrumbQuery{RevisionIDs: []RevisionID{rev.ID}})
		if err != nil {
			return nil, err
		}
		step := LineageStep{
			Revision: rev.Revision, RevisionID: rev.ID, ParentID: rev.ParentRevisionID,
			HarvestID: rev.HarvestID, Rationale: rev.Rationale,
			Crumbs: []CrumbID{}, CreatedAt: rev.CreatedAt, Provenance: rev.Provenance,
		}
		for _, crumb := range crumbs {
			step.Crumbs = append(step.Crumbs, crumb.ID)
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func withEmptyInsightSlices(d InsightDetail) InsightDetail {
	if d.Crumbs == nil {
		d.Crumbs = []Crumb{}
	}
	if d.References == nil {
		d.References = []CrumbReference{}
	}
	if d.Validations == nil {
		d.Validations = []EventRow{}
	}
	if d.Authorities == nil {
		d.Authorities = []EventRow{}
	}
	if d.Proposals == nil {
		d.Proposals = []Proposal{}
	}
	return d
}
