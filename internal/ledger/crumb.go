package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Crumb capture, review, and pruning.
//
// Three rules hold across this file and are worth reading once:
//
//   - Redaction runs before any write that accepts free text, inside the
//     ledger. A command body never redacts, so there is no write path that can
//     forget to. Dolt keeps committed history, so a secret that gets past this
//     point is permanent — which is why an unresolvable finding aborts the
//     whole operation instead of degrading it.
//   - Review appends. It moves the materialised review_state and records the
//     transition; it never rewrites confidence, provenance, or content.
//   - Prune is the only delete in the system. It is allowed for candidates
//     only, it reports blockage per id, and it removes rows from the head —
//     committed history retains them, so prune is retention, not erasure.
const (
	// maxCrumbChars mirrors ck_crumbs_size. It is the database's statement that
	// a Crumb is a fragment rather than a transcript, checked here too so the
	// caller gets a typed error instead of a constraint violation.
	maxCrumbChars = 4096

	// The transcript-shape signals for automatic capture. Redaction removes
	// secrets and does nothing about the product boundary that says transcripts
	// are not stored, so automatic input is refused on shape before it is
	// redacted. Manual content is human-authored and bounded by the size cap
	// alone.
	maxAutoCaptureLines  = 12
	minTranscriptSpeaker = 3
)

// speakerTurn matches the line prefixes a session transcript repeats. Three or
// more of them is a transcript no matter what the line count says.
var speakerTurn = regexp.MustCompile(`(?im)^[\s>]{0,4}(?:\*\*|##\s*)?(user|human|assistant|system|claude|agent|ai|tool)(?:\*\*)?\s*[:>]`)

// CaptureCrumb records one atomic fragment. Automatic marks input harvested
// from session material rather than authored by the caller, which is the only
// difference that changes what is accepted.
type CaptureCrumb struct {
	Content       string
	Confidence    float64
	References    []RefSpec
	Automatic     bool
	HarvestID     HarvestID
	PolicyVersion string
}

// CaptureResult carries the redaction outcome alongside the Crumb. The findings
// are what the CLI turns into warnings[]: a caller has to be able to see that
// its text was rewritten before it was stored, and the Crumb alone cannot say
// so. Deduplicated reports an automatic re-capture of text already held for this
// session — the answer, not an error.
type CaptureResult struct {
	Crumb        Crumb     `json:"crumb"`
	References   []RefSpec `json:"references,omitempty"`
	Findings     []Finding `json:"-"`
	Deduplicated bool      `json:"-"`
}

// RefSpec is one `kind:locator[@relation]` argument. The locator is opaque:
// core never parses it, and the only thing checked here is that it is present
// and carries no secret.
type RefSpec struct {
	Kind      string   `json:"kind"`
	Locator   string   `json:"locator"`
	Workspace string   `json:"workspace,omitempty"`
	Relation  Relation `json:"relation"`
}

var relations = []Relation{RelationSource, RelationEvidence, RelationSubject, RelationSpawnedWork}

// ParseRefSpec reads `kind:locator[@relation]`.
//
// The kind is everything before the first colon, because a locator routinely
// contains one (`https://…`). The relation is read from the last `@` and only
// when the suffix is a known relation, because a locator routinely contains one
// of those too (`pkg@1.2.3`) — an unrecognised suffix stays part of the locator
// rather than becoming a silent parse failure.
func ParseRefSpec(arg string, fallback Relation) (RefSpec, error) {
	kind, rest, ok := strings.Cut(strings.TrimSpace(arg), ":")
	if !ok {
		return RefSpec{}, Fail(ErrInvalidInput, "invalid_reference",
			"%q is not a reference; expected kind:locator[@relation]", arg)
	}
	spec := RefSpec{Kind: kind, Locator: rest, Relation: fallback}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		if rel := Relation(rest[at+1:]); slices.Contains(relations, rel) {
			spec.Locator, spec.Relation = rest[:at], rel
		}
	}
	return spec, spec.Validate()
}

func (s RefSpec) Validate() error {
	switch {
	case s.Kind == "":
		return Fail(ErrInvalidInput, "invalid_reference", "a reference needs an adapter kind")
	case strings.ContainsAny(s.Kind, ": \t\n"), len(s.Kind) > 64:
		return Fail(ErrInvalidInput, "invalid_reference",
			"reference kind %q must be a namespace token of at most 64 characters", s.Kind)
	case strings.TrimSpace(s.Locator) == "":
		return Fail(ErrInvalidInput, "invalid_reference", "a reference needs a locator")
	case len(s.Locator) > 1024:
		return Fail(ErrInvalidInput, "invalid_reference", "reference locator is longer than 1024 characters")
	case len(s.Workspace) > 255:
		return Fail(ErrInvalidInput, "invalid_reference", "reference workspace is longer than 255 characters")
	case !slices.Contains(relations, s.Relation):
		return Fail(ErrInvalidInput, "invalid_relation",
			"%q is not a reference relation; expected one of %s", s.Relation, joinRelations())
	}
	return nil
}

func joinRelations() string {
	parts := make([]string, len(relations))
	for i, r := range relations {
		parts[i] = string(r)
	}
	return strings.Join(parts, ", ")
}

// CaptureCrumb redacts, then writes. The order is the point: the redactor runs
// before the transaction opens, so a finding it cannot resolve aborts with
// nothing persisted rather than with a rolled-back write that still touched the
// journal.
func (l *Ledger) CaptureCrumb(ctx context.Context, c CaptureCrumb) (CaptureResult, error) {
	if err := l.actor.Validate(); err != nil {
		return CaptureResult{}, err
	}
	if err := ValidateConfidence(c.Confidence); err != nil {
		return CaptureResult{}, err
	}
	content := strings.TrimSpace(c.Content)
	if content == "" {
		return CaptureResult{}, Fail(ErrInvalidInput, "invalid_content", "a Crumb needs content")
	}
	if c.Automatic {
		if err := rejectTranscriptShape(content); err != nil {
			return CaptureResult{}, err
		}
	}
	if n := utf8.RuneCountInString(content); n > maxCrumbChars {
		return CaptureResult{}, Fail(ErrInvalidInput, "invalid_content_size",
			"a Crumb is a fragment: %d characters exceeds the %d-character limit", n, maxCrumbChars)
	}

	clean, findings, err := l.redactField("content", content)
	if err != nil {
		return CaptureResult{}, err
	}
	if n := utf8.RuneCountInString(clean); n > maxCrumbChars {
		return CaptureResult{}, Fail(ErrInvalidInput, "invalid_content_size",
			"the redacted Crumb is %d characters, which exceeds the %d-character limit", n, maxCrumbChars)
	}

	for _, ref := range c.References {
		if err := ref.Validate(); err != nil {
			return CaptureResult{}, err
		}
		// Identity columns reject rather than redact: rewriting a locator would
		// silently change which record it names, producing a Reference that
		// resolves to nothing while looking valid.
		if err := l.rejectSecrets("reference locator", ref.Locator); err != nil {
			return CaptureResult{}, err
		}
	}

	crumb := Crumb{
		ID:               NewCrumbID(),
		Content:          clean,
		ContentHash:      hashContent(clean),
		ReviewState:      StateCandidate,
		Confidence:       c.Confidence,
		CapturedAt:       l.clock(),
		HarvestID:        c.HarvestID,
		PolicyVersion:    c.PolicyVersion,
		RedactionVersion: l.redactor.Version(),
		Provenance:       l.actor,
	}
	if crumb.HarvestID != "" && crumb.PolicyVersion == "" {
		return CaptureResult{}, Fail(ErrIntegrity, "integrity_harvest_policy",
			"a Crumb captured by a harvest must carry the policy version that judged it")
	}
	l.assertRedacted("crumbs.content", crumb.Content)

	result := CaptureResult{References: c.References, Findings: findings}
	err = l.store.Write(ctx, func(tx Tx) error {
		// uq_crumbs_hash_session makes repeated automatic capture within one
		// session a duplicate. Answering with the Crumb already held is what
		// that key is for; letting the insert fail would turn a dedupe into an
		// error the caller has to interpret.
		if l.actor.SessionID != "" {
			existing, err := tx.Crumbs(CrumbQuery{SessionID: l.actor.SessionID})
			if err != nil {
				return err
			}
			for _, e := range existing {
				if e.ContentHash == crumb.ContentHash {
					result.Crumb, result.Deduplicated = e, true
					return nil
				}
			}
		}
		if err := tx.InsertCrumb(crumb); err != nil {
			return err
		}
		record := RecordRef{Kind: KindCrumb, ID: string(crumb.ID)}
		for _, ref := range c.References {
			id, err := tx.UpsertReference(Reference{
				ID: NewReferenceID(), Kind: ref.Kind, Locator: ref.Locator,
				Workspace: ref.Workspace, CreatedAt: crumb.CapturedAt,
			})
			if err != nil {
				return err
			}
			if err := tx.LinkReference(record, id, ref.Relation); err != nil {
				return err
			}
		}
		result.Crumb = crumb
		return nil
	})
	if err != nil {
		return CaptureResult{}, err
	}
	return result, nil
}

// ReviewCrumb is a batch because `bdc crumb review <id>...` is: reviewing five
// Crumbs under one rationale is one decision, and one transaction is the only
// way a partial answer is impossible.
type ReviewCrumb struct {
	IDs       []CrumbID
	ToState   ReviewState
	Rationale string
}

type ReviewResult struct {
	Crumbs   []Crumb            `json:"crumbs"`
	Events   []CrumbReviewEvent `json:"events"`
	Findings []Finding          `json:"-"`
}

// ReviewCrumb appends a transition per Crumb and moves the materialised state.
// It never touches content, confidence, or capture provenance: those are what
// the Crumb *was*, and a review is a later opinion about it.
func (l *Ledger) ReviewCrumb(ctx context.Context, c ReviewCrumb) (ReviewResult, error) {
	if err := l.actor.Validate(); err != nil {
		return ReviewResult{}, err
	}
	if len(c.IDs) == 0 {
		return ReviewResult{}, Fail(ErrInvalidInput, "invalid_id", "review needs at least one Crumb id")
	}
	// Reviewing back to candidate would erase a decision rather than record one,
	// which is the single thing an append-only history must not permit.
	if c.ToState != StateAccepted && c.ToState != StateRejected {
		return ReviewResult{}, Fail(ErrInvalidInput, "invalid_review_state",
			"a review moves a Crumb to accepted or rejected, got %q", c.ToState)
	}
	if strings.TrimSpace(c.Rationale) == "" {
		return ReviewResult{}, Fail(ErrInvalidInput, "invalid_rationale",
			"a review needs a rationale; the decision is the record")
	}
	rationale, findings, err := l.redactField("rationale", strings.TrimSpace(c.Rationale))
	if err != nil {
		return ReviewResult{}, err
	}
	l.assertRedacted("crumb_review_events.rationale", rationale)

	at := l.clock()
	out := ReviewResult{Findings: findings}
	err = l.store.Write(ctx, func(tx Tx) error {
		out.Crumbs, out.Events = nil, nil
		found, err := tx.Crumbs(CrumbQuery{IDs: c.IDs})
		if err != nil {
			return err
		}
		byID := make(map[CrumbID]Crumb, len(found))
		for _, crumb := range found {
			byID[crumb.ID] = crumb
		}
		for _, id := range c.IDs {
			crumb, ok := byID[id]
			if !ok {
				return NotFound("crumb", string(id))
			}
			event := CrumbReviewEvent{
				ID: NewReviewEventID(), CrumbID: id,
				FromState: crumb.ReviewState, ToState: c.ToState,
				Rationale: rationale, OccurredAt: at, Provenance: l.actor,
			}
			if err := tx.AppendCrumbReview(event); err != nil {
				return err
			}
			crumb.ReviewState = c.ToState
			out.Crumbs = append(out.Crumbs, crumb)
			out.Events = append(out.Events, event)
		}
		return nil
	})
	if err != nil {
		return ReviewResult{}, err
	}
	return out, nil
}

// PruneCrumbs is retention, not erasure. Committed Dolt history retains a pruned
// Crumb, so this removes rows from the head and nothing more; a secret that
// survived redaction cannot be pruned away.
type PruneCrumbs struct {
	IDs       []CrumbID
	Before    time.Time
	State     ReviewState // candidate, or empty for the same thing
	Confirmed bool
}

type PruneResult struct {
	Pruned    int          `json:"pruned"`
	PrunedIDs []CrumbID    `json:"pruned_ids"`
	Blocked   []PruneBlock `json:"blocked"`
}

// PruneBlock is one Crumb the prune refused, with the reason a caller can act
// on. Blockage is computed before the delete: a foreign-key violation aborts the
// whole transaction and loses the per-id answer, so fk_ic_crumb is the backstop,
// never the check.
type PruneBlock struct {
	CrumbID   CrumbID      `json:"crumb_id"`
	Code      string       `json:"code"`
	Reason    string       `json:"reason"`
	Revisions []RevisionID `json:"revisions,omitempty"`
}

func (l *Ledger) PruneCrumbs(ctx context.Context, c PruneCrumbs) (PruneResult, error) {
	if !c.Confirmed {
		return PruneResult{}, Fail(ErrInvalidInput, "invalid_confirmation",
			"prune deletes rows from the ledger head; pass --yes to confirm")
	}
	if c.State != "" && c.State != StateCandidate {
		return PruneResult{}, Fail(ErrInvalidInput, "invalid_review_state",
			"prune is allowed for candidate Crumbs only, got %q", c.State)
	}
	if len(c.IDs) == 0 && c.Before.IsZero() {
		return PruneResult{}, Fail(ErrInvalidInput, "invalid_selection",
			"prune needs --id or --before; it will not delete every candidate by default")
	}

	var out PruneResult
	err := l.store.Write(ctx, func(tx Tx) error {
		// Rebuilt per attempt: Write may roll back and the caller must never see
		// a result assembled from a transaction that did not commit.
		out = PruneResult{PrunedIDs: []CrumbID{}, Blocked: []PruneBlock{}}
		q := CrumbQuery{IDs: c.IDs, Before: c.Before}
		if len(c.IDs) == 0 {
			q.States = []ReviewState{StateCandidate}
		}
		candidates, err := tx.Crumbs(q)
		if err != nil {
			return err
		}
		if len(c.IDs) > 0 {
			seen := make(map[CrumbID]struct{}, len(candidates))
			for _, crumb := range candidates {
				seen[crumb.ID] = struct{}{}
			}
			for _, id := range c.IDs {
				if _, ok := seen[id]; !ok {
					return NotFound("crumb", string(id))
				}
			}
		}

		var deletable []CrumbID
		for _, crumb := range candidates {
			if crumb.ReviewState != StateCandidate {
				out.Blocked = append(out.Blocked, PruneBlock{
					CrumbID: crumb.ID, Code: "not_candidate",
					Reason: fmt.Sprintf("the Crumb is %s; prune is allowed for candidates only", crumb.ReviewState),
				})
				continue
			}
			links, err := tx.CrumbLinks(crumb.ID)
			if err != nil {
				return err
			}
			if len(links.Revisions) > 0 {
				block := PruneBlock{
					CrumbID: crumb.ID, Code: "supports_insight",
					Reason: fmt.Sprintf("the Crumb supports %d Insight revision(s); evidence lineage cannot be pruned away",
						len(links.Revisions)),
				}
				for _, rev := range links.Revisions {
					block.Revisions = append(block.Revisions, rev.ID)
				}
				out.Blocked = append(out.Blocked, block)
				continue
			}
			deletable = append(deletable, crumb.ID)
		}

		n, err := tx.DeleteCrumbs(deletable)
		if err != nil {
			return err
		}
		out.Pruned = n
		if deletable != nil {
			out.PrunedIDs = deletable
		}
		return nil
	})
	if err != nil {
		return PruneResult{}, err
	}
	return out, nil
}

// CrumbPage is one page of a listing plus the total the filter matched, so a
// caller can tell "these are all of them" from "these are the first twenty".
type CrumbPage struct {
	Crumbs []Crumb `json:"crumbs"`
	Total  int     `json:"total"`
}

// Crumbs applies Limit and Offset here rather than in SQL because Total must
// count the whole filtered set, and a LIMIT query cannot report what it did not
// return. A Crumb is capped at 4 KiB by ck_crumbs_size, so the full set is
// bounded by construction.
func (l *Ledger) Crumbs(ctx context.Context, q CrumbQuery) (CrumbPage, error) {
	limit, offset := q.Limit, q.Offset
	q.Limit, q.Offset = 0, 0
	var page CrumbPage
	err := l.store.Read(ctx, func(snap Snapshot) error {
		all, err := snap.Crumbs(q)
		if err != nil {
			return err
		}
		page.Total = len(all)
		if offset > len(all) {
			offset = len(all)
		}
		all = all[offset:]
		if limit > 0 && limit < len(all) {
			all = all[:limit]
		}
		page.Crumbs = all
		return nil
	})
	if err != nil {
		return CrumbPage{}, err
	}
	if page.Crumbs == nil {
		page.Crumbs = []Crumb{}
	}
	return page, nil
}

// CrumbDetail is everything one Crumb participates in. Harvests and Insights are
// both present because a Crumb is never consumed: it stays available to further
// Harvests and Insights, and the only way to see that is to list them.
type CrumbDetail struct {
	Crumb        Crumb             `json:"crumb"`
	ReviewEvents []EventRow        `json:"review_events"`
	References   []CrumbReference  `json:"references"`
	Harvests     []HarvestLinkRow  `json:"harvests"`
	Insights     []InsightRevision `json:"insights"`
}

// CrumbReference is a Reference as one record sees it: the Reference plus the
// relation under which it is attached.
type CrumbReference struct {
	Reference
	Relation Relation `json:"relation"`
}

func (l *Ledger) Crumb(ctx context.Context, id CrumbID) (CrumbDetail, error) {
	if _, err := ParseID(PrefixCrumb, string(id)); err != nil {
		return CrumbDetail{}, err
	}
	var detail CrumbDetail
	err := l.store.Read(ctx, func(snap Snapshot) error {
		found, err := snap.Crumbs(CrumbQuery{IDs: []CrumbID{id}})
		if err != nil {
			return err
		}
		if len(found) == 0 {
			return NotFound("crumb", string(id))
		}
		detail.Crumb = found[0]

		record := RecordRef{Kind: KindCrumb, ID: string(id)}
		if detail.ReviewEvents, err = snap.Events(EventQuery{Targets: []RecordRef{record}}); err != nil {
			return err
		}
		if detail.References, err = readReferences(snap, record); err != nil {
			return err
		}
		links, err := snap.CrumbLinks(id)
		if err != nil {
			return err
		}
		detail.Harvests, detail.Insights = links.Harvests, links.Revisions
		return nil
	})
	if err != nil {
		return CrumbDetail{}, err
	}
	return withEmptySlices(detail), nil
}

// readReferences pairs each link with the Reference it names. The two reads are
// joined here rather than in SQL because the link carries the relation and the
// Reference carries identity, and a caller needs both to say anything useful.
func readReferences(snap Snapshot, record RecordRef) ([]CrumbReference, error) {
	links, err := snap.ReferenceLinks(record)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, nil
	}
	ids := make([]ReferenceID, 0, len(links))
	for _, l := range links {
		ids = append(ids, l.ReferenceID)
	}
	refs, err := snap.References(ReferenceQuery{IDs: ids})
	if err != nil {
		return nil, err
	}
	byID := make(map[ReferenceID]Reference, len(refs))
	for _, r := range refs {
		byID[r.ID] = r
	}
	out := make([]CrumbReference, 0, len(links))
	for _, l := range links {
		ref, ok := byID[l.ReferenceID]
		if !ok {
			// ref_links.reference_id carries fk_rl_ref, so a missing Reference
			// means the row was read outside its foreign key. Report it rather
			// than rendering a link to nothing.
			return nil, Fail(ErrIntegrity, "integrity_missing_reference",
				"ref_link on %s names reference %s, which does not exist", record, l.ReferenceID)
		}
		out = append(out, CrumbReference{Reference: ref, Relation: l.Relation})
	}
	return out, nil
}

func withEmptySlices(d CrumbDetail) CrumbDetail {
	if d.ReviewEvents == nil {
		d.ReviewEvents = []EventRow{}
	}
	if d.References == nil {
		d.References = []CrumbReference{}
	}
	if d.Harvests == nil {
		d.Harvests = []HarvestLinkRow{}
	}
	if d.Insights == nil {
		d.Insights = []InsightRevision{}
	}
	return d
}

// rejectTranscriptShape refuses automatic input that is a session transcript
// rather than a fragment. Redaction removes secrets and does nothing about the
// product boundary that says transcripts are not stored, so the shape check runs
// first and independently.
func rejectTranscriptShape(content string) error {
	if turns := len(speakerTurn.FindAllString(content, -1)); turns >= minTranscriptSpeaker {
		return Fail(ErrInvalidInput, "invalid_transcript_shape",
			"automatic capture refuses transcript-shaped input: %d speaker turns; capture the conclusion, not the transcript",
			turns)
	}
	if lines := strings.Count(content, "\n") + 1; lines > maxAutoCaptureLines {
		return Fail(ErrInvalidInput, "invalid_transcript_shape",
			"automatic capture refuses transcript-shaped input: %d lines exceeds the %d-line limit for a fragment",
			lines, maxAutoCaptureLines)
	}
	return nil
}

// redactField is the Redact treatment from the design's column table: findings
// are replaced, the clean text is written, and the redaction version is recorded
// on the record.
func (l *Ledger) redactField(field, text string) (string, []Finding, error) {
	clean, findings, err := l.redactor.Redact(text)
	if err != nil {
		return "", nil, FailWith(ErrRedaction, "redaction_failed", err,
			"the %s could not be redacted, so nothing was written", field).
			WithDetails(map[string]any{"field": field})
	}
	return clean, findings, nil
}

// rejectSecrets is the Reject treatment: identity columns are never rewritten,
// because redacting a locator would silently change which record it names. The
// error names the field and the rule and never the value.
func (l *Ledger) rejectSecrets(field, value string) error {
	_, findings, err := l.redactor.Redact(value)
	if err != nil {
		return err
	}
	if len(findings) == 0 {
		return nil
	}
	return Fail(ErrRedaction, "redaction_failed",
		"the %s matches redaction rule %q; an identity value is never rewritten, so nothing was written",
		field, findings[0].Rule).WithDetails(map[string]any{"field": field, "rule": findings[0].Rule})
}

// assertRedacted is a live assertion, not a test helper: the value about to be
// persisted must be the redactor's own output. Redaction is idempotent, so a
// finding here means a write path assembled text after redaction ran, and Dolt's
// committed history would keep the result forever. Crash, don't trash — and
// never name the value.
func (l *Ledger) assertRedacted(column, value string) {
	_, findings, err := l.redactor.Redact(value)
	if err != nil || len(findings) > 0 {
		panic(fmt.Sprintf("beadcrumbs: refusing to write %s: the value did not come from the redactor", column))
	}
}

func hashContent(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
