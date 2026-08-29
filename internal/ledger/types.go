package ledger

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"
)

// The closed vocabularies. Each is a SQL ENUM, so an invalid value is rejected
// by the database as well as here; extending one is an additive ALTER, not a
// data migration.
type (
	ActorKind       string
	ReviewState     string
	Verdict         string
	AuthorityLevel  string
	Relation        string
	RecordKind      string
	HarvestMode     string
	HarvestOutcome  string
	HarvestRole     string
	PromotionStatus string
	Capability      string
)

const (
	ActorHuman ActorKind = "human"
	ActorAgent ActorKind = "agent"

	StateCandidate ReviewState = "candidate"
	StateAccepted  ReviewState = "accepted"
	StateRejected  ReviewState = "rejected"

	VerdictUnreviewed Verdict = "unreviewed"
	VerdictSupported  Verdict = "supported"
	VerdictDisputed   Verdict = "disputed"
	VerdictRejected   Verdict = "rejected"
	VerdictSuperseded Verdict = "superseded"

	AuthorityAdvisory  AuthorityLevel = "advisory"
	AuthorityDefault   AuthorityLevel = "default"
	AuthorityMandatory AuthorityLevel = "mandatory"

	RelationSource      Relation = "source"
	RelationEvidence    Relation = "evidence"
	RelationSubject     Relation = "subject"
	RelationSpawnedWork Relation = "spawned-work"

	KindCrumb      RecordKind = "crumb"
	KindRevision   RecordKind = "insight_revision"
	KindProposal   RecordKind = "promotion_proposal"
	KindValidation RecordKind = "validation"

	HarvestManual    HarvestMode = "manual"
	HarvestAutomatic HarvestMode = "automatic"

	HarvestCompleted HarvestOutcome = "completed"
	HarvestFailed    HarvestOutcome = "failed"
	HarvestAborted   HarvestOutcome = "aborted"

	RoleConsidered HarvestRole = "considered"
	RoleSelected   HarvestRole = "selected"

	PromotionProposed   PromotionStatus = "proposed"
	PromotionApplied    PromotionStatus = "applied"
	PromotionRejected   PromotionStatus = "rejected"
	PromotionFailed     PromotionStatus = "failed"
	PromotionSuperseded PromotionStatus = "superseded"

	CapRequiresHumanAuthority Capability = "requires-human-authority"
	CapSupportsSupersession   Capability = "supports-supersession"
	CapSupportsReviewThread   Capability = "supports-review-thread"
	CapAppendOnly             Capability = "append-only"
	CapStableAnchor           Capability = "stable-anchor"
	CapContentAddressable     Capability = "content-addressable"
)

// Classes are a validated vocabulary, not a SQL ENUM: adding a semantic class
// must never require a migration. The same is true of destination kinds, which
// have no fixed list at all — an adapter namespace is opaque to core.
var classes = []string{
	"learning", "memory", "decision", "adr", "policy",
	"term", "business-ontology", "technical-ontology", "mapping",
}

// Classes returns the validated semantic classes in declaration order.
func Classes() []string { return slices.Clone(classes) }

func ValidateClass(class string) error {
	if slices.Contains(classes, class) {
		return nil
	}
	return Fail(ErrInvalidInput, "invalid_class",
		"%q is not a semantic class; expected one of %s", class, strings.Join(classes, ", "))
}

// ValidateDestKind checks shape, not membership. `docs` and `beads` are seeds,
// not the closed set, so the only real rule is that a kind is a non-empty
// namespace token that survives round-tripping through a `kind:locator` argument.
func ValidateDestKind(kind string) error {
	if kind == "" {
		return Fail(ErrInvalidInput, "invalid_destination", "a destination kind is required")
	}
	if strings.ContainsAny(kind, ": \t\n") {
		return Fail(ErrInvalidInput, "invalid_destination",
			"destination kind %q may not contain a colon or whitespace", kind)
	}
	if len(kind) > 64 {
		return Fail(ErrInvalidInput, "invalid_destination", "destination kind %q is longer than 64 characters", kind)
	}
	return nil
}

// Provenance is denormalised onto every record and event table on purpose: it
// is immutable, and a join to reconstruct who did something is a join that can
// go missing.
type Provenance struct {
	ActorID    string    `json:"actor_id"`
	ActorKind  ActorKind `json:"actor_kind"`
	ActorModel string    `json:"actor_model,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
}

// Validate mirrors the ck_<t>_prov constraint every table carries. NOT NULL
// alone is not "provenance present": an agent row with empty-string model and
// session inserts cleanly under an IS NOT NULL form, which is why both layers
// check length.
func (p Provenance) Validate() error {
	if p.ActorID == "" {
		return Fail(ErrInvalidInput, "invalid_provenance", "an actor id is required")
	}
	switch p.ActorKind {
	case ActorHuman:
		return nil
	case ActorAgent:
		if p.ActorModel == "" || p.SessionID == "" {
			return Fail(ErrInvalidInput, "invalid_provenance",
				"an agent actor must carry both a model and a session id")
		}
		return nil
	default:
		return Fail(ErrInvalidInput, "invalid_provenance",
			"actor kind must be human or agent, got %q", p.ActorKind)
	}
}

// RecordRef names a record of any kind. Three columns in the schema are
// polymorphic and therefore carry no foreign key; this type is what keeps the
// pairing of kind and id from drifting apart in Go.
type RecordRef struct {
	Kind RecordKind `json:"kind"`
	ID   string     `json:"id"`
}

func (r RecordRef) Zero() bool     { return r.Kind == "" && r.ID == "" }
func (r RecordRef) String() string { return string(r.Kind) + ":" + r.ID }

// MarshalJSON renders a reference to no record as null. `omitempty` does not
// apply to structs, so an absent supersession or subject would otherwise be
// {"kind":"","id":""} — a shape that reads like a record whose kind and id
// happen to be empty, which is not a thing that exists.
func (r RecordRef) MarshalJSON() ([]byte, error) {
	if r.Zero() {
		return []byte("null"), nil
	}
	type ref RecordRef
	return json.Marshal(ref(r))
}

// Crumb is an atomic captured fragment. Content is always post-redaction: raw
// text never reaches this struct's way to the database.
type Crumb struct {
	ID               CrumbID     `json:"id"`
	Content          string      `json:"content"`
	ContentHash      string      `json:"content_hash"`
	ReviewState      ReviewState `json:"review_state"`
	Confidence       float64     `json:"confidence"`
	CapturedAt       time.Time   `json:"captured_at"`
	HarvestID        HarvestID   `json:"harvest_id,omitempty"`
	PolicyVersion    string      `json:"policy_version,omitempty"`
	RedactionVersion string      `json:"redaction_version"`
	Provenance
}

// CrumbReviewEvent is one append-only transition. Review never rewrites a Crumb's
// history; the crumbs row's review_state is the materialised latest transition.
type CrumbReviewEvent struct {
	ID         ReviewEventID `json:"id"`
	CrumbID    CrumbID       `json:"crumb_id"`
	FromState  ReviewState   `json:"from_state"`
	ToState    ReviewState   `json:"to_state"`
	Rationale  string        `json:"rationale"`
	OccurredAt time.Time     `json:"occurred_at"`
	Provenance
}

// Harvest is one synthesis run. A failed or aborted Harvest is written by a
// second transaction after the first rolled back, and carries no content.
type Harvest struct {
	ID               HarvestID      `json:"id"`
	Mode             HarvestMode    `json:"mode"`
	Outcome          HarvestOutcome `json:"outcome"`
	FailureCode      string         `json:"failure_code,omitempty"`
	CrumbsConsidered int            `json:"crumbs_considered"`
	CrumbsSelected   int            `json:"crumbs_selected"`
	PolicyVersion    string         `json:"policy_version"`
	RedactionVersion string         `json:"redaction_version"`
	StartedAt        time.Time      `json:"started_at"`
	FinishedAt       time.Time      `json:"finished_at"`
	Provenance
}

// HarvestCrumb is a Crumb's role in one Harvest. "considered" is bookkeeping and
// cascades away with the Harvest; lineage lives in insight_crumbs.
type HarvestCrumb struct {
	CrumbID CrumbID     `json:"crumb_id"`
	Role    HarvestRole `json:"role"`
}

// Insight is identity plus a materialised head. Revisions remain the truth;
// head_revision exists so a listing does not need a correlated MAX.
type Insight struct {
	ID           InsightID `json:"id"`
	HeadRevision int       `json:"head_revision"`
	CreatedAt    time.Time `json:"created_at"`
	Provenance
}

// InsightRevision is immutable. Revision 1 has no parent and needs no rationale;
// every later revision requires both.
type InsightRevision struct {
	ID               RevisionID `json:"id"`
	InsightID        InsightID  `json:"insight_id"`
	Revision         int        `json:"revision"`
	Title            string     `json:"title"`
	Content          string     `json:"content"`
	ContentHash      string     `json:"content_hash"`
	Class            string     `json:"class"`
	Confidence       float64    `json:"confidence"`
	Rationale        string     `json:"rationale,omitempty"`
	HarvestID        HarvestID  `json:"harvest_id,omitempty"`
	ParentRevisionID RevisionID `json:"parent_revision_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	Provenance
}

// Reference is an opaque locator in an adapter namespace. Label, Meta, and
// FetchedAt are an observed cache and are never authoritative — nothing joins on
// them for correctness.
type Reference struct {
	ID        ReferenceID `json:"id"`
	Kind      string      `json:"kind"`
	Locator   string      `json:"locator"`
	Workspace string      `json:"workspace"`
	Label     string      `json:"label,omitempty"`
	Meta      []byte      `json:"meta,omitempty"`
	FetchedAt time.Time   `json:"fetched_at,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
}

// ReferenceLink attaches a Reference to a record under one semantic relation.
type ReferenceLink struct {
	Record      RecordRef   `json:"record"`
	ReferenceID ReferenceID `json:"reference_id"`
	Relation    Relation    `json:"relation"`
	CreatedAt   time.Time   `json:"created_at"`
}

// Validation is an append-only evidence judgement. Absence of any row means
// unreviewed; the current verdict is the latest row by occurrence.
type Validation struct {
	ID           ValidationID `json:"id"`
	Target       RecordRef    `json:"target"`
	Verdict      Verdict      `json:"verdict"`
	Rationale    string       `json:"rationale"`
	SupersededBy RecordRef    `json:"superseded_by,omitempty"`
	OccurredAt   time.Time    `json:"occurred_at"`
	Provenance
}

// Authority is an append-only grant. Only a human may grant mandatory; the
// ledger rejects it first and ck_aut_mandatory_human is the live assertion
// behind that check.
type Authority struct {
	ID                 AuthorityID    `json:"id"`
	Target             RecordRef      `json:"target"`
	Level              AuthorityLevel `json:"level"`
	Scope              string         `json:"scope"`
	DestinationKind    string         `json:"destination_kind,omitempty"`
	DestinationLocator string         `json:"destination_locator,omitempty"`
	Rationale          string         `json:"rationale"`
	OccurredAt         time.Time      `json:"occurred_at"`
	Provenance
}

// Proposal is a destination-neutral promotion request, immutable once written
// and keyed by ContentHash. Two proposals with the same hash are the same
// proposal — that is what makes idempotency a database property.
type Proposal struct {
	ID                   ProposalID     `json:"id"`
	InsightID            InsightID      `json:"insight_id"`
	RevisionID           RevisionID     `json:"revision_id"`
	Class                string         `json:"class"`
	DestKind             string         `json:"dest_kind"`
	DestLocator          string         `json:"dest_locator"`
	DestWorkspace        string         `json:"dest_workspace"`
	Capabilities         []Capability   `json:"capabilities,omitempty"`
	Content              string         `json:"content"`
	ContentHash          string         `json:"content_hash"`
	Confidence           float64        `json:"confidence"`
	RequestedAuthority   AuthorityLevel `json:"requested_authority"`
	SupersedesProposalID ProposalID     `json:"supersedes_proposal_id,omitempty"`
	PolicyVersion        string         `json:"policy_version"`
	RedactionVersion     string         `json:"redaction_version"`
	CreatedAt            time.Time      `json:"created_at"`
	Provenance
}

// Promotion is one independent attempt against a Proposal. Attempts are numbered
// from 1 and never reused, so a destination outage leaves the Proposal retryable
// rather than stranded.
type Promotion struct {
	ID         PromotionID     `json:"id"`
	ProposalID ProposalID      `json:"proposal_id"`
	Attempt    int             `json:"attempt"`
	Status     PromotionStatus `json:"status"`
	Detail     string          `json:"detail,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
	Provenance
}

// Receipt records what an applied attempt actually wrote. Locator may differ
// from what was proposed — ADR numbering is decided by the repository, not by
// the proposal — and Anchor's strength is declared by the destination's
// capabilities, never assumed.
type Receipt struct {
	ID           ReceiptID   `json:"id"`
	PromotionID  PromotionID `json:"promotion_id"`
	Kind         string      `json:"kind"`
	Locator      string      `json:"locator"`
	Anchor       string      `json:"anchor,omitempty"`
	ExternalHash string      `json:"external_hash,omitempty"`
	Verified     bool        `json:"verified"`
	ReferenceID  ReferenceID `json:"reference_id,omitempty"`
	RecordedAt   time.Time   `json:"recorded_at"`
	Provenance
}

// capabilities is the vocabulary in declaration order. Encoding, decoding, and
// validation all read it, so a new member is added in one place and the stored
// form stays canonical.
var capabilities = []Capability{
	CapRequiresHumanAuthority, CapSupportsSupersession, CapSupportsReviewThread,
	CapAppendOnly, CapStableAnchor, CapContentAddressable,
}

// EncodeCapabilities renders a capability set as the comma-joined form a SQL SET
// column accepts, deduplicated and in declaration order so the stored value is
// canonical rather than insertion-ordered.
func EncodeCapabilities(caps []Capability) string {
	var out []string
	for _, c := range capabilities {
		if slices.Contains(caps, c) {
			out = append(out, string(c))
		}
	}
	return strings.Join(out, ",")
}

// DecodeCapabilities parses a SQL SET value. An unknown member is an error, not
// a silent drop: it means the column was extended by a build this one cannot
// interpret.
func DecodeCapabilities(raw string) ([]Capability, error) {
	if raw == "" {
		return nil, nil
	}
	var out []Capability
	for _, part := range strings.Split(raw, ",") {
		c := Capability(part)
		if !slices.Contains(capabilities, c) {
			return nil, Fail(ErrIntegrity, "integrity_unknown_capability",
				"stored capability %q is not one this build understands", part)
		}
		out = append(out, c)
	}
	return out, nil
}

func ValidateCapabilities(caps []Capability) error {
	for _, c := range caps {
		if !slices.Contains(capabilities, c) {
			return Fail(ErrInvalidInput, "invalid_capability",
				"%q is not a destination capability; expected one of %s", c, joinNames(capabilities))
		}
	}
	return nil
}

// joinNames renders a closed vocabulary for the error that rejects a value not
// in it. Every vocabulary in the package is a string type, so one helper covers
// all of them and no list is written out twice.
func joinNames[T ~string](values []T) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = string(v)
	}
	return strings.Join(parts, ", ")
}

// ValidateConfidence is the Go-side half of the DECIMAL(4,3) CHECK every table
// with a confidence carries. The range check mirrors the constraint; the
// precision check exists because a fourth decimal place would be silently
// rounded on write, and a confidence that changed value between the command line
// and the ledger is worse than a rejected one.
func ValidateConfidence(c float64) error {
	if c < 0 || c > 1 {
		return Fail(ErrInvalidInput, "invalid_confidence", "confidence must be between 0 and 1, got %v", c)
	}
	if math.Abs(c*1000-math.Round(c*1000)) > 1e-6 {
		return Fail(ErrInvalidInput, "invalid_confidence",
			"confidence %v has more precision than the ledger stores (3 decimal places)", c)
	}
	return nil
}
