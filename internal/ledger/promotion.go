package ledger

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Promotion Proposals, attempts, and receipts.
//
// A proposal is a destination-neutral request to turn one Insight revision into
// one durable external record. Four rules hold across this file:
//
//   - Idempotency is a database property. content_hash covers every field that
//     changes what would be written or what authority is needed to write it,
//     and uq_pp_hash is the unique index that answers created=false. Re-running
//     `bdc promote propose` with identical arguments is the same proposal, not
//     a second one, and no code convention is load-bearing for that.
//   - Proposals are immutable. An idempotent hit whose confidence or evidence
//     differs from the stored proposal reports the divergence and keeps the
//     stored one; it never rewrites it.
//   - Attempts are independent. Each destination is its own proposal and each
//     try is its own `promotions` row, so a destination outage marks one attempt
//     failed and has no lifecycle effect on any other proposal or attempt.
//   - A receipt proves what happened, not what is true. Its locator may differ
//     from the proposed one — ADR numbering is decided by the repository — and
//     its anchor is only as strong as the destination's declared capabilities.
//     Without stable-anchor a receipt proves an attempt happened and nothing
//     more, and the output says so.
//
// Nothing here performs an external write. v1 ships no destination adapters:
// the flow is propose, an actor writes it, record the receipt.

// maxProposalChars mirrors ck_pp_size. Rendered content is a document rather
// than a fragment, and it is still bounded.
const maxProposalChars = 262144

// mappingSubjects is the arity rule for the one class whose validity depends on
// two external things: a mapping between A and B is meaningless with fewer than
// two subjects, and no new relation type is needed to say so.
const mappingSubjects = 2

// Destination is where a promotion would land: an adapter kind, an opaque
// locator core never parses, an optional workspace, and the capabilities the
// caller declares for it. Capabilities are declared, never inferred — nothing
// here probes a destination to discover what it supports.
type Destination struct {
	Kind         string
	Locator      string
	Workspace    string
	Capabilities []Capability
}

// ParseDestination reads a `kind:locator` argument. The kind is everything
// before the first colon, because a locator routinely contains one.
func ParseDestination(arg string) (Destination, error) {
	kind, locator, ok := strings.Cut(strings.TrimSpace(arg), ":")
	if !ok {
		return Destination{}, Fail(ErrInvalidInput, "invalid_destination",
			"%q is not a destination; expected kind:locator", arg)
	}
	d := Destination{Kind: kind, Locator: locator}
	return d, d.validate()
}

func (d Destination) validate() error {
	if err := ValidateDestKind(d.Kind); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(d.Locator) == "":
		return Fail(ErrInvalidInput, "invalid_destination", "a destination needs a locator")
	case len(d.Locator) > 1024:
		return Fail(ErrInvalidInput, "invalid_destination", "destination locator is longer than 1024 characters")
	case len(d.Workspace) > 255:
		return Fail(ErrInvalidInput, "invalid_destination", "destination workspace is longer than 255 characters")
	}
	return ValidateCapabilities(d.Capabilities)
}

// ProposePromotion is `bdc promote propose`. Revision 0 means the head.
type ProposePromotion struct {
	InsightID          InsightID
	Revision           int
	Class              string
	Destination        Destination
	Content            string
	Evidence           []RefSpec
	RequestedAuthority AuthorityLevel
	Supersedes         ProposalID
	Confidence         float64
}

// ProposalResult is the `{proposal, created, content_hash, authority_required}`
// the CLI contract promises. On an authority block the proposal is still
// recorded and this result still describes it — the operation returns it
// alongside the typed error so a human can grant authority and retry against
// the proposal that exists.
type ProposalResult struct {
	Proposal          Proposal             `json:"proposal"`
	Created           bool                 `json:"created"`
	ContentHash       string               `json:"content_hash"`
	AuthorityRequired AuthorityRequirement `json:"authority_required"`
	Findings          []Finding            `json:"-"`
	Notices           []Notice             `json:"-"`
}

func (l *Ledger) ProposePromotion(ctx context.Context, c ProposePromotion) (ProposalResult, error) {
	if err := l.actor.Validate(); err != nil {
		return ProposalResult{}, err
	}
	if _, err := ParseID(PrefixInsight, string(c.InsightID)); err != nil {
		return ProposalResult{}, err
	}
	if c.Revision < 0 {
		return ProposalResult{}, Fail(ErrInvalidInput, "invalid_revision",
			"a revision number is 1 or greater, got %d", c.Revision)
	}
	if err := ValidateClass(c.Class); err != nil {
		return ProposalResult{}, err
	}
	if err := c.Destination.validate(); err != nil {
		return ProposalResult{}, err
	}
	if err := ValidateConfidence(c.Confidence); err != nil {
		return ProposalResult{}, err
	}
	if c.RequestedAuthority == "" {
		c.RequestedAuthority = AuthorityAdvisory
	}
	if !slices.Contains(authorityLevels, c.RequestedAuthority) {
		return ProposalResult{}, Fail(ErrInvalidInput, "invalid_authority",
			"%q is not an authority level; expected advisory, default, or mandatory", c.RequestedAuthority)
	}
	if c.Supersedes != "" {
		if _, err := ParseID(PrefixProposal, string(c.Supersedes)); err != nil {
			return ProposalResult{}, err
		}
	}
	for _, ref := range c.Evidence {
		if err := ref.Validate(); err != nil {
			return ProposalResult{}, err
		}
		if err := l.rejectSecrets("evidence locator", ref.Locator); err != nil {
			return ProposalResult{}, err
		}
	}
	if err := validateMappingArity(c.Class, c.Evidence); err != nil {
		return ProposalResult{}, err
	}
	// promotion_proposals.dest_locator is a Reject column: an identity value is
	// never rewritten, because a redacted locator names a different place.
	if err := l.rejectSecrets("destination locator", c.Destination.Locator); err != nil {
		return ProposalResult{}, err
	}

	content, findings, err := l.redactField("proposal content", strings.TrimSpace(c.Content))
	if err != nil {
		return ProposalResult{}, err
	}
	switch {
	case content == "":
		return ProposalResult{}, Fail(ErrInvalidInput, "invalid_content",
			"a proposal needs the content that would be written; pass --content or --content-file")
	case len(content) > maxProposalChars:
		return ProposalResult{}, Fail(ErrInvalidInput, "invalid_content",
			"proposal content is %d characters, above the %d-character limit", len(content), maxProposalChars)
	}
	l.assertRedacted("promotion_proposals.content", content)

	required := AuthorityRequiredFor(c.Class, c.Destination.Capabilities, c.RequestedAuthority)
	out := ProposalResult{Findings: findings, AuthorityRequired: required}
	blocked := false

	err = l.store.Write(ctx, func(tx Tx) error {
		revision, err := resolveRevision(tx, c.InsightID, c.Revision)
		if err != nil {
			return err
		}
		if c.Supersedes != "" {
			existing, err := tx.Proposals(PromotionQuery{IDs: []ProposalID{c.Supersedes}})
			if err != nil {
				return err
			}
			if len(existing) == 0 {
				return NotFound("promotion proposal", string(c.Supersedes))
			}
		}

		proposal := Proposal{
			ID: NewProposalID(), InsightID: c.InsightID, RevisionID: revision.ID,
			Class: c.Class, DestKind: c.Destination.Kind, DestLocator: c.Destination.Locator,
			DestWorkspace: c.Destination.Workspace, Capabilities: c.Destination.Capabilities,
			Content: content, Confidence: c.Confidence,
			RequestedAuthority: c.RequestedAuthority, SupersedesProposalID: c.Supersedes,
			PolicyVersion: l.config.PolicyVersion, RedactionVersion: l.redactor.Version(),
			CreatedAt: l.clock(), Provenance: l.actor,
		}
		proposal.ContentHash = hashContent(canonicalProposal(proposal, revision.Revision))

		id, created, err := tx.UpsertProposal(proposal)
		if err != nil {
			return err
		}
		out.Created, out.ContentHash = created, proposal.ContentHash

		record := RecordRef{Kind: KindProposal, ID: string(id)}
		if created {
			for _, ref := range c.Evidence {
				refID, err := tx.UpsertReference(Reference{
					ID: NewReferenceID(), Kind: ref.Kind, Locator: ref.Locator,
					Workspace: ref.Workspace, CreatedAt: proposal.CreatedAt,
				})
				if err != nil {
					return err
				}
				if err := tx.LinkReference(record, refID, ref.Relation); err != nil {
					return err
				}
			}
		}

		// Read the stored row back in both cases. On a hit it is a proposal
		// this call did not write, and on a create the DECIMAL(4,3) confidence
		// has been through the column that stores it.
		stored, err := tx.Proposals(PromotionQuery{IDs: []ProposalID{id}})
		if err != nil {
			return err
		}
		if len(stored) != 1 {
			return Fail(ErrIntegrity, "integrity_missing_proposal",
				"proposal %s was written and cannot be read back", id)
		}
		out.Proposal = stored[0]

		if !created {
			notices, err := divergenceNotices(tx, stored[0], c)
			if err != nil {
				return err
			}
			out.Notices = append(out.Notices, notices...)
		}

		blocked, err = l.authorityUnmet(tx, required, record, stored[0].RevisionID)
		return err
	})
	if err != nil {
		return ProposalResult{}, err
	}
	if blocked {
		// The proposal is recorded and the transaction committed. Only the
		// external write is refused, which is what makes "grant authority and
		// retry" a real path rather than advice.
		return out, Fail(ErrAuthorityDenied, "authority_required",
			"this proposal requires human authority: class %q, destination %s:%s. "+
				"A human can grant it with `bdc authority %s --level default --rationale ...`",
			out.Proposal.Class, out.Proposal.DestKind, out.Proposal.DestLocator, out.Proposal.ID).
			WithDetails(map[string]any{
				"proposal_id":        string(out.Proposal.ID),
				"content_hash":       out.ContentHash,
				"created":            out.Created,
				"authority_required": string(required),
			})
	}
	return out, nil
}

// authorityUnmet reports whether the effective requirement is still unsatisfied.
// A human actor satisfies it directly — a human running the command is a human
// decision — and otherwise a human must have granted the proposal or the
// revision behind it a level above advisory.
func (l *Ledger) authorityUnmet(snap Snapshot, required AuthorityRequirement, proposal RecordRef, revision RevisionID) (bool, error) {
	if required == RequireNone || l.actor.ActorKind == ActorHuman {
		return false, nil
	}
	held, err := humanAuthorityHeld(snap, proposal, RecordRef{Kind: KindRevision, ID: string(revision)})
	return !held, err
}

// canonicalProposal is the serialisation behind content_hash. It covers every
// field that changes *what would be written* or *what authority is needed to
// write it*; confidence and evidence are deliberately outside, because neither
// changes the artifact. Each value is length-prefixed, so no locator or content,
// however exotic, can impersonate a field boundary.
func canonicalProposal(p Proposal, revision int) string {
	var b strings.Builder
	b.WriteString("beadcrumbs.proposal.v1\n")
	write := func(field, value string) {
		fmt.Fprintf(&b, "%s=%d:%s\n", field, len(value), value)
	}
	write("insight", string(p.InsightID))
	write("revision", strconv.Itoa(revision))
	write("class", p.Class)
	write("dest_kind", p.DestKind)
	write("dest_locator", p.DestLocator)
	write("dest_workspace", p.DestWorkspace)
	write("capabilities", EncodeCapabilities(p.Capabilities))
	write("authority", string(p.RequestedAuthority))
	write("content", p.Content)
	return b.String()
}

// divergenceNotices reports what an idempotent hit was asked for and did not
// get. Neither confidence nor evidence is in the hash, so both can differ from
// the stored proposal while naming the same artifact; the stored values win and
// the caller is told, because silently returning someone else's confidence as
// if it were theirs is the failure this exists to prevent.
func divergenceNotices(snap Snapshot, stored Proposal, c ProposePromotion) ([]Notice, error) {
	var notices []Notice
	if stored.Confidence != c.Confidence {
		notices = append(notices, Notice{
			Code: "proposal_confidence_diverged",
			Message: fmt.Sprintf(
				"proposal %s already exists with confidence %.3f; the requested %.3f was not applied, because proposals are immutable",
				stored.ID, stored.Confidence, c.Confidence),
		})
	}
	links, err := snap.ReferenceLinks(RecordRef{Kind: KindProposal, ID: string(stored.ID)})
	if err != nil {
		return nil, err
	}
	var refs []Reference
	if len(links) > 0 {
		ids := make([]ReferenceID, 0, len(links))
		for _, link := range links {
			ids = append(ids, link.ReferenceID)
		}
		if refs, err = snap.References(ReferenceQuery{IDs: ids}); err != nil {
			return nil, err
		}
	}
	byID := make(map[ReferenceID]Reference, len(refs))
	for _, ref := range refs {
		byID[ref.ID] = ref
	}
	held := make(map[string]struct{}, len(links))
	for _, link := range links {
		if ref, ok := byID[link.ReferenceID]; ok {
			held[evidenceKey(RefSpec{
				Kind: ref.Kind, Locator: ref.Locator, Workspace: ref.Workspace, Relation: link.Relation,
			})] = struct{}{}
		}
	}
	for _, ref := range c.Evidence {
		if _, ok := held[evidenceKey(ref)]; !ok {
			notices = append(notices, Notice{
				Code: "proposal_evidence_diverged",
				Message: fmt.Sprintf(
					"proposal %s already exists and does not cite %s:%s as %s; it was not added, because proposals are immutable",
					stored.ID, ref.Kind, ref.Locator, ref.Relation),
			})
		}
	}
	return notices, nil
}

func evidenceKey(ref RefSpec) string {
	return ref.Kind + "\x00" + ref.Locator + "\x00" + ref.Workspace + "\x00" + string(ref.Relation)
}

func validateMappingArity(class string, evidence []RefSpec) error {
	if class != "mapping" {
		return nil
	}
	subjects := 0
	for _, ref := range evidence {
		if ref.Relation == RelationSubject {
			subjects++
		}
	}
	if subjects >= mappingSubjects {
		return nil
	}
	return Fail(ErrInvalidInput, "invalid_mapping_arity",
		"a mapping relates two things: pass at least %d --evidence kind:locator@subject references, got %d",
		mappingSubjects, subjects)
}

// resolveRevision picks the revision a proposal names. Revision 0 is the head,
// which is what an agent proposing "the current conclusion" means.
func resolveRevision(snap Snapshot, id InsightID, number int) (InsightRevision, error) {
	revisions, err := snap.Revisions(id)
	if err != nil {
		return InsightRevision{}, err
	}
	if len(revisions) == 0 {
		return InsightRevision{}, NotFound("insight", string(id))
	}
	if number == 0 {
		return revisions[len(revisions)-1], nil
	}
	for _, rev := range revisions {
		if rev.Revision == number {
			return rev, nil
		}
	}
	return InsightRevision{}, NotFound("revision", string(id)+"@"+strconv.Itoa(number))
}

// RecordPromotion is `bdc promote record`: an external write landed, and this
// is the attributable link back to it.
type RecordPromotion struct {
	ProposalID   ProposalID
	Locator      string
	Anchor       string
	ExternalHash string
	Verified     bool
}

// ReceiptResult is the `{promotion, receipt}` the CLI contract promises.
// Durable says whether the receipt proves a durable record or only that an
// attempt happened; it is read from the destination's declared capabilities and
// never inferred from the anchor's shape.
type ReceiptResult struct {
	Promotion Promotion `json:"promotion"`
	Receipt   Receipt   `json:"receipt"`
	Durable   bool      `json:"durable"`
	Findings  []Finding `json:"-"`
	Notices   []Notice  `json:"-"`
}

func (l *Ledger) RecordPromotion(ctx context.Context, c RecordPromotion) (ReceiptResult, error) {
	if err := l.actor.Validate(); err != nil {
		return ReceiptResult{}, err
	}
	if _, err := ParseID(PrefixProposal, string(c.ProposalID)); err != nil {
		return ReceiptResult{}, err
	}
	switch {
	case strings.TrimSpace(c.Locator) == "":
		return ReceiptResult{}, Fail(ErrInvalidInput, "invalid_receipt",
			"a receipt needs the locator that was actually written; pass --locator")
	case len(c.Locator) > 1024:
		return ReceiptResult{}, Fail(ErrInvalidInput, "invalid_receipt",
			"receipt locator is longer than 1024 characters")
	case len(c.Anchor) > 512:
		return ReceiptResult{}, Fail(ErrInvalidInput, "invalid_receipt",
			"receipt anchor is longer than 512 characters")
	}
	// receipts.{locator,anchor,external_hash} are Reject columns: each is proof
	// of what was written, and proof that was rewritten proves nothing.
	for _, field := range []struct{ name, value string }{
		{"receipt locator", c.Locator},
		{"receipt anchor", c.Anchor},
		{"receipt external hash", c.ExternalHash},
	} {
		if err := l.rejectSecrets(field.name, field.value); err != nil {
			return ReceiptResult{}, err
		}
	}

	var out ReceiptResult
	err := l.store.Write(ctx, func(tx Tx) error {
		out = ReceiptResult{}
		proposal, err := readProposal(tx, c.ProposalID)
		if err != nil {
			return err
		}
		// The same gate propose applies. Without it, an agent could take the
		// exit-3 path, ignore it, and record the write anyway — which is
		// exactly the bypass the authority axis exists to prevent. This one
		// rolls back: an unauthorised attempt is not a fact worth keeping.
		required := AuthorityRequiredFor(proposal.Class, proposal.Capabilities, proposal.RequestedAuthority)
		unmet, err := l.authorityUnmet(tx, required, RecordRef{Kind: KindProposal, ID: string(proposal.ID)}, proposal.RevisionID)
		if err != nil {
			return err
		}
		if unmet {
			return Fail(ErrAuthorityDenied, "authority_required",
				"proposal %s requires human authority before a promotion may be recorded", proposal.ID).
				WithDetails(map[string]any{
					"proposal_id":        string(proposal.ID),
					"content_hash":       proposal.ContentHash,
					"authority_required": string(required),
				})
		}

		attempt, err := nextAttempt(tx, proposal.ID)
		if err != nil {
			return err
		}
		at := l.clock()
		promotion := Promotion{
			ID: NewPromotionID(), ProposalID: proposal.ID, Attempt: attempt,
			Status: PromotionApplied, OccurredAt: at, Provenance: l.actor,
		}
		if err := tx.AppendPromotion(promotion); err != nil {
			return err
		}
		// The written record becomes a Reference like any other, so the graph
		// can reach the artifact without anything learning the destination's
		// locator format.
		refID, err := tx.UpsertReference(Reference{
			ID: NewReferenceID(), Kind: proposal.DestKind, Locator: c.Locator,
			Workspace: proposal.DestWorkspace, CreatedAt: at,
		})
		if err != nil {
			return err
		}
		receipt := Receipt{
			ID: NewReceiptID(), PromotionID: promotion.ID, Kind: proposal.DestKind,
			Locator: c.Locator, Anchor: c.Anchor, ExternalHash: c.ExternalHash,
			Verified: c.Verified, ReferenceID: refID, RecordedAt: at, Provenance: l.actor,
		}
		if err := tx.InsertReceipt(receipt); err != nil {
			return err
		}
		out.Promotion, out.Receipt = promotion, receipt
		out.Durable = durable(proposal.Capabilities, receipt)
		out.Notices = receiptNotices(proposal, receipt)
		return nil
	})
	if err != nil {
		return ReceiptResult{}, err
	}
	return out, nil
}

// durable is the honest reading of a receipt: a record is durably anchored only
// when the destination declared it can be, and an anchor was actually recorded.
func durable(caps []Capability, r Receipt) bool {
	return slices.Contains(caps, CapStableAnchor) && r.Anchor != ""
}

func receiptNotices(p Proposal, r Receipt) []Notice {
	var notices []Notice
	switch {
	case !slices.Contains(p.Capabilities, CapStableAnchor):
		notices = append(notices, Notice{
			Code: "receipt_not_durable",
			Message: fmt.Sprintf(
				"%s does not declare stable-anchor: this receipt proves an attempt happened, not that a durable record exists",
				p.DestKind),
		})
	case r.Anchor == "":
		notices = append(notices, Notice{
			Code: "receipt_without_anchor",
			Message: fmt.Sprintf(
				"%s declares stable-anchor but no --anchor was recorded, so the receipt cannot prove the record it names",
				p.DestKind),
		})
	}
	if r.ExternalHash != "" && !slices.Contains(p.Capabilities, CapContentAddressable) {
		notices = append(notices, Notice{
			Code: "external_hash_not_content_addressable",
			Message: fmt.Sprintf(
				"%s does not declare content-addressable, so the recorded external hash is unverifiable", p.DestKind),
		})
	}
	if !r.Verified {
		notices = append(notices, Notice{
			Code:    "receipt_unverified",
			Message: "the recorder asserted this write rather than observing it; pass --verified only when it was read back",
		})
	}
	return notices
}

// RejectPromotion is a decision not to write. FailPromotion is a write that was
// attempted and did not land. They are separate operations because they mean
// different things to a retry, and both leave the proposal retryable: the next
// record or fail is attempt n+1 against the same proposal.
type RejectPromotion struct {
	ProposalID ProposalID
	Rationale  string
}

type FailPromotion struct {
	ProposalID ProposalID
	Detail     string
}

// PromotionResult is the `{promotion}` the CLI contract promises for the two
// terminal outcomes that write no receipt.
type PromotionResult struct {
	Promotion Promotion `json:"promotion"`
	Findings  []Finding `json:"-"`
}

func (l *Ledger) RejectPromotion(ctx context.Context, c RejectPromotion) (PromotionResult, error) {
	return l.appendOutcome(ctx, c.ProposalID, PromotionRejected, c.Rationale,
		"a rejection needs a rationale; a decision not to write is still a decision")
}

func (l *Ledger) FailPromotion(ctx context.Context, c FailPromotion) (PromotionResult, error) {
	return l.appendOutcome(ctx, c.ProposalID, PromotionFailed, c.Detail,
		"a failure needs a detail; without it nobody can tell a retry from a repeat")
}

// appendOutcome writes one terminal attempt. ck_prm_detail requires the detail
// for both statuses, and the message differs because the two mean different
// things to whoever reads the attempt later.
func (l *Ledger) appendOutcome(ctx context.Context, id ProposalID, status PromotionStatus, detail, missing string) (PromotionResult, error) {
	if err := l.actor.Validate(); err != nil {
		return PromotionResult{}, err
	}
	if _, err := ParseID(PrefixProposal, string(id)); err != nil {
		return PromotionResult{}, err
	}
	if strings.TrimSpace(detail) == "" {
		return PromotionResult{}, Fail(ErrInvalidInput, "invalid_detail", "%s", missing)
	}
	clean, findings, err := l.redactField("detail", strings.TrimSpace(detail))
	if err != nil {
		return PromotionResult{}, err
	}
	l.assertRedacted("promotions.detail", clean)

	out := PromotionResult{Findings: findings}
	err = l.store.Write(ctx, func(tx Tx) error {
		proposal, err := readProposal(tx, id)
		if err != nil {
			return err
		}
		attempt, err := nextAttempt(tx, proposal.ID)
		if err != nil {
			return err
		}
		promotion := Promotion{
			ID: NewPromotionID(), ProposalID: proposal.ID, Attempt: attempt,
			Status: status, Detail: clean, OccurredAt: l.clock(), Provenance: l.actor,
		}
		if err := tx.AppendPromotion(promotion); err != nil {
			return err
		}
		out.Promotion = promotion
		return nil
	})
	if err != nil {
		return PromotionResult{}, err
	}
	return out, nil
}

// nextAttempt is MAX(attempt)+1 for one proposal, read inside the transaction
// that writes the row. uq_prm_attempt is the backstop if two writers ever
// compute the same number; the engine's exclusive lock is why they cannot.
func nextAttempt(snap Snapshot, id ProposalID) (int, error) {
	attempts, _, err := snap.Attempts([]ProposalID{id})
	if err != nil {
		return 0, err
	}
	next := 1
	for _, a := range attempts {
		if a.Attempt >= next {
			next = a.Attempt + 1
		}
	}
	return next, nil
}

func readProposal(snap Snapshot, id ProposalID) (Proposal, error) {
	rows, err := snap.Proposals(PromotionQuery{IDs: []ProposalID{id}})
	if err != nil {
		return Proposal{}, err
	}
	if len(rows) == 0 {
		return Proposal{}, NotFound("promotion proposal", string(id))
	}
	return rows[0], nil
}

// PromotionView is one proposal with its whole attempt history. Status is the
// latest attempt's, or `proposed` when there has been none — a proposal with no
// attempt is not a failure, it is a request nobody has acted on yet.
type PromotionView struct {
	Proposal
	Status            PromotionStatus      `json:"status"`
	AuthorityRequired AuthorityRequirement `json:"authority_required"`
	Attempts          []Promotion          `json:"attempts"`
	Receipt           *Receipt             `json:"receipt"`
	Durable           bool                 `json:"durable"`
}

func (l *Ledger) Promotions(ctx context.Context, q PromotionQuery) ([]PromotionView, error) {
	for _, status := range q.Statuses {
		if !slices.Contains(promotionStatuses, status) {
			return nil, Fail(ErrInvalidInput, "invalid_status",
				"%q is not a promotion status; expected one of %s", status, joinStatuses())
		}
	}
	if q.InsightID != "" {
		if _, err := ParseID(PrefixInsight, string(q.InsightID)); err != nil {
			return nil, err
		}
	}

	views := []PromotionView{}
	err := l.store.Read(ctx, func(snap Snapshot) error {
		proposals, err := snap.Proposals(q)
		if err != nil {
			return err
		}
		ids := make([]ProposalID, 0, len(proposals))
		for _, p := range proposals {
			ids = append(ids, p.ID)
		}
		attempts, receipts, err := snap.Attempts(ids)
		if err != nil {
			return err
		}
		byProposal := map[ProposalID][]Promotion{}
		for _, a := range attempts {
			byProposal[a.ProposalID] = append(byProposal[a.ProposalID], a)
		}
		byPromotion := map[PromotionID]Receipt{}
		for _, r := range receipts {
			byPromotion[r.PromotionID] = r
		}

		for _, p := range proposals {
			view := PromotionView{
				Proposal: p,
				Status:   PromotionProposed,
				AuthorityRequired: AuthorityRequiredFor(
					p.Class, p.Capabilities, p.RequestedAuthority),
				Attempts: byProposal[p.ID],
			}
			if view.Attempts == nil {
				view.Attempts = []Promotion{}
			}
			for _, a := range view.Attempts {
				view.Status = a.Status
				if r, ok := byPromotion[a.ID]; ok {
					receipt := r
					view.Receipt = &receipt
					view.Durable = durable(p.Capabilities, receipt)
				}
			}
			views = append(views, view)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return views, nil
}

var promotionStatuses = []PromotionStatus{
	PromotionProposed, PromotionApplied, PromotionRejected, PromotionFailed, PromotionSuperseded,
}

func joinStatuses() string {
	parts := make([]string, len(promotionStatuses))
	for i, s := range promotionStatuses {
		parts[i] = string(s)
	}
	return strings.Join(parts, ", ")
}
