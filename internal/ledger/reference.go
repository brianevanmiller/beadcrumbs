package ledger

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// The tracker-neutral reference graph.
//
// A Reference is an opaque locator in an adapter namespace, attached to a record
// under one semantic relation. That pair is the whole seam:
//
//   - Identity is (kind, locator, workspace) and is unique in the database.
//     Core never parses, validates, or interprets a locator — no bead ids, no
//     Linear keys, no GitHub URL fields, no Project OS paths exist anywhere in
//     this package, and adding a tracker adds no column and no branch here.
//   - Label, meta, and fetched_at are an observed cache. Nothing joins them for
//     correctness and every read carries explicit freshness, so a caller can
//     always tell what the ledger recorded from what an adapter last saw.
//   - With no enricher for a kind, a Reference degrades to its stored locator.
//     That is the normal v1 case, not a failure: v1 ships no adapters.
//
// ref_links.record_id is polymorphic and therefore carries no foreign key. The
// referential rule is enforced here, on the write, and audited by the orphan
// scan in `bdc doctor` — the one invariant the database cannot hold.

// targetPrefixes maps a record kind to the id prefix that names it. Ids are
// kind-prefixed, so an attachment target needs no second flag that a caller
// could contradict.
var targetPrefixes = map[RecordKind]string{
	KindCrumb:      PrefixCrumb,
	KindRevision:   PrefixRevision,
	KindProposal:   PrefixProposal,
	KindValidation: PrefixValidation,
}

// TargetRef resolves a record id to the record it names, for the four kinds
// ref_links admits.
func TargetRef(id string) (RecordRef, error) {
	id = strings.TrimSpace(id)
	for kind, prefix := range targetPrefixes {
		if strings.HasPrefix(id, prefix) {
			parsed, err := ParseID(prefix, id)
			if err != nil {
				return RecordRef{}, err
			}
			return RecordRef{Kind: kind, ID: parsed}, nil
		}
	}
	return RecordRef{}, Fail(ErrInvalidInput, "invalid_id",
		"%q is not a record a Reference attaches to; expected a Crumb (%s), Insight revision (%s), "+
			"promotion proposal (%s), or validation (%s) id",
		id, PrefixCrumb, PrefixRevision, PrefixProposal, PrefixValidation)
}

// AttachReference is `bdc reference add`. Ref is the same `kind:locator[@relation]`
// shape `bdc capture --ref` accepts, so one parse and one validation serve both.
type AttachReference struct {
	Target RecordRef
	Ref    RefSpec

	// Label is a caller-supplied display string. It fills the cache's label
	// without marking it fetched: nothing looked the record up, so the
	// freshness of a labelled-but-unenriched Reference is still "never".
	Label string
}

// AttachResult is the `{reference, link}` the CLI contract promises. The
// Reference may predate this call — identity is (kind, locator, workspace) and
// attaching an already-known locator resolves to it rather than minting a
// second one — so Created says which happened.
//
// Reference is a ReferenceView rather than a bare Reference so that every
// Reference crossing the CLI boundary carries its freshness. A cache with no
// freshness beside it is indistinguishable from a fact.
type AttachResult struct {
	Reference ReferenceView `json:"reference"`
	Link      ReferenceLink `json:"link"`
	Created   bool          `json:"-"`
	Findings  []Finding     `json:"-"`
}

// AttachReference resolves the Reference and links it to the target under one
// relation. Both halves are idempotent: the same locator attached twice under
// the same relation is one fact stated twice.
//
// refs and ref_links carry no provenance columns, so an attachment records no
// actor. What is attached to what is a property of the records, and the records
// carry their own.
func (l *Ledger) AttachReference(ctx context.Context, c AttachReference) (AttachResult, error) {
	if err := validateTarget(c.Target); err != nil {
		return AttachResult{}, err
	}
	if err := c.Ref.Validate(); err != nil {
		return AttachResult{}, err
	}
	// Identity columns reject rather than redact: rewriting a locator would
	// silently change which record it names, producing a Reference that resolves
	// to nothing while looking valid.
	if err := l.rejectSecrets("reference locator", c.Ref.Locator); err != nil {
		return AttachResult{}, err
	}
	label, findings, err := l.redactField("reference label", strings.TrimSpace(c.Label))
	if err != nil {
		return AttachResult{}, err
	}
	if len(label) > 512 {
		return AttachResult{}, Fail(ErrInvalidInput, "invalid_reference",
			"reference label is longer than 512 characters")
	}
	l.assertRedacted("refs.label", label)

	at := l.clock()
	out := AttachResult{Findings: findings}
	err = l.store.Write(ctx, func(tx Tx) error {
		// The ledger's half of the polymorphic foreign key. Checked inside the
		// transaction that writes the link, because a target that exists at
		// validation time and not at write time is exactly the orphan this is
		// here to prevent.
		if err := assertTargetExists(tx, c.Target); err != nil {
			return err
		}
		minted := NewReferenceID()
		id, err := tx.UpsertReference(Reference{
			ID: minted, Kind: c.Ref.Kind, Locator: c.Ref.Locator,
			Workspace: c.Ref.Workspace, Label: label, CreatedAt: at,
		})
		if err != nil {
			return err
		}
		out.Created = id == minted
		if err := tx.LinkReference(c.Target, id, c.Ref.Relation); err != nil {
			return err
		}
		// Read both back rather than assembling them: the stored Reference may
		// be older than this call, and the link's timestamp is the store's.
		refs, err := tx.References(ReferenceQuery{IDs: []ReferenceID{id}})
		if err != nil {
			return err
		}
		if len(refs) != 1 {
			return Fail(ErrIntegrity, "integrity_missing_reference",
				"reference %s was written and cannot be read back", id)
		}
		out.Reference = l.view(refs[0], c.Ref.Relation, nil)

		links, err := tx.ReferenceLinks(c.Target)
		if err != nil {
			return err
		}
		for _, link := range links {
			if link.ReferenceID == id && link.Relation == c.Ref.Relation {
				out.Link = link
				return nil
			}
		}
		return Fail(ErrIntegrity, "integrity_missing_link",
			"reference %s was linked to %s and the link cannot be read back", id, c.Target)
	})
	if err != nil {
		return AttachResult{}, err
	}
	return out, nil
}

// ReferenceOptions is the read-time behavior `bdc reference list` selects.
// Refresh is separate from the query because it is not a filter: it decides
// whether the cache is re-observed, not which References are returned.
type ReferenceOptions struct {
	Refresh bool
}

// FreshnessState is how a Reference's observed cache stands right now.
type FreshnessState string

const (
	// FreshnessNever means nothing ever enriched this Reference. It resolves to
	// its locator, which is the v1 default because v1 ships no adapters.
	FreshnessNever FreshnessState = "never"
	// FreshnessCached means an earlier run observed it. The label and meta are
	// as old as FetchedAt says and may contradict the tracker.
	FreshnessCached FreshnessState = "cached"
	// FreshnessLive means this command observed it.
	FreshnessLive FreshnessState = "live"
)

// Freshness is the explicit statement every Reference read carries. A cache
// with no freshness is indistinguishable from a fact, which is the one thing
// this cache must never look like.
type Freshness struct {
	State      FreshnessState `json:"state"`
	FetchedAt  time.Time      `json:"fetched_at,omitempty"`
	AgeSeconds float64        `json:"age_seconds,omitempty"`

	// Enricher is the adapter kind that can refresh this Reference, empty when
	// none is configured for its kind. Dispatch is by kind string; there is no
	// adapter registry in v1.
	Enricher string `json:"enricher,omitempty"`

	// Error is set when a refresh was attempted and failed. Enrichment is
	// optional and never fails a read, so this travels as data and the CLI
	// renders it as a warning.
	Error string `json:"error,omitempty"`
}

// MarshalJSON omits a fetched_at that was never set, for the same reason
// ReferenceView does: encoding/json renders a zero time.Time as year 1, which
// reads as an observation that happened.
func (f Freshness) MarshalJSON() ([]byte, error) {
	type wire Freshness
	out := struct {
		wire
		FetchedAt *time.Time `json:"fetched_at,omitempty"`
	}{wire: wire(f)}
	if !f.FetchedAt.IsZero() {
		at := f.FetchedAt
		out.FetchedAt = &at
	}
	return json.Marshal(out)
}

// ReferenceView is a Reference as a reader sees it: identity, the relation it
// is attached under when the query named a target, a display string that is
// never empty, and the freshness of everything cached.
type ReferenceView struct {
	Reference
	Relation  Relation  `json:"relation,omitempty"`
	Display   string    `json:"display"`
	Freshness Freshness `json:"freshness"`
}

// MarshalJSON renders a view with the two corrections the domain struct cannot
// make on its own: a fetched_at that was never set is absent rather than
// year 1 — encoding/json does not omit a zero time.Time, and "0001-01-01" reads
// as a fetch that happened — and the observed metadata is the JSON document it
// is rather than the base64 a []byte would become.
func (v ReferenceView) MarshalJSON() ([]byte, error) {
	type wire ReferenceView // sheds this method, so Marshal below does not recurse
	out := struct {
		wire
		FetchedAt *time.Time      `json:"fetched_at,omitempty"`
		Meta      json.RawMessage `json:"meta,omitempty"`
	}{wire: wire(v)}
	if !v.FetchedAt.IsZero() {
		at := v.FetchedAt
		out.FetchedAt = &at
	}
	if json.Valid(v.Meta) {
		out.Meta = json.RawMessage(v.Meta)
	}
	return json.Marshal(out)
}

// References lists the reference graph. When the query names a target there is
// one view per attachment, because the relation is the point; otherwise there
// is one view per Reference.
//
// A refresh is three steps in order — read, enrich outside any transaction,
// write the cache back — because enrichment does external I/O and the engine
// holds an exclusive lock for the life of a transaction.
func (l *Ledger) References(ctx context.Context, q ReferenceQuery, o ReferenceOptions) ([]ReferenceView, error) {
	if q.Target != nil {
		if err := validateTarget(*q.Target); err != nil {
			return nil, err
		}
	}
	for _, rel := range q.Relations {
		if !slices.Contains(relations, rel) {
			return nil, Fail(ErrInvalidInput, "invalid_relation",
				"%q is not a reference relation; expected one of %s", rel, joinRelations())
		}
	}

	var (
		refs  []Reference
		links []ReferenceLink
	)
	if err := l.store.Read(ctx, func(snap Snapshot) error {
		var err error
		if refs, err = snap.References(q); err != nil {
			return err
		}
		if q.Target == nil {
			return nil
		}
		links, err = snap.ReferenceLinks(*q.Target)
		return err
	}); err != nil {
		return nil, err
	}

	observed := map[ReferenceID]observation{}
	if o.Refresh {
		observed = l.refresh(ctx, refs)
	}

	views := make([]ReferenceView, 0, len(refs))
	if q.Target == nil {
		for _, ref := range refs {
			views = append(views, l.view(ref, "", observed))
		}
		return views, nil
	}

	byID := make(map[ReferenceID]Reference, len(refs))
	for _, ref := range refs {
		byID[ref.ID] = ref
	}
	for _, link := range links {
		ref, ok := byID[link.ReferenceID]
		if !ok {
			// The Reference was filtered out by kind, or trimmed by the limit.
			continue
		}
		if len(q.Relations) > 0 && !slices.Contains(q.Relations, link.Relation) {
			continue
		}
		views = append(views, l.view(ref, link.Relation, observed))
	}
	return views, nil
}

// observation is one refresh attempt's outcome, keyed by Reference. On success
// it carries the Reference as this command observed it, because the listing was
// read before the enricher ran and would otherwise report the stale cache it
// just replaced.
type observation struct {
	ref Reference
	err string
}

// refresh re-observes each Reference whose kind the configured enricher serves,
// then writes the whole batch back in one transaction. A Reference with no
// enricher is skipped silently — degrading to the stored locator is the
// documented behavior, not a failure — and an enricher error is recorded
// against that Reference and never returned, because enrichment may not fail a
// read any more than it may fail a write.
func (l *Ledger) refresh(ctx context.Context, refs []Reference) map[ReferenceID]observation {
	out := map[ReferenceID]observation{}
	if l.enricher == nil {
		return out
	}
	kind := l.enricher.Kind()

	updates := make([]Reference, 0, len(refs))
	for _, ref := range refs {
		if ref.Kind != kind {
			continue
		}
		label, meta, fetchedAt, err := l.enricher.Enrich(ctx, ref.Locator, ref.Workspace)
		if err != nil {
			out[ref.ID] = observation{err: err.Error()}
			continue
		}
		// An observed label is rewritten silently: it is a cache nobody asked
		// for verbatim, and the findings would name a rule for text the caller
		// never wrote.
		clean, _, err := l.redactor.Redact(label)
		if err != nil {
			out[ref.ID] = observation{err: "the observed label could not be redacted, so it was not stored"}
			continue
		}
		cleanMeta, err := l.redactMeta(meta)
		if err != nil {
			out[ref.ID] = observation{err: err.Error()}
			continue
		}
		if fetchedAt.IsZero() {
			fetchedAt = l.clock()
		}
		fetchedAt = fetchedAt.UTC().Truncate(time.Microsecond)
		l.assertRedacted("refs.label", clean)
		observed := Reference{
			ID: ref.ID, Kind: ref.Kind, Locator: ref.Locator, Workspace: ref.Workspace,
			Label: clean, Meta: cleanMeta, FetchedAt: fetchedAt, CreatedAt: ref.CreatedAt,
		}
		updates = append(updates, observed)
		out[ref.ID] = observation{ref: observed}
	}
	if len(updates) == 0 {
		return out
	}
	if err := l.store.Write(ctx, func(tx Tx) error {
		for _, u := range updates {
			if _, err := tx.UpsertReference(u); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		// The observation still happened; only the cache write did not. Report
		// it per Reference rather than failing the listing.
		for _, u := range updates {
			out[u.ID] = observation{err: "the observed values could not be cached: " + err.Error()}
		}
	}
	return out
}

// redactMeta applies the Redact treatment to a JSON document's string leaves and
// keys. Enrichment comes from outside the ledger, so its metadata is untrusted
// text like any other; a document that cannot be parsed, or that still matches a
// rule after rewriting, is dropped rather than stored — a cache is never worth a
// secret in committed history.
func (l *Ledger) redactMeta(meta []byte) ([]byte, error) {
	if len(meta) == 0 {
		return nil, nil
	}
	var doc any
	if err := json.Unmarshal(meta, &doc); err != nil {
		return nil, Fail(ErrAdapter, "adapter_invalid_meta",
			"the enricher returned metadata that is not JSON, so it was not stored")
	}
	clean, err := l.redactTree(doc)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return nil, Fail(ErrAdapter, "adapter_invalid_meta",
			"the enricher's metadata could not be re-encoded, so it was not stored")
	}
	if _, findings, err := l.redactor.Redact(string(encoded)); err != nil || len(findings) > 0 {
		return nil, Fail(ErrRedaction, "redaction_failed",
			"the enricher's metadata still matches a redaction rule after rewriting, so it was not stored")
	}
	return encoded, nil
}

func (l *Ledger) redactTree(node any) (any, error) {
	switch v := node.(type) {
	case string:
		clean, _, err := l.redactor.Redact(v)
		return clean, err
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			clean, err := l.redactTree(item)
			if err != nil {
				return nil, err
			}
			out[i] = clean
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			cleanKey, _, err := l.redactor.Redact(key)
			if err != nil {
				return nil, err
			}
			clean, err := l.redactTree(item)
			if err != nil {
				return nil, err
			}
			out[cleanKey] = clean
		}
		return out, nil
	default:
		return node, nil
	}
}

// view pairs a Reference with its freshness and a display string that is never
// empty: with no label — the v1 default — a Reference renders as its locator.
func (l *Ledger) view(ref Reference, rel Relation, observed map[ReferenceID]observation) ReferenceView {
	obs, refreshed := observed[ref.ID]
	if refreshed && obs.err == "" {
		ref = obs.ref
	}
	v := ReferenceView{Reference: ref, Relation: rel, Display: ref.Label}
	if v.Display == "" {
		v.Display = ref.Locator
	}
	v.Freshness = Freshness{State: FreshnessNever}
	if l.enricher != nil && l.enricher.Kind() == ref.Kind {
		v.Freshness.Enricher = ref.Kind
	}
	switch {
	case refreshed && obs.err != "":
		v.Freshness.Error = obs.err
	case refreshed:
		v.Freshness.State, v.Freshness.FetchedAt = FreshnessLive, ref.FetchedAt
		return v
	}
	if !ref.FetchedAt.IsZero() {
		v.Freshness.State = FreshnessCached
		v.Freshness.FetchedAt = ref.FetchedAt
		v.Freshness.AgeSeconds = l.clock().Sub(ref.FetchedAt).Seconds()
	}
	return v
}

func validateTarget(ref RecordRef) error {
	prefix, ok := targetPrefixes[ref.Kind]
	if !ok {
		return Fail(ErrInvalidInput, "invalid_record_kind",
			"%q is not a record kind a Reference attaches to; expected %s, %s, %s, or %s",
			ref.Kind, KindCrumb, KindRevision, KindProposal, KindValidation)
	}
	_, err := ParseID(prefix, ref.ID)
	return err
}

// assertTargetExists is the substitute for the foreign key ref_links.record_id
// cannot have. The revision and validation lookups walk rather than seek: the
// storage port has no by-id read for either, and inventing one belongs to the
// slice that owns the port, not to this one.
func assertTargetExists(snap Snapshot, ref RecordRef) error {
	found, err := targetExists(snap, ref)
	if err != nil {
		return err
	}
	if !found {
		return NotFound(recordNoun(ref.Kind), ref.ID)
	}
	return nil
}

func targetExists(snap Snapshot, ref RecordRef) (bool, error) {
	switch ref.Kind {
	case KindCrumb:
		rows, err := snap.Crumbs(CrumbQuery{IDs: []CrumbID{CrumbID(ref.ID)}})
		return len(rows) > 0, err
	case KindProposal:
		rows, err := snap.Proposals(PromotionQuery{IDs: []ProposalID{ProposalID(ref.ID)}})
		return len(rows) > 0, err
	case KindRevision:
		insights, err := snap.Insights(InsightQuery{})
		if err != nil {
			return false, err
		}
		for _, insight := range insights {
			revisions, err := snap.Revisions(insight.ID)
			if err != nil {
				return false, err
			}
			for _, rev := range revisions {
				if string(rev.ID) == ref.ID {
					return true, nil
				}
			}
		}
		return false, nil
	case KindValidation:
		events, err := snap.Events(EventQuery{})
		if err != nil {
			return false, err
		}
		for _, e := range events {
			if e.Kind == EventValidation && e.ID == ref.ID {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, Fail(ErrInvalidInput, "invalid_record_kind",
			"%q is not a record kind a Reference attaches to", ref.Kind)
	}
}

func recordNoun(kind RecordKind) string {
	switch kind {
	case KindCrumb:
		return "crumb"
	case KindRevision:
		return "insight revision"
	case KindProposal:
		return "promotion proposal"
	case KindValidation:
		return "validation"
	default:
		return string(kind)
	}
}
