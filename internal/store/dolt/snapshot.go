package dolt

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// queryer is what a snapshot needs from either an open transaction or the raw
// engine. Nothing else in this package depends on which one it got.
type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// snapshot is the read half of the storage port. Every result it returns is a
// domain value: no row ids, no cursors, no open statements escape it.
type snapshot struct {
	ctx context.Context
	q   queryer
}

func (s *snapshot) row(query string, args ...any) *sql.Row {
	return s.q.QueryRowContext(s.ctx, query, args...)
}

// scanRows runs a query and folds each row through scan, so no read has to
// remember to close its *sql.Rows or to check Err after the loop.
func scanRows[T any](s *snapshot, query string, args []any, scan func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := s.q.QueryContext(s.ctx, query, args...)
	if err != nil {
		return nil, translate(err, query)
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, storageErr(err, "cannot read a row from %s", firstLine(query))
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr(err, "cannot read rows from %s", firstLine(query))
	}
	return out, nil
}

const crumbColumns = `id, content, content_hash, review_state, confidence, captured_at,
	harvest_id, policy_version, redaction_version, actor_id, actor_kind, actor_model, session_id`

// Crumbs lists newest first. Descending is the only ordering under which Limit
// means what a caller expects — the most recent N, not the oldest.
func (s *snapshot) Crumbs(q ledger.CrumbQuery) ([]ledger.CrumbRow, error) {
	w := newWhere()
	w.inStrings("id", strs(q.IDs))
	w.inStrings("review_state", strs(q.States))
	w.gte("captured_at", q.Since)
	w.lt("captured_at", q.Before)
	w.eq("session_id", q.SessionID)
	w.eq("harvest_id", string(q.HarvestID))
	// The supporting set of a revision is a semi-join rather than a JOIN:
	// joining insight_crumbs would repeat a Crumb once per revision it feeds,
	// and a Crumb is never consumed, so that repetition is the normal case.
	if revisions := strs(q.RevisionIDs); len(revisions) > 0 {
		w.conds = append(w.conds, `id IN (SELECT ic.crumb_id FROM insight_crumbs ic
			WHERE ic.revision_id IN (`+placeholders(len(revisions))+`))`)
		for _, id := range revisions {
			w.args = append(w.args, id)
		}
	}
	query := `SELECT ` + crumbColumns + ` FROM crumbs` + w.clause() +
		` ORDER BY captured_at DESC, id DESC` + limitOffset(q.Limit, q.Offset)
	return scanRows(s, query, w.args, scanCrumb)
}

func scanCrumb(rows *sql.Rows) (ledger.Crumb, error) {
	var (
		c        ledger.Crumb
		state    string
		captured time.Time
		harvest  sql.NullString
		policy   sql.NullString
		prov     provScan
	)
	if err := rows.Scan(&c.ID, &c.Content, &c.ContentHash, &state, decimalInto(&c.Confidence),
		&captured, &harvest, &policy, &c.RedactionVersion,
		&prov.id, &prov.kind, &prov.model, &prov.session); err != nil {
		return c, err
	}
	c.ReviewState = ledger.ReviewState(state)
	c.CapturedAt = captured.UTC()
	c.HarvestID = ledger.HarvestID(harvest.String)
	c.PolicyVersion = policy.String
	c.Provenance = prov.value()
	return c, nil
}

// CrumbLinks is everything a Crumb feeds. Revisions is what makes prune's
// blocked[] per-id: the ledger reads it before deleting, because a foreign-key
// violation aborts the whole transaction and loses the per-id answer.
func (s *snapshot) CrumbLinks(id ledger.CrumbID) (ledger.CrumbLinkRows, error) {
	var out ledger.CrumbLinkRows
	harvests, err := scanRows(s, `SELECT hc.harvest_id, hc.role, h.finished_at
		FROM harvest_crumbs hc JOIN harvests h ON h.id = hc.harvest_id
		WHERE hc.crumb_id = ? ORDER BY h.finished_at, hc.harvest_id`,
		[]any{string(id)}, func(rows *sql.Rows) (ledger.HarvestLinkRow, error) {
			var (
				r        ledger.HarvestLinkRow
				role     string
				finished time.Time
			)
			if err := rows.Scan(&r.HarvestID, &role, &finished); err != nil {
				return r, err
			}
			r.Role, r.FinishedAt = ledger.HarvestRole(role), finished.UTC()
			return r, nil
		})
	if err != nil {
		return out, err
	}
	out.Harvests = harvests

	out.Revisions, err = scanRows(s, `SELECT `+prefixed("r", revisionColumns)+`
		FROM insight_revisions r JOIN insight_crumbs ic ON ic.revision_id = r.id
		WHERE ic.crumb_id = ? ORDER BY r.created_at, r.id`,
		[]any{string(id)}, scanRevision)
	return out, err
}

const revisionColumns = `id, insight_id, revision, title, content, content_hash, class,
	confidence, rationale, harvest_id, parent_revision_id, created_at,
	actor_id, actor_kind, actor_model, session_id`

func scanRevision(rows *sql.Rows) (ledger.InsightRevision, error) {
	var (
		r         ledger.InsightRevision
		rationale sql.NullString
		harvest   sql.NullString
		parent    sql.NullString
		created   time.Time
		prov      provScan
	)
	if err := rows.Scan(&r.ID, &r.InsightID, &r.Revision, &r.Title, &r.Content, &r.ContentHash,
		&r.Class, decimalInto(&r.Confidence), &rationale, &harvest, &parent, &created,
		&prov.id, &prov.kind, &prov.model, &prov.session); err != nil {
		return r, err
	}
	r.Rationale = rationale.String
	r.HarvestID = ledger.HarvestID(harvest.String)
	r.ParentRevisionID = ledger.RevisionID(parent.String)
	r.CreatedAt = created.UTC()
	r.Provenance = prov.value()
	return r, nil
}

// Insights joins each Insight to its head revision, because no listing is useful
// without the head's title, class, and confidence.
//
// Verdict and authority filters read the *latest* event for the head revision
// and fall back to the meaning of absence — unreviewed, advisory — which is the
// same reading every other part of the product gives an empty history.
//
// The CAST inside each subquery is load-bearing: COALESCE over a bare ENUM
// collapses it to its integer index in dolt 2.3.1, so `COALESCE(verdict,
// 'unreviewed')` returns "2" and every string comparison against it silently
// fails.
func (s *snapshot) Insights(q ledger.InsightQuery) ([]ledger.InsightRow, error) {
	w := newWhere()
	w.inStrings("i.id", strs(q.IDs))
	w.inStrings("r.class", q.Classes)
	w.gte("r.created_at", q.Since)
	if len(q.Verdicts) > 0 {
		w.inStrings(`COALESCE((SELECT CAST(v.verdict AS CHAR) FROM validations v
			WHERE v.target_kind = 'insight_revision' AND v.target_id = r.id
			ORDER BY v.occurred_at DESC, v.id DESC LIMIT 1), 'unreviewed')`, strs(q.Verdicts))
	}
	if len(q.AuthorityLevels) > 0 {
		w.inStrings(`COALESCE((SELECT CAST(a.level AS CHAR) FROM authorities a
			WHERE a.target_kind = 'insight_revision' AND a.target_id = r.id
			ORDER BY a.occurred_at DESC, a.id DESC LIMIT 1), 'advisory')`, strs(q.AuthorityLevels))
	}
	query := `SELECT i.id, i.head_revision, i.created_at,
		i.actor_id, i.actor_kind, i.actor_model, i.session_id,
		r.id, r.title, r.class, r.confidence, r.created_at
		FROM insights i
		JOIN insight_revisions r ON r.insight_id = i.id AND r.revision = i.head_revision` +
		w.clause() + ` ORDER BY r.created_at DESC, i.id DESC` + limitOffset(q.Limit, q.Offset)

	return scanRows(s, query, w.args, func(rows *sql.Rows) (ledger.InsightRow, error) {
		var (
			row      ledger.InsightRow
			created  time.Time
			updated  time.Time
			prov     provScan
			headRevi string
		)
		if err := rows.Scan(&row.ID, &row.HeadRevision, &created,
			&prov.id, &prov.kind, &prov.model, &prov.session,
			&headRevi, &row.Title, &row.Class, decimalInto(&row.Confidence), &updated); err != nil {
			return row, err
		}
		row.CreatedAt = created.UTC()
		row.UpdatedAt = updated.UTC()
		row.HeadRevisionID = ledger.RevisionID(headRevi)
		row.Provenance = prov.value()
		return row, nil
	})
}

// Revisions is the full lineage, oldest first: a revision list is read as a
// history, not as a feed.
func (s *snapshot) Revisions(id ledger.InsightID) ([]ledger.RevisionRow, error) {
	return scanRows(s, `SELECT `+revisionColumns+` FROM insight_revisions
		WHERE insight_id = ? ORDER BY revision`, []any{string(id)}, scanRevision)
}

func (s *snapshot) References(q ledger.ReferenceQuery) ([]ledger.ReferenceRow, error) {
	w := newWhere()
	w.inStrings("f.id", strs(q.IDs))
	w.inStrings("f.kind", q.Kinds)
	join := ""
	if q.Target != nil || len(q.Relations) > 0 {
		join = " JOIN ref_links l ON l.reference_id = f.id"
		if q.Target != nil {
			w.eq("l.record_kind", string(q.Target.Kind))
			w.eq("l.record_id", q.Target.ID)
		}
		w.inStrings("l.relation", strs(q.Relations))
	}
	query := `SELECT DISTINCT f.id, f.kind, f.locator, f.workspace, f.label, f.meta,
		f.fetched_at, f.created_at FROM refs f` + join + w.clause() +
		` ORDER BY f.created_at DESC, f.id DESC` + limitOffset(q.Limit, 0)

	return scanRows(s, query, w.args, func(rows *sql.Rows) (ledger.Reference, error) {
		var (
			r       ledger.Reference
			label   sql.NullString
			meta    sql.NullString
			fetched sql.NullTime
			created time.Time
		)
		if err := rows.Scan(&r.ID, &r.Kind, &r.Locator, &r.Workspace, &label, &meta, &fetched, &created); err != nil {
			return r, err
		}
		r.Label = label.String
		if meta.Valid {
			r.Meta = []byte(meta.String)
		}
		r.FetchedAt = fetched.Time.UTC()
		if !fetched.Valid {
			r.FetchedAt = time.Time{}
		}
		r.CreatedAt = created.UTC()
		return r, nil
	})
}

func (s *snapshot) ReferenceLinks(rec ledger.RecordRef) ([]ledger.ReferenceLinkRow, error) {
	return scanRows(s, `SELECT record_kind, record_id, reference_id, relation, created_at
		FROM ref_links WHERE record_kind = ? AND record_id = ?
		ORDER BY created_at, reference_id`, []any{string(rec.Kind), rec.ID},
		func(rows *sql.Rows) (ledger.ReferenceLink, error) {
			var (
				l       ledger.ReferenceLink
				kind    string
				rel     string
				created time.Time
			)
			if err := rows.Scan(&kind, &l.Record.ID, &l.ReferenceID, &rel, &created); err != nil {
				return l, err
			}
			l.Record.Kind = ledger.RecordKind(kind)
			l.Relation = ledger.Relation(rel)
			l.CreatedAt = created.UTC()
			return l, nil
		})
}

const proposalColumns = `id, insight_id, revision_id, class, dest_kind, dest_locator,
	dest_workspace, dest_capabilities, content, content_hash, confidence,
	requested_authority, supersedes_proposal_id, policy_version, redaction_version,
	created_at, actor_id, actor_kind, actor_model, session_id`

// Proposals filters Statuses on the latest attempt, defaulting to 'proposed'
// when no attempt exists — a Proposal with no attempt is exactly what "proposed"
// means.
func (s *snapshot) Proposals(q ledger.PromotionQuery) ([]ledger.ProposalRow, error) {
	w := newWhere()
	w.inStrings("p.id", strs(q.IDs))
	w.eq("p.insight_id", string(q.InsightID))
	w.eq("p.content_hash", q.ContentHash)
	w.inStrings("p.dest_kind", q.DestKinds)
	if len(q.Statuses) > 0 {
		w.inStrings(`COALESCE((SELECT CAST(m.status AS CHAR) FROM promotions m
			WHERE m.proposal_id = p.id ORDER BY m.attempt DESC LIMIT 1), 'proposed')`, strs(q.Statuses))
	}
	query := `SELECT ` + prefixed("p", proposalColumns) + ` FROM promotion_proposals p` +
		w.clause() + ` ORDER BY p.created_at DESC, p.id DESC` + limitOffset(q.Limit, 0)

	return scanRows(s, query, w.args, func(rows *sql.Rows) (ledger.Proposal, error) {
		var (
			p          ledger.Proposal
			caps       string
			authority  string
			supersedes sql.NullString
			created    time.Time
			prov       provScan
		)
		if err := rows.Scan(&p.ID, &p.InsightID, &p.RevisionID, &p.Class, &p.DestKind,
			&p.DestLocator, &p.DestWorkspace, &caps, &p.Content, &p.ContentHash,
			decimalInto(&p.Confidence), &authority, &supersedes, &p.PolicyVersion,
			&p.RedactionVersion, &created,
			&prov.id, &prov.kind, &prov.model, &prov.session); err != nil {
			return p, err
		}
		var err error
		if p.Capabilities, err = ledger.DecodeCapabilities(caps); err != nil {
			return p, err
		}
		p.RequestedAuthority = ledger.AuthorityLevel(authority)
		p.SupersedesProposalID = ledger.ProposalID(supersedes.String)
		p.CreatedAt = created.UTC()
		p.Provenance = prov.value()
		return p, nil
	})
}

// Attempts returns every attempt for the given Proposals and every receipt those
// attempts produced, in one call: an attempt without its receipt is never what a
// caller wanted, and two round trips would let them disagree.
func (s *snapshot) Attempts(ids []ledger.ProposalID) ([]ledger.PromotionRow, []ledger.ReceiptRow, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = string(id)
	}
	in := placeholders(len(ids))

	promotions, err := scanRows(s, `SELECT id, proposal_id, attempt, status, detail, occurred_at,
		actor_id, actor_kind, actor_model, session_id FROM promotions
		WHERE proposal_id IN (`+in+`) ORDER BY proposal_id, attempt`, args,
		func(rows *sql.Rows) (ledger.Promotion, error) {
			var (
				p        ledger.Promotion
				status   string
				detail   sql.NullString
				occurred time.Time
				prov     provScan
			)
			if err := rows.Scan(&p.ID, &p.ProposalID, &p.Attempt, &status, &detail, &occurred,
				&prov.id, &prov.kind, &prov.model, &prov.session); err != nil {
				return p, err
			}
			p.Status = ledger.PromotionStatus(status)
			p.Detail = detail.String
			p.OccurredAt = occurred.UTC()
			p.Provenance = prov.value()
			return p, nil
		})
	if err != nil {
		return nil, nil, err
	}

	receipts, err := scanRows(s, `SELECT c.id, c.promotion_id, c.kind, c.locator, c.anchor,
		c.external_hash, c.verified, c.reference_id, c.recorded_at,
		c.actor_id, c.actor_kind, c.actor_model, c.session_id
		FROM receipts c JOIN promotions m ON m.id = c.promotion_id
		WHERE m.proposal_id IN (`+in+`) ORDER BY c.recorded_at, c.id`, args,
		func(rows *sql.Rows) (ledger.Receipt, error) {
			var (
				r        ledger.Receipt
				anchor   sql.NullString
				extHash  sql.NullString
				verified int
				ref      sql.NullString
				recorded time.Time
				prov     provScan
			)
			if err := rows.Scan(&r.ID, &r.PromotionID, &r.Kind, &r.Locator, &anchor, &extHash,
				&verified, &ref, &recorded,
				&prov.id, &prov.kind, &prov.model, &prov.session); err != nil {
				return r, err
			}
			r.Anchor, r.ExternalHash = anchor.String, extHash.String
			r.Verified = verified != 0
			r.ReferenceID = ledger.ReferenceID(ref.String)
			r.RecordedAt = recorded.UTC()
			r.Provenance = prov.value()
			return r, nil
		})
	return promotions, receipts, err
}

// Events interleaves the three append-only histories oldest first, because every
// narrative read wants a timeline rather than three lists to merge itself.
func (s *snapshot) Events(q ledger.EventQuery) ([]ledger.EventRow, error) {
	// scope and the destination columns exist only on authorities; the other two
	// branches select NULL so the union keeps one shape. They are carried
	// because the promotion gate reads a grant's narrowing through this row.
	union := `SELECT 'review' AS kind, e.id AS id, 'crumb' AS target_kind, e.crumb_id AS target_id,
			CAST(e.to_state AS CHAR) AS summary, NULL AS scope,
			NULL AS destination_kind, NULL AS destination_locator,
			e.rationale AS rationale, e.occurred_at AS occurred_at,
			e.actor_id AS actor_id, CAST(e.actor_kind AS CHAR) AS actor_kind,
			e.actor_model AS actor_model, e.session_id AS session_id
		FROM crumb_review_events e
		UNION ALL
		SELECT 'validation', v.id, CAST(v.target_kind AS CHAR), v.target_id,
			CAST(v.verdict AS CHAR), NULL, NULL, NULL,
			v.rationale, v.occurred_at,
			v.actor_id, CAST(v.actor_kind AS CHAR), v.actor_model, v.session_id
		FROM validations v
		UNION ALL
		SELECT 'authority', a.id, CAST(a.target_kind AS CHAR), a.target_id,
			CAST(a.level AS CHAR), a.scope, a.destination_kind, a.destination_locator,
			a.rationale, a.occurred_at,
			a.actor_id, CAST(a.actor_kind AS CHAR), a.actor_model, a.session_id
		FROM authorities a`

	w := newWhere()
	w.gte("occurred_at", q.Since)
	if len(q.Targets) > 0 {
		var ors []string
		for _, t := range q.Targets {
			ors = append(ors, "(target_kind = ? AND target_id = ?)")
			w.args = append(w.args, string(t.Kind), t.ID)
		}
		w.conds = append(w.conds, "("+strings.Join(ors, " OR ")+")")
	}
	query := `SELECT kind, id, target_kind, target_id, summary, scope,
		destination_kind, destination_locator, rationale, occurred_at,
		actor_id, actor_kind, actor_model, session_id FROM (` + union + `) e` +
		w.clause() + ` ORDER BY occurred_at, id` + limitOffset(q.Limit, 0)

	return scanRows(s, query, w.args, func(rows *sql.Rows) (ledger.EventRow, error) {
		var (
			e           ledger.EventRow
			kind        string
			target      string
			scope       sql.NullString
			destKind    sql.NullString
			destLocator sql.NullString
			occurred    time.Time
			prov        provScan
		)
		if err := rows.Scan(&kind, &e.ID, &target, &e.Target.ID, &e.Summary,
			&scope, &destKind, &destLocator, &e.Rationale, &occurred,
			&prov.id, &prov.kind, &prov.model, &prov.session); err != nil {
			return e, err
		}
		e.Kind = ledger.EventKind(kind)
		e.Target.Kind = ledger.RecordKind(target)
		e.Scope, e.DestinationKind, e.DestinationLocator = scope.String, destKind.String, destLocator.String
		e.OccurredAt = occurred.UTC()
		e.Provenance = prov.value()
		return e, nil
	})
}

// OrphanTargets scans all three polymorphic target columns. Only
// ref_links.record_id can dangle today — authorities target revisions and
// proposals, neither of which is ever deleted — and the scan covers all three
// anyway, so adding a target kind does not silently skip a check.
func (s *snapshot) OrphanTargets() ([]ledger.OrphanRow, error) {
	scans := []struct {
		table, column, kindExpr, idExpr, from string
		kinds                                 map[ledger.RecordKind]string
	}{
		{
			table: "ref_links", column: "record_id",
			kindExpr: "CAST(x.record_kind AS CHAR)", idExpr: "x.record_id", from: "ref_links x",
			kinds: map[ledger.RecordKind]string{
				ledger.KindCrumb:      "crumbs",
				ledger.KindRevision:   "insight_revisions",
				ledger.KindProposal:   "promotion_proposals",
				ledger.KindValidation: "validations",
			},
		},
		{
			table: "validations", column: "target_id",
			kindExpr: "CAST(x.target_kind AS CHAR)", idExpr: "x.target_id", from: "validations x",
			kinds: map[ledger.RecordKind]string{
				ledger.KindCrumb:    "crumbs",
				ledger.KindRevision: "insight_revisions",
				ledger.KindProposal: "promotion_proposals",
			},
		},
		{
			table: "authorities", column: "target_id",
			kindExpr: "CAST(x.target_kind AS CHAR)", idExpr: "x.target_id", from: "authorities x",
			kinds: map[ledger.RecordKind]string{
				ledger.KindRevision: "insight_revisions",
				ledger.KindProposal: "promotion_proposals",
			},
		},
	}

	var out []ledger.OrphanRow
	for _, sc := range scans {
		for kind, table := range sc.kinds {
			query := fmt.Sprintf(`SELECT %s, %s FROM %s
				WHERE %s = ? AND NOT EXISTS (SELECT 1 FROM %s t WHERE t.id = %s)`,
				sc.kindExpr, sc.idExpr, sc.from, sc.kindExpr, table, sc.idExpr)
			rows, err := scanRows(s, query, []any{string(kind)}, func(rows *sql.Rows) (ledger.OrphanRow, error) {
				o := ledger.OrphanRow{Table: sc.table, Column: sc.column}
				var k string
				if err := rows.Scan(&k, &o.RecordID); err != nil {
					return o, err
				}
				o.RecordKind = ledger.RecordKind(k)
				return o, nil
			})
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
	}
	return out, nil
}

// HeadRevisionDrift is the check behind "insights.head_revision equals
// MAX(insight_revisions.revision)". The head is a cache, and an unverified cache
// is a lie waiting to happen.
func (s *snapshot) HeadRevisionDrift() ([]ledger.HeadDriftRow, error) {
	return scanRows(s, `SELECT i.id, i.head_revision, COALESCE(MAX(r.revision), 0) AS max_revision
		FROM insights i LEFT JOIN insight_revisions r ON r.insight_id = i.id
		GROUP BY i.id, i.head_revision
		HAVING i.head_revision <> COALESCE(MAX(r.revision), 0)
		ORDER BY i.id`, nil, func(rows *sql.Rows) (ledger.HeadDriftRow, error) {
		var d ledger.HeadDriftRow
		return d, rows.Scan(&d.InsightID, &d.Head, &d.MaxRevision)
	})
}

func (s *snapshot) Counts(q ledger.CountQuery) (ledger.Counts, error) {
	c := ledger.Counts{
		CrumbsByState:      map[ledger.ReviewState]int{},
		PromotionsByStatus: map[ledger.PromotionStatus]int{},
	}
	since := newWhere()
	since.gte("captured_at", q.Since)
	crumbs, err := scanRows(s, `SELECT CAST(review_state AS CHAR), COUNT(*) FROM crumbs`+
		since.clause()+` GROUP BY review_state`, since.args,
		func(rows *sql.Rows) ([2]string, error) {
			var pair [2]string
			return pair, rows.Scan(&pair[0], &pair[1])
		})
	if err != nil {
		return c, err
	}
	for _, pair := range crumbs {
		n, _ := strconv.Atoi(pair[1])
		c.CrumbsByState[ledger.ReviewState(pair[0])] = n
	}

	promotions, err := scanRows(s, `SELECT CAST(status AS CHAR), COUNT(*) FROM promotions GROUP BY status`,
		nil, func(rows *sql.Rows) ([2]string, error) {
			var pair [2]string
			return pair, rows.Scan(&pair[0], &pair[1])
		})
	if err != nil {
		return c, err
	}
	for _, pair := range promotions {
		n, _ := strconv.Atoi(pair[1])
		c.PromotionsByStatus[ledger.PromotionStatus(pair[0])] = n
	}

	for _, t := range []struct {
		table string
		into  *int
	}{
		{"insights", &c.Insights},
		{"insight_revisions", &c.Revisions},
		{"harvests", &c.Harvests},
		{"refs", &c.References},
		{"promotion_proposals", &c.Proposals},
	} {
		if err := s.row(`SELECT COUNT(*) FROM ` + t.table).Scan(t.into); err != nil {
			return c, storageErr(err, "cannot count %s", t.table)
		}
	}
	return c, nil
}

func (s *snapshot) Config() (map[string]string, error) {
	pairs, err := scanRows(s, `SELECT k, v FROM repo_config`, nil, func(rows *sql.Rows) ([2]string, error) {
		var pair [2]string
		return pair, rows.Scan(&pair[0], &pair[1])
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		out[pair[0]] = pair[1]
	}
	return out, nil
}

// where accumulates conditions and their arguments together, which is what keeps
// a filter and its placeholder from ever drifting apart.
type where struct {
	conds []string
	args  []any
}

func newWhere() *where { return &where{} }

func (w *where) eq(column, value string) {
	if value == "" {
		return
	}
	w.conds = append(w.conds, column+" = ?")
	w.args = append(w.args, value)
}

func (w *where) gte(column string, t time.Time) {
	if t.IsZero() {
		return
	}
	w.conds = append(w.conds, column+" >= ?")
	w.args = append(w.args, utc(t))
}

func (w *where) lt(column string, t time.Time) {
	if t.IsZero() {
		return
	}
	w.conds = append(w.conds, column+" < ?")
	w.args = append(w.args, utc(t))
}

func (w *where) inStrings(expr string, values []string) {
	if len(values) == 0 {
		return
	}
	w.conds = append(w.conds, expr+" IN ("+placeholders(len(values))+")")
	for _, v := range values {
		w.args = append(w.args, v)
	}
}

func (w *where) clause() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}

func limitOffset(limit, offset int) string {
	switch {
	case limit <= 0 && offset <= 0:
		return ""
	case limit <= 0:
		// MySQL has no OFFSET without LIMIT; the ceiling is effectively "all".
		return fmt.Sprintf(" LIMIT %d OFFSET %d", 1<<31-1, offset)
	case offset <= 0:
		return fmt.Sprintf(" LIMIT %d", limit)
	default:
		return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// prefixed qualifies a bare column list with a table alias, so a column list is
// written once and reused in the joined queries that need an alias.
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// strs widens any of the id and vocabulary types to the plain strings a
// placeholder list needs.
func strs[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

// provScan holds the four provenance columns every record and event table
// carries, so a scan cannot pick up three of them.
type provScan struct {
	id      string
	kind    string
	model   sql.NullString
	session sql.NullString
}

func (p provScan) value() ledger.Provenance {
	return ledger.Provenance{
		ActorID:    p.id,
		ActorKind:  ledger.ActorKind(p.kind),
		ActorModel: p.model.String,
		SessionID:  p.session.String,
	}
}

func provArgs(p ledger.Provenance) []any {
	return []any{p.ActorID, string(p.ActorKind), nullStr(p.ActorModel), nullStr(p.SessionID)}
}

// utc normalises to DATETIME(6)'s precision. A Go time carries nanoseconds, so
// without truncation a value read back never equals the one written.
func utc(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return utc(t)
}

// decimalInto scans a DECIMAL column into a float64. The driver may hand back a
// float, an integer, text, or a decimal value with only a String method, and a
// plain *float64 destination fails on the last two.
func decimalInto(dst *float64) sql.Scanner { return decimalScanner{dst} }

type decimalScanner struct{ dst *float64 }

func (d decimalScanner) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d.dst = 0
		return nil
	case float64:
		*d.dst = v
		return nil
	case float32:
		*d.dst = float64(v)
		return nil
	case int64:
		*d.dst = float64(v)
		return nil
	case []byte:
		return d.parse(string(v))
	case string:
		return d.parse(v)
	case fmt.Stringer:
		return d.parse(v.String())
	default:
		return fmt.Errorf("cannot read a decimal from %T", src)
	}
}

func (d decimalScanner) parse(s string) error {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return fmt.Errorf("cannot read a decimal from %q: %w", s, err)
	}
	*d.dst = f
	return nil
}

// translate maps the storage errors that mean something specific to a caller.
// Everything else stays a storage error carrying the engine's own text: guessing
// at a meaning we have not verified would be worse than reporting the truth.
func translate(err error, query string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		return ledger.FailWith(ledger.ErrInvalidInput, "invalid_duplicate", err,
			"a record with that identity already exists")
	}
	return storageErr(err, "%s", firstLine(query))
}
