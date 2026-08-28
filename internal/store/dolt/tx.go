package dolt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// *Store is the ledger's only storage implementation. The assertion is here
// rather than in a test because a missing method is a build error worth having
// at the definition site.
var _ ledger.Store = (*Store)(nil)

// Write runs fn inside one bounded transaction and, on success, ends it with one
// Dolt commit. The commit is what gives the ledger a versioned history — the
// driver makes none on its own — and its message carries the command name and
// actor kind and no domain content.
//
// The SQL commit and the Dolt commit are two steps, in that order. If the Dolt
// commit fails the rows are already durable in the working set and the next
// command's `DOLT_COMMIT -A` picks them up, so the failure mode is a missing
// history entry, never a lost write.
func (s *Store) Write(ctx context.Context, fn func(ledger.Tx) error) error {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageErr(err, "cannot begin a transaction")
	}
	t := &tx{snapshot: snapshot{ctx: ctx, q: sqlTx}}
	if err := fn(t); err != nil {
		if rbErr := sqlTx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return errors.Join(err, storageErr(rbErr, "rollback failed"))
		}
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return storageErr(err, "commit failed")
	}
	return s.Commit(ctx, s.commitMessage())
}

// Read runs fn against a transaction-consistent snapshot. It is a transaction
// even though it writes nothing: a narrative that counted Crumbs in one query
// and listed them in the next could otherwise report a total that does not match
// its own list.
func (s *Store) Read(ctx context.Context, fn func(ledger.Snapshot) error) error {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageErr(err, "cannot begin a read transaction")
	}
	defer func() { _ = sqlTx.Rollback() }()
	return fn(&snapshot{ctx: ctx, q: sqlTx})
}

func (s *Store) commitMessage() string {
	if s.cfg.ActorKind == "" {
		return "bdc " + s.cfg.Command
	}
	return fmt.Sprintf("bdc %s (%s)", s.cfg.Command, s.cfg.ActorKind)
}

// tx is one open transaction. It embeds snapshot because five writes are defined
// in terms of the current row: a review needs from_state, a revision needs the
// next number and its parent, an attempt needs the next attempt, an upsert needs
// the existing hash, and prune needs the supporting-Insight set before deleting.
type tx struct {
	snapshot
}

func (t *tx) InsertCrumb(c ledger.Crumb) error {
	if err := assertID(ledger.PrefixCrumb, string(c.ID)); err != nil {
		return err
	}
	return t.exec(`INSERT INTO crumbs
		(id, content, content_hash, review_state, confidence, captured_at,
		 harvest_id, policy_version, redaction_version,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		append([]any{
			string(c.ID), c.Content, c.ContentHash, string(c.ReviewState), c.Confidence, utc(c.CapturedAt),
			nullStr(string(c.HarvestID)), nullStr(c.PolicyVersion), c.RedactionVersion,
		}, provArgs(c.Provenance)...)...)
}

// AppendCrumbReview writes the event and moves the Crumb's materialised state in
// the same statement pair. The events are the history; review_state is a cache
// of the latest one, and letting them diverge would make every list lie.
func (t *tx) AppendCrumbReview(e ledger.CrumbReviewEvent) error {
	if err := assertID(ledger.PrefixReviewEvent, string(e.ID)); err != nil {
		return err
	}
	if err := t.exec(`INSERT INTO crumb_review_events
		(id, crumb_id, from_state, to_state, rationale, occurred_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		append([]any{
			string(e.ID), string(e.CrumbID), string(e.FromState), string(e.ToState),
			e.Rationale, utc(e.OccurredAt),
		}, provArgs(e.Provenance)...)...); err != nil {
		return err
	}
	n, err := t.execN(`UPDATE crumbs SET review_state = ? WHERE id = ?`, string(e.ToState), string(e.CrumbID))
	if err != nil {
		return err
	}
	if n == 0 {
		return ledger.NotFound("crumb", string(e.CrumbID))
	}
	return nil
}

// DeleteCrumbs removes each Crumb together with the ref_links and validations
// whose polymorphic target is one of them. Those two columns carry no foreign
// key, so nothing else can clean them: verified against dolt 2.3.1, deleting a
// Crumb with one ref_link and one validation leaves both rows behind pointing at
// an id that no longer exists, while crumb_review_events CASCADE correctly.
func (t *tx) DeleteCrumbs(ids []ledger.CrumbID) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = string(id)
	}
	in := placeholders(len(ids))
	if err := t.exec(`DELETE FROM ref_links WHERE record_kind = 'crumb' AND record_id IN (`+in+`)`, args...); err != nil {
		return 0, err
	}
	if err := t.exec(`DELETE FROM validations WHERE target_kind = 'crumb' AND target_id IN (`+in+`)`, args...); err != nil {
		return 0, err
	}
	return t.execN(`DELETE FROM crumbs WHERE id IN (`+in+`)`, args...)
}

func (t *tx) InsertHarvest(h ledger.Harvest, links []ledger.HarvestCrumb) error {
	if err := assertID(ledger.PrefixHarvest, string(h.ID)); err != nil {
		return err
	}
	if err := t.exec(`INSERT INTO harvests
		(id, mode, outcome, failure_code, crumbs_considered, crumbs_selected,
		 policy_version, redaction_version, started_at, finished_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		append([]any{
			string(h.ID), string(h.Mode), string(h.Outcome), nullStr(h.FailureCode),
			h.CrumbsConsidered, h.CrumbsSelected, h.PolicyVersion, h.RedactionVersion,
			utc(h.StartedAt), utc(h.FinishedAt),
		}, provArgs(h.Provenance)...)...); err != nil {
		return err
	}
	for _, l := range links {
		if err := t.exec(`INSERT INTO harvest_crumbs (harvest_id, crumb_id, role) VALUES (?,?,?)`,
			string(h.ID), string(l.CrumbID), string(l.Role)); err != nil {
			return err
		}
	}
	return nil
}

// InsertRevision creates the Insight itself on revision 1 and links the
// supporting Crumbs. The three writes are one method because a revision with no
// Insight, an Insight with no revision, and a revision with no lineage are all
// states every read assumes cannot exist.
func (t *tx) InsertRevision(r ledger.InsightRevision, crumbs []ledger.CrumbID) error {
	if err := assertID(ledger.PrefixRevision, string(r.ID)); err != nil {
		return err
	}
	if err := assertID(ledger.PrefixInsight, string(r.InsightID)); err != nil {
		return err
	}
	if r.Revision == 1 {
		if err := t.exec(`INSERT INTO insights
			(id, head_revision, created_at, actor_id, actor_kind, actor_model, session_id)
			VALUES (?,?,?,?,?,?,?)`,
			append([]any{string(r.InsightID), 1, utc(r.CreatedAt)}, provArgs(r.Provenance)...)...); err != nil {
			return err
		}
	}
	if err := t.exec(`INSERT INTO insight_revisions
		(id, insight_id, revision, title, content, content_hash, class, confidence,
		 rationale, harvest_id, parent_revision_id, created_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		append([]any{
			string(r.ID), string(r.InsightID), r.Revision, r.Title, r.Content, r.ContentHash,
			r.Class, r.Confidence, nullStr(r.Rationale), nullStr(string(r.HarvestID)),
			nullStr(string(r.ParentRevisionID)), utc(r.CreatedAt),
		}, provArgs(r.Provenance)...)...); err != nil {
		return err
	}
	for _, c := range crumbs {
		if err := t.exec(`INSERT INTO insight_crumbs (revision_id, crumb_id) VALUES (?,?)`,
			string(r.ID), string(c)); err != nil {
			return err
		}
	}
	return nil
}

func (t *tx) SetInsightHead(id ledger.InsightID, revision int) error {
	n, err := t.execN(`UPDATE insights SET head_revision = ? WHERE id = ?`, revision, string(id))
	if err != nil {
		return err
	}
	if n == 0 {
		return ledger.NotFound("insight", string(id))
	}
	return nil
}

// UpsertReference resolves (kind, locator, workspace) to one Reference. The
// observed cache — label, meta, fetched_at — is refreshed only when the caller
// supplied one, so a lookup with no enrichment never blanks what enrichment
// previously learned.
func (t *tx) UpsertReference(r ledger.Reference) (ledger.ReferenceID, error) {
	var existing string
	err := t.row(`SELECT id FROM refs WHERE kind = ? AND locator = ? AND workspace = ?`,
		r.Kind, r.Locator, r.Workspace).Scan(&existing)
	switch {
	case err == nil:
		if r.Label == "" && r.Meta == nil && r.FetchedAt.IsZero() {
			return ledger.ReferenceID(existing), nil
		}
		return ledger.ReferenceID(existing), t.exec(
			`UPDATE refs SET label = COALESCE(?, label), meta = COALESCE(?, meta),
			 fetched_at = COALESCE(?, fetched_at) WHERE id = ?`,
			nullStr(r.Label), nullBytes(r.Meta), nullTime(r.FetchedAt), existing)
	case !errors.Is(err, sql.ErrNoRows):
		return "", storageErr(err, "cannot resolve reference %s:%s", r.Kind, r.Locator)
	}
	if err := assertID(ledger.PrefixReference, string(r.ID)); err != nil {
		return "", err
	}
	return r.ID, t.exec(`INSERT INTO refs
		(id, kind, locator, workspace, label, meta, fetched_at, created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		string(r.ID), r.Kind, r.Locator, r.Workspace,
		nullStr(r.Label), nullBytes(r.Meta), nullTime(r.FetchedAt), utc(r.CreatedAt))
}

// LinkReference is idempotent. Attaching the same Reference to the same record
// under the same relation twice is one fact stated twice, not an error. The link
// timestamp is read here rather than injected because the operation carries no
// domain time of its own; nothing depends on it beyond display order.
func (t *tx) LinkReference(rec ledger.RecordRef, ref ledger.ReferenceID, rel ledger.Relation) error {
	var found int
	err := t.row(`SELECT 1 FROM ref_links
		WHERE record_kind = ? AND record_id = ? AND reference_id = ? AND relation = ?`,
		string(rec.Kind), rec.ID, string(ref), string(rel)).Scan(&found)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return storageErr(err, "cannot resolve reference link")
	}
	return t.exec(`INSERT INTO ref_links (record_kind, record_id, reference_id, relation, created_at)
		VALUES (?,?,?,?,?)`,
		string(rec.Kind), rec.ID, string(ref), string(rel), utc(time.Now()))
}

func (t *tx) AppendValidation(v ledger.Validation) error {
	if err := assertID(ledger.PrefixValidation, string(v.ID)); err != nil {
		return err
	}
	return t.exec(`INSERT INTO validations
		(id, target_kind, target_id, verdict, rationale,
		 superseded_by_kind, superseded_by_id, occurred_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		append([]any{
			string(v.ID), string(v.Target.Kind), v.Target.ID, string(v.Verdict), v.Rationale,
			nullStr(string(v.SupersededBy.Kind)), nullStr(v.SupersededBy.ID), utc(v.OccurredAt),
		}, provArgs(v.Provenance)...)...)
}

func (t *tx) AppendAuthority(a ledger.Authority) error {
	if err := assertID(ledger.PrefixAuthority, string(a.ID)); err != nil {
		return err
	}
	return t.exec(`INSERT INTO authorities
		(id, target_kind, target_id, level, scope, destination_kind, destination_locator,
		 rationale, occurred_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		append([]any{
			string(a.ID), string(a.Target.Kind), a.Target.ID, string(a.Level), a.Scope,
			nullStr(a.DestinationKind), nullStr(a.DestinationLocator), a.Rationale, utc(a.OccurredAt),
		}, provArgs(a.Provenance)...)...)
}

// UpsertProposal answers the idempotency question from the database rather than
// from a code convention: uq_pp_hash is the key, and an existing row is a hit,
// not a conflict.
func (t *tx) UpsertProposal(p ledger.Proposal) (ledger.ProposalID, bool, error) {
	var existing string
	err := t.row(`SELECT id FROM promotion_proposals WHERE content_hash = ?`, p.ContentHash).Scan(&existing)
	switch {
	case err == nil:
		return ledger.ProposalID(existing), false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", false, storageErr(err, "cannot resolve proposal by content hash")
	}
	if err := assertID(ledger.PrefixProposal, string(p.ID)); err != nil {
		return "", false, err
	}
	if err := t.exec(`INSERT INTO promotion_proposals
		(id, insight_id, revision_id, class, dest_kind, dest_locator, dest_workspace,
		 dest_capabilities, content, content_hash, confidence, requested_authority,
		 supersedes_proposal_id, policy_version, redaction_version, created_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		append([]any{
			string(p.ID), string(p.InsightID), string(p.RevisionID), p.Class,
			p.DestKind, p.DestLocator, p.DestWorkspace, ledger.EncodeCapabilities(p.Capabilities),
			p.Content, p.ContentHash, p.Confidence, string(p.RequestedAuthority),
			nullStr(string(p.SupersedesProposalID)), p.PolicyVersion, p.RedactionVersion, utc(p.CreatedAt),
		}, provArgs(p.Provenance)...)...); err != nil {
		return "", false, err
	}
	return p.ID, true, nil
}

func (t *tx) AppendPromotion(p ledger.Promotion) error {
	if err := assertID(ledger.PrefixPromotion, string(p.ID)); err != nil {
		return err
	}
	return t.exec(`INSERT INTO promotions
		(id, proposal_id, attempt, status, detail, occurred_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		append([]any{
			string(p.ID), string(p.ProposalID), p.Attempt, string(p.Status),
			nullStr(p.Detail), utc(p.OccurredAt),
		}, provArgs(p.Provenance)...)...)
}

func (t *tx) InsertReceipt(r ledger.Receipt) error {
	if err := assertID(ledger.PrefixReceipt, string(r.ID)); err != nil {
		return err
	}
	verified := 0
	if r.Verified {
		verified = 1
	}
	return t.exec(`INSERT INTO receipts
		(id, promotion_id, kind, locator, anchor, external_hash, verified, reference_id,
		 recorded_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		append([]any{
			string(r.ID), string(r.PromotionID), r.Kind, r.Locator,
			nullStr(r.Anchor), nullStr(r.ExternalHash), verified, nullStr(string(r.ReferenceID)),
			utc(r.RecordedAt),
		}, provArgs(r.Provenance)...)...)
}

func (t *tx) SetConfig(key, value string) error {
	n, err := t.execN(`UPDATE repo_config SET v = ?, updated_at = ? WHERE k = ?`, value, utc(time.Now()), key)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return t.exec(`INSERT INTO repo_config (k, v, updated_at) VALUES (?,?,?)`, key, value, utc(time.Now()))
}

// assertID is a live invariant check, not input validation: every id written
// here is minted by the ledger, so a missing or mistyped prefix means a write
// path built the wrong record and the row would be unreachable by its own show
// command.
func assertID(prefix, id string) error {
	if strings.HasPrefix(id, prefix) && len(id) == len(prefix)+36 {
		return nil
	}
	return ledger.Fail(ledger.ErrIntegrity, "integrity_bad_id",
		"refusing to write id %q: expected a %s id minted by the ledger", id, strings.TrimSuffix(prefix, "_"))
}

func (t *tx) exec(query string, args ...any) error {
	_, err := t.execN(query, args...)
	return err
}

func (t *tx) execN(query string, args ...any) (int, error) {
	res, err := t.q.ExecContext(t.ctx, query, args...)
	if err != nil {
		return 0, translate(err, query)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, storageErr(err, "cannot count affected rows")
	}
	return int(n), nil
}
