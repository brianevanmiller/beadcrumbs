package ledger

import (
	"context"
	"time"
)

// Store is the storage port. It is domain-shaped rather than CRUD: every Tx
// method writes one complete domain fact, so a caller cannot assemble half of
// one and no SQL escapes the storage package.
type Store interface {
	// Write runs fn inside one bounded transaction. A non-nil return rolls the
	// whole transaction back; a nil return commits it and ends it with one Dolt
	// commit, which is what gives the ledger a versioned history.
	Write(ctx context.Context, fn func(Tx) error) error

	// Read runs fn against a transaction-consistent snapshot.
	Read(ctx context.Context, fn func(Snapshot) error) error

	Maintenance
	Close() error
}

// Maintenance is the lifecycle half of the port. Restore is deliberately absent:
// it replaces the directory the Store lives in, so it cannot be a method on an
// open Store.
type Maintenance interface {
	Migrate(ctx context.Context) (MigrationResult, error)
	SchemaVersion(ctx context.Context) (int, error)
	Backup(ctx context.Context, destURL string) (BackupResult, error)
	GC(ctx context.Context) (GCResult, error)
	Diagnose(ctx context.Context) (StoreReport, error)
}

// Tx embeds Snapshot because several writes are defined in terms of the current
// row and are otherwise unimplementable: a review reads the Crumb to fill
// from_state, a revision reads MAX(revision) for its number and parent, an
// attempt reads MAX(attempt), and prune reads insight_crumbs *before* deleting
// so it can report blockage per id. A foreign-key violation aborts the whole
// transaction and loses that per-id answer, so fk_ic_crumb is the backstop, not
// the check.
type Tx interface {
	Snapshot

	InsertCrumb(Crumb) error
	AppendCrumbReview(CrumbReviewEvent) error

	// DeleteCrumbs removes each Crumb and, in the same transaction, the
	// ref_links and validations whose polymorphic target is one of them. Those
	// two columns carry no foreign key, so nothing else can clean them; a Crumb
	// deleted without this leaves rows pointing at an id that no longer exists.
	DeleteCrumbs([]CrumbID) (int, error)

	InsertHarvest(Harvest, []HarvestCrumb) error

	// AppendHarvestCrumbs records the role of Crumbs the Harvest row already
	// exists for. It is separate from InsertHarvest because crumbs.harvest_id
	// points at the Harvest while harvest_crumbs.crumb_id points at the Crumb:
	// a Crumb captured *by* a Harvest can only be inserted after the Harvest
	// row, so its role can only be linked after that. One call cannot satisfy
	// both foreign keys.
	AppendHarvestCrumbs(HarvestID, []HarvestCrumb) error

	// InsertRevision creates the Insight itself on revision 1 and links the
	// supporting Crumbs. Splitting the two would allow a revision with no
	// Insight, or an Insight with no revision, both of which the reads assume
	// cannot exist.
	InsertRevision(InsightRevision, []CrumbID) error
	SetInsightHead(InsightID, int) error

	// UpsertReference resolves (kind, locator, workspace) to one Reference,
	// returning the existing id when it is already known and refreshing the
	// observed cache only when the caller supplied one.
	UpsertReference(Reference) (ReferenceID, error)
	LinkReference(RecordRef, ReferenceID, Relation) error

	AppendValidation(Validation) error
	AppendAuthority(Authority) error

	// UpsertProposal returns created=false for an idempotent hit on
	// content_hash. That is not an error; it is the answer.
	UpsertProposal(Proposal) (ProposalID, bool, error)
	AppendPromotion(Promotion) error
	InsertReceipt(Receipt) error

	SetConfig(key, value string) error
}

// Snapshot is every read the product needs. Results carry no storage concept:
// no row ids, no cursors, no open statements.
type Snapshot interface {
	Crumbs(CrumbQuery) ([]CrumbRow, error)
	CrumbLinks(CrumbID) (CrumbLinkRows, error)
	Insights(InsightQuery) ([]InsightRow, error)
	Revisions(InsightID) ([]RevisionRow, error)
	References(ReferenceQuery) ([]ReferenceRow, error)
	ReferenceLinks(RecordRef) ([]ReferenceLinkRow, error)
	Proposals(PromotionQuery) ([]ProposalRow, error)
	Attempts([]ProposalID) ([]PromotionRow, []ReceiptRow, error)

	// Events is validations, authorities, and reviews in one time-ordered
	// sequence, because every narrative read wants them interleaved.
	Events(EventQuery) ([]EventRow, error)

	// OrphanTargets scans all three polymorphic target columns — ref_links,
	// validations, and authorities — not just the one that can dangle today.
	OrphanTargets() ([]OrphanRow, error)

	// HeadRevisionDrift reports Insights whose materialised head disagrees with
	// MAX(revision). The head is a cache, and a cache with no check is a lie
	// waiting to happen.
	HeadRevisionDrift() ([]HeadDriftRow, error)

	Counts(CountQuery) (Counts, error)

	// Config is repo_config as raw key/value text. root.go reads it from the
	// same snapshot it opens, because the Redactor is constructed before the
	// Ledger that injects it.
	Config() (map[string]string, error)
}

// Redactor runs inside the ledger, on every write path that accepts free text.
// A Finding carries the rule id, offset, length, and replacement token — never
// the matched substring, which would defeat the redaction it reports.
type Redactor interface {
	Version() string
	Redact(text string) (clean string, findings []Finding, err error)
}

// Finding is one redaction hit. It deliberately cannot carry the secret.
type Finding struct {
	Rule        string `json:"rule"`
	Offset      int    `json:"offset"`
	Length      int    `json:"length"`
	Replacement string `json:"replacement"`
}

// Enricher is optional metadata lookup for a Reference. It may never fail a core
// write: an enrichment error is a warning, and the Reference still resolves to
// its locator.
type Enricher interface {
	Kind() string
	Enrich(ctx context.Context, locator, workspace string) (label string, meta []byte, fetchedAt time.Time, err error)
}

// Maintenance results. They live here rather than in the storage package because
// the port owns its own result types; the storage package aliases them so a
// caller never learns which engine produced one.
type MigrationResult struct {
	From    int      `json:"from"`
	To      int      `json:"to"`
	Applied []string `json:"applied"`
}

type BackupResult struct {
	Destination   string `json:"destination"`
	Bytes         int64  `json:"bytes"`
	SchemaVersion int    `json:"schema_version"`
}

type GCResult struct {
	BeforeBytes int64 `json:"before_bytes"`
	AfterBytes  int64 `json:"after_bytes"`
	DurationMS  int64 `json:"duration_ms"`
}

// Check statuses. Only StatusFail makes a report not OK; a warning is actionable
// but not broken.
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
)

// Check is one named diagnostic. Name is what a script matches on; Detail is
// prose for a human.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// StoreReport is the storage half of `bdc doctor`.
type StoreReport struct {
	LedgerPath    string  `json:"ledger_path"`
	Stealth       bool    `json:"stealth"`
	SchemaVersion int     `json:"schema_version"`
	JournalBytes  int64   `json:"journal_bytes"`
	TotalBytes    int64   `json:"total_bytes"`
	GCRecommended bool    `json:"gc_recommended"`
	Checks        []Check `json:"checks"`
	OK            bool    `json:"ok"`
}

// Add appends a check and, for a failure, flips the report. It is a method
// rather than a helper in each caller so "what makes a report not OK" has one
// answer.
func (r *StoreReport) Add(name, status, detail string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: detail})
	if status == StatusFail {
		r.OK = false
	}
}

// Queries. A zero-valued query means "everything", so a caller that forgets a
// filter gets a wide answer rather than an empty one it might mistake for a fact.
type CrumbQuery struct {
	IDs    []CrumbID
	States []ReviewState
	Since  time.Time
	Before time.Time

	// SessionID and HarvestID are capture provenance: which session recorded
	// the Crumb and which Harvest captured it. RevisionIDs runs the other way —
	// the Crumbs supporting one or more Insight revisions — and is what makes
	// `bdc insight show` a keyed lookup instead of a walk of every Crumb in the
	// ledger.
	SessionID   string
	HarvestID   HarvestID
	RevisionIDs []RevisionID

	Limit  int
	Offset int
}

// InsightQuery's Verdicts and AuthorityLevels filter on the *latest* event for
// the head revision, which is the only reading of "this Insight is disputed"
// that matches how the append-only history is interpreted everywhere else.
type InsightQuery struct {
	IDs             []InsightID
	Classes         []string
	Verdicts        []Verdict
	AuthorityLevels []AuthorityLevel
	Since           time.Time
	Limit           int
	Offset          int
}

type ReferenceQuery struct {
	IDs       []ReferenceID
	Kinds     []string
	Relations []Relation
	Target    *RecordRef
	Limit     int
}

type PromotionQuery struct {
	IDs         []ProposalID
	InsightID   InsightID
	ContentHash string
	DestKinds   []string
	Statuses    []PromotionStatus
	Limit       int
}

type EventQuery struct {
	Targets []RecordRef
	Since   time.Time
	Limit   int
}

type CountQuery struct {
	Since time.Time
}

// Rows. Where a read returns exactly the record that was written, the row type
// is that record: a parallel struct would be a second place for a field to go
// missing.
type (
	CrumbRow         = Crumb
	RevisionRow      = InsightRevision
	ReferenceRow     = Reference
	ReferenceLinkRow = ReferenceLink
	ProposalRow      = Proposal
	PromotionRow     = Promotion
	ReceiptRow       = Receipt
)

// InsightRow is an Insight joined to its head revision, because no listing of
// Insights is useful without the head's title, class, and confidence.
type InsightRow struct {
	Insight
	HeadRevisionID RevisionID `json:"head_revision_id"`
	Title          string     `json:"title"`
	Class          string     `json:"class"`
	Confidence     float64    `json:"confidence"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// CrumbLinkRows is everything a Crumb feeds. Revisions is what makes prune's
// blocked[] per-id rather than a transaction-wide foreign key error.
type CrumbLinkRows struct {
	Harvests  []HarvestLinkRow `json:"harvests"`
	Revisions []RevisionRow    `json:"revisions"`
}

type HarvestLinkRow struct {
	HarvestID  HarvestID   `json:"harvest_id"`
	Role       HarvestRole `json:"role"`
	FinishedAt time.Time   `json:"finished_at"`
}

// EventKind distinguishes the three append-only histories Events interleaves.
type EventKind string

const (
	EventReview     EventKind = "review"
	EventValidation EventKind = "validation"
	EventAuthority  EventKind = "authority"
)

// EventRow is the common shape of the three histories. Summary is the
// kind-specific verb — the new review state, the verdict, or the authority level
// — so a narrative can render a timeline without switching on Kind.
type EventRow struct {
	Kind       EventKind `json:"kind"`
	ID         string    `json:"id"`
	Target     RecordRef `json:"target"`
	Summary    string    `json:"summary"`
	Rationale  string    `json:"rationale"`
	OccurredAt time.Time `json:"occurred_at"`
	Provenance
}

// OrphanRow is one polymorphic reference with no live target.
type OrphanRow struct {
	Table      string     `json:"table"`
	Column     string     `json:"column"`
	RecordKind RecordKind `json:"record_kind"`
	RecordID   string     `json:"record_id"`
}

// HeadDriftRow is one Insight whose materialised head disagrees with its
// revisions.
type HeadDriftRow struct {
	InsightID   InsightID `json:"insight_id"`
	Head        int       `json:"head"`
	MaxRevision int       `json:"max_revision"`
}

// Counts is the tally every narrative and diagnostic read needs.
type Counts struct {
	CrumbsByState      map[ReviewState]int     `json:"crumbs_by_state"`
	Insights           int                     `json:"insights"`
	Revisions          int                     `json:"revisions"`
	Harvests           int                     `json:"harvests"`
	References         int                     `json:"references"`
	Proposals          int                     `json:"proposals"`
	PromotionsByStatus map[PromotionStatus]int `json:"promotions_by_status"`
}
