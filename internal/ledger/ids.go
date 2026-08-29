package ledger

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Ids are kind-prefixed UUID text. Event and record ids (Crumbs, Insights,
// harvests, proposals, …) are UUIDv7: time-ordered, so an id sorts by creation
// and a listing needs no secondary key. Reference ids are the exception — they
// are a SHA-256 of the natural key, formatted as a UUID — so two clones that
// name the same (kind, locator, workspace) mint the same primary key and a
// Dolt merge is an idempotent no-op rather than a unique-constraint violation.
//
// The prefix makes an id self-describing wherever one appears — a CLI argument,
// a JSON envelope, a `dolt sql` result — so a caller can tell a Crumb id from a
// proposal id without asking the ledger.
//
// Every id fits CHAR(40): a four-character prefix plus the 36-character
// canonical UUID. `pp_` is 39, and CHAR returns it unpadded; a padded value
// would break every foreign key that joins on it.
const (
	PrefixCrumb       = "crb_"
	PrefixReviewEvent = "cre_"
	PrefixHarvest     = "hrv_"
	PrefixInsight     = "ins_"
	PrefixRevision    = "rev_"
	PrefixReference   = "ref_"
	PrefixValidation  = "val_"
	PrefixAuthority   = "aut_"
	PrefixProposal    = "pp_"
	PrefixPromotion   = "prm_"
	PrefixReceipt     = "rcp_"
)

// The id types are distinct so the compiler catches a Crumb id passed where a
// revision id belongs — the single most likely mistake in a schema where every
// key is a 40-character string.
type (
	CrumbID       string
	ReviewEventID string
	HarvestID     string
	InsightID     string
	RevisionID    string
	ReferenceID   string
	ValidationID  string
	AuthorityID   string
	ProposalID    string
	PromotionID   string
	ReceiptID     string
)

func NewCrumbID() CrumbID             { return CrumbID(mint(PrefixCrumb)) }
func NewReviewEventID() ReviewEventID { return ReviewEventID(mint(PrefixReviewEvent)) }
func NewHarvestID() HarvestID         { return HarvestID(mint(PrefixHarvest)) }
func NewInsightID() InsightID         { return InsightID(mint(PrefixInsight)) }
func NewRevisionID() RevisionID       { return RevisionID(mint(PrefixRevision)) }
func NewValidationID() ValidationID   { return ValidationID(mint(PrefixValidation)) }
func NewAuthorityID() AuthorityID     { return AuthorityID(mint(PrefixAuthority)) }
func NewProposalID() ProposalID       { return ProposalID(mint(PrefixProposal)) }
func NewPromotionID() PromotionID     { return PromotionID(mint(PrefixPromotion)) }
func NewReceiptID() ReceiptID         { return ReceiptID(mint(PrefixReceipt)) }

// identitySeparator is the unit separator between the three fields of a
// Reference's natural key. Kind, locator, and workspace must not contain it —
// otherwise two distinct tuples can concatenate to the same bytes and collide
// on the primary key while remaining distinct on uq_refs_identity.
const identitySeparator byte = 0x1F

// ReferenceIDFor is the id of the Reference that names (kind, locator, workspace).
// The canonical form is `kind || 0x1F || locator || 0x1F || workspace` with the
// stored workspace — the empty string, never SQL NULL. Nothing else is folded:
// no trim, no case change, no path clean. The digest is the first 16 bytes of
// SHA-256, rendered as a UUID so the id still fits CHAR(40) and ParseID.
func ReferenceIDFor(kind, locator, workspace string) ReferenceID {
	sum := sha256.Sum256([]byte(kind + string(identitySeparator) + locator + string(identitySeparator) + workspace))
	var u uuid.UUID
	copy(u[:], sum[:16])
	return ReferenceID(PrefixReference + u.String())
}

// mint panics rather than returning an error. uuid.NewV7 fails only when
// crypto/rand fails, which is not a condition a ledger write can meaningfully
// degrade around: crash, don't trash.
func mint(prefix string) string {
	u, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Sprintf("beadcrumbs: cannot generate an id: %v", err))
	}
	id := prefix + u.String()
	if len(id) > 40 {
		panic(fmt.Sprintf("beadcrumbs: id %q exceeds CHAR(40)", id))
	}
	return id
}

// ParseID validates a user-supplied id against its expected prefix. The check is
// worth doing at the boundary: `bdc insight show crb_…` would otherwise become a
// not-found for a record that exists under a different kind.
func ParseID(prefix, s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", Fail(ErrInvalidInput, "invalid_id", "an id is required")
	case !strings.HasPrefix(s, prefix):
		return "", Fail(ErrInvalidInput, "invalid_id", "%q is not a %s id", s, strings.TrimSuffix(prefix, "_"))
	case len(s) != len(prefix)+36:
		return "", Fail(ErrInvalidInput, "invalid_id", "%q is not a well-formed id", s)
	}
	if _, err := uuid.Parse(s[len(prefix):]); err != nil {
		return "", Fail(ErrInvalidInput, "invalid_id", "%q is not a well-formed id", s)
	}
	return s, nil
}
