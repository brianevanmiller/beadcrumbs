package ledger_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
	"github.com/brianevanmiller/beadcrumbs/internal/redact"
)

// The reference graph is tracker-neutral by construction: identity is (kind,
// locator, workspace), the locator is never parsed, and the label and metadata
// are a cache that always reports its own freshness. These tests run against a
// real ledger because every one of those properties is held jointly by the
// ledger and the schema's uq_refs_identity, its binary collation, and the
// foreign key ref_links.record_id cannot have.

// A Reference is identified by what it names, not by who attached it. The same
// locator attached from two records is one Reference; a different workspace, or
// a locator that differs only in case, is a different one.
func TestReferenceIdentityIsKindLocatorWorkspace(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	first := f.capture("the driver fixes the current database at Connect", 0.8)
	second := f.capture("a USE issued afterwards does not survive", 0.6)

	a := f.attach(crumbTarget(first.ID), "beads:bdc-7ah@subject")
	b := f.attach(crumbTarget(second.ID), "beads:bdc-7ah@evidence")
	if a.Reference.ID != b.Reference.ID {
		t.Fatalf("the same locator from two records minted two References: %s and %s",
			a.Reference.ID, b.Reference.ID)
	}
	if !a.Created || b.Created {
		t.Fatalf("expected the first attach to create and the second to resolve, got %v and %v",
			a.Created, b.Created)
	}
	if a.Link.Relation == b.Link.Relation {
		t.Fatalf("both links recorded relation %q; the relation is per attachment, not per Reference",
			a.Link.Relation)
	}

	scoped, err := f.L.AttachReference(ctx, ledger.AttachReference{
		Target: crumbTarget(first.ID),
		Ref: ledger.RefSpec{Kind: "beads", Locator: "bdc-7ah", Workspace: "other-repo",
			Relation: ledger.RelationSubject},
	})
	if err != nil {
		t.Fatalf("attaching a workspace-scoped reference: %v", err)
	}
	if scoped.Reference.ID == a.Reference.ID {
		t.Fatal("a workspace-scoped locator collapsed onto the unscoped Reference")
	}

	// utf8mb4_0900_bin is what makes "the locator is opaque" true at the storage
	// layer: docs/Foo.md and docs/foo.md are two different files.
	upper := f.attach(crumbTarget(first.ID), "docs:docs/Foo.md@source")
	lower := f.attach(crumbTarget(first.ID), "docs:docs/foo.md@source")
	if upper.Reference.ID == lower.Reference.ID {
		t.Fatal("case-variant locators resolved to one Reference; identity is byte-exact")
	}
}

// ref_links covers four record kinds and every relation. None of them is a
// tracker: the same opaque locator attaches to a Crumb, a revision, a proposal,
// and a validation with no kind-specific field anywhere.
func TestReferenceAttachesToEveryRecordKind(t *testing.T) {
	f := newFixture(t)
	crumb := f.capture("promotion attempts are independent per destination", 0.7)
	insight, revision := seedRevision(f)
	proposal := seedProposal(f, insight, revision)
	validation := seedValidation(f, ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision)})

	cases := []struct {
		target   ledger.RecordRef
		relation ledger.Relation
	}{
		{crumbTarget(crumb.ID), ledger.RelationSource},
		{ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision)}, ledger.RelationSubject},
		{ledger.RecordRef{Kind: ledger.KindProposal, ID: string(proposal)}, ledger.RelationEvidence},
		{ledger.RecordRef{Kind: ledger.KindValidation, ID: string(validation)}, ledger.RelationSpawnedWork},
	}
	for _, tc := range cases {
		res := f.attach(tc.target, "beads:bdc-7ah.17@"+string(tc.relation))
		if res.Link.Record != tc.target {
			t.Fatalf("link recorded %s, expected %s", res.Link.Record, tc.target)
		}

		views := f.references(ledger.ReferenceQuery{Target: &tc.target}, ledger.ReferenceOptions{})
		if len(views) != 1 {
			t.Fatalf("%s: expected 1 attachment, got %d", tc.target.Kind, len(views))
		}
		if views[0].Relation != tc.relation {
			t.Fatalf("%s: relation surfaced as %q, expected %q", tc.target.Kind, views[0].Relation, tc.relation)
		}
		if views[0].Locator != "bdc-7ah.17" {
			t.Fatalf("%s: locator surfaced as %q; core never rewrites a locator",
				tc.target.Kind, views[0].Locator)
		}
	}

	// One Reference, four attachments: identity is the locator, not the record.
	if n := f.count(`SELECT COUNT(*) FROM refs`); n != 1 {
		t.Fatalf("expected 1 Reference for 4 attachments, got %d", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM ref_links`); n != 4 {
		t.Fatalf("expected 4 links, got %d", n)
	}
	if got := f.references(ledger.ReferenceQuery{Relations: []ledger.Relation{ledger.RelationEvidence}},
		ledger.ReferenceOptions{}); len(got) != 1 {
		t.Fatalf("expected the evidence filter to match 1 Reference, got %d", len(got))
	}
}

// ref_links.record_id is polymorphic and carries no foreign key, so the ledger
// is the only thing standing between a typo and an orphan. It refuses the write
// rather than recording a link to nothing.
func TestReferenceRefusesTargetThatDoesNotExist(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	for _, target := range []ledger.RecordRef{
		{Kind: ledger.KindCrumb, ID: string(ledger.NewCrumbID())},
		{Kind: ledger.KindRevision, ID: string(ledger.NewRevisionID())},
		{Kind: ledger.KindProposal, ID: string(ledger.NewProposalID())},
		{Kind: ledger.KindValidation, ID: string(ledger.NewValidationID())},
	} {
		_, err := f.L.AttachReference(ctx, ledger.AttachReference{
			Target: target,
			Ref:    ledger.RefSpec{Kind: "beads", Locator: "bdc-1", Relation: ledger.RelationSubject},
		})
		if !errors.Is(err, ledger.ErrNotFound) {
			t.Fatalf("%s: expected a not-found error, got %v", target.Kind, err)
		}
	}
	if n := f.count(`SELECT COUNT(*) FROM ref_links`); n != 0 {
		t.Fatalf("a refused attachment wrote %d link(s)", n)
	}

	// The doctor scan is the audit behind the same invariant; with the write
	// refused there is nothing for it to find.
	report, err := f.L.Doctor(ctx)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	for _, check := range report.Checks {
		if check.Name == "polymorphic_targets" && check.Status != ledger.StatusOK {
			t.Fatalf("orphan scan reported %s: %s", check.Status, check.Detail)
		}
	}

	// A target id from a kind ref_links does not admit is a usage error, named
	// as one, rather than a not-found for a record that was never eligible.
	if _, err := ledger.TargetRef(string(ledger.NewHarvestID())); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("expected a Harvest id to be an invalid target, got %v", err)
	}
}

// v1 ships no adapters. A Reference with no enricher is the normal case, not a
// degraded one: it resolves to the locator that was stored, and a refresh that
// finds no enricher is not an error.
func TestReferenceWithoutEnricherResolvesToItsLocator(t *testing.T) {
	f := newFixture(t)
	crumb := f.capture("references resolve to their locator with no adapter installed", 0.5)
	target := crumbTarget(crumb.ID)
	f.attach(target, "beads:bdc-7ah.17@subject")

	views := f.references(ledger.ReferenceQuery{Target: &target}, ledger.ReferenceOptions{Refresh: true})
	if len(views) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(views))
	}
	v := views[0]
	if v.Display != "bdc-7ah.17" {
		t.Fatalf("display is %q; with no label a Reference renders as its locator", v.Display)
	}
	if v.Freshness.State != ledger.FreshnessNever {
		t.Fatalf("freshness is %q, expected %q", v.Freshness.State, ledger.FreshnessNever)
	}
	if v.Freshness.Enricher != "" || v.Freshness.Error != "" {
		t.Fatalf("no enricher is installed, so freshness must name none and report no error: %+v", v.Freshness)
	}
	if !v.FetchedAt.IsZero() {
		t.Fatalf("nothing fetched this Reference, yet fetched_at is %s", v.FetchedAt)
	}
}

// The cache reports its own age. A reader has to be able to tell what this
// command observed from what some earlier command did, which is the whole
// reason label and meta are never authoritative.
func TestReferenceFreshnessSurfacesCacheState(t *testing.T) {
	f := newFixture(t)
	crumb := f.capture("enrichment is a cache and says so", 0.5)
	target := crumbTarget(crumb.ID)
	f.attach(target, "beads:bdc-7ah.17@subject")
	f.attach(target, "docs:docs/adr/0007.md@source")

	fetched := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	enricher := &fakeEnricher{kind: "beads", label: "Tracker-neutral references", at: fetched,
		meta: []byte(`{"status":"open"}`)}
	led := withEnricher(t, f, enricher)

	live := viewByKind(t, references(t, led, ledger.ReferenceQuery{Target: &target},
		ledger.ReferenceOptions{Refresh: true}), "beads")
	if live.Freshness.State != ledger.FreshnessLive {
		t.Fatalf("a just-observed Reference is %q, expected %q", live.Freshness.State, ledger.FreshnessLive)
	}
	if !live.Freshness.FetchedAt.Equal(fetched) {
		t.Fatalf("freshness reports %s, expected the observation time %s", live.Freshness.FetchedAt, fetched)
	}
	if live.Display != "Tracker-neutral references" {
		t.Fatalf("display is %q, expected the observed label", live.Display)
	}

	// A second kind has no enricher and is untouched by the refresh: dispatch is
	// by adapter kind string and there is no registry to fall through.
	untouched := viewByKind(t, references(t, led, ledger.ReferenceQuery{Target: &target},
		ledger.ReferenceOptions{Refresh: true}), "docs")
	if untouched.Freshness.State != ledger.FreshnessNever || untouched.Freshness.Enricher != "" {
		t.Fatalf("the docs Reference was touched by the beads enricher: %+v", untouched.Freshness)
	}
	if enricher.calls != 2 {
		t.Fatalf("the enricher ran %d times for two refreshes of one beads Reference", enricher.calls)
	}

	// Without a refresh the same read reports the stored observation as cached,
	// with its age, and never as live.
	cached := viewByKind(t, references(t, led, ledger.ReferenceQuery{Target: &target},
		ledger.ReferenceOptions{}), "beads")
	if cached.Freshness.State != ledger.FreshnessCached {
		t.Fatalf("a stored observation reads as %q, expected %q", cached.Freshness.State, ledger.FreshnessCached)
	}
	if cached.Freshness.AgeSeconds < 3500 {
		t.Fatalf("an hour-old observation reports an age of %.0fs", cached.Freshness.AgeSeconds)
	}
	if cached.Freshness.Enricher != "beads" {
		t.Fatalf("freshness names enricher %q, expected beads", cached.Freshness.Enricher)
	}
	// Dolt normalises a JSON column's whitespace, so the document is compared
	// decoded rather than byte-for-byte.
	var meta map[string]string
	if err := json.Unmarshal(cached.Meta, &meta); err != nil {
		t.Fatalf("cached metadata %q is not JSON: %v", cached.Meta, err)
	}
	if meta["status"] != "open" {
		t.Fatalf("cached metadata is %v", meta)
	}
}

// Enrichment is optional and may never fail a read. A failing adapter degrades
// one Reference to its locator and reports why, in data the CLI turns into a
// warning.
func TestReferenceEnrichFailureIsDataNotError(t *testing.T) {
	f := newFixture(t)
	crumb := f.capture("an adapter error is a warning, not a failure", 0.5)
	target := crumbTarget(crumb.ID)
	f.attach(target, "beads:bdc-7ah.17@subject")

	led := withEnricher(t, f, &fakeEnricher{kind: "beads", err: errors.New("bd exited 1")})
	views := references(t, led, ledger.ReferenceQuery{Target: &target}, ledger.ReferenceOptions{Refresh: true})
	if len(views) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(views))
	}
	if views[0].Freshness.Error == "" {
		t.Fatal("a failed enrichment reported nothing")
	}
	if views[0].Freshness.State != ledger.FreshnessNever {
		t.Fatalf("a failed enrichment left the state at %q", views[0].Freshness.State)
	}
	if views[0].Display != "bdc-7ah.17" {
		t.Fatalf("display is %q; a failed enrichment falls back to the locator", views[0].Display)
	}
}

// Enrichment arrives from outside the ledger, so its metadata is untrusted text
// like any other. A secret in it is never cached, and Dolt's committed history
// is why "never" has to mean never.
func TestEnrichedMetadataWithASecretIsNotCached(t *testing.T) {
	f := newFixture(t)
	crumb := f.capture("observed metadata passes redaction like any other text", 0.5)
	target := crumbTarget(crumb.ID)
	f.attach(target, "beads:bdc-7ah.17@subject")

	led := withEnricher(t, f, &fakeEnricher{kind: "beads", label: "a ticket",
		meta: []byte(`{"note":"deploy key AKIAIOSFODNN7EXAMPLE","status":"open"}`)})
	views := references(t, led, ledger.ReferenceQuery{Target: &target}, ledger.ReferenceOptions{Refresh: true})
	if len(views) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(views))
	}
	if strings.Contains(string(views[0].Meta), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("the observed secret was cached: %s", views[0].Meta)
	}
	if !strings.Contains(string(views[0].Meta), "open") {
		t.Fatalf("redaction dropped the rest of the metadata: %s", views[0].Meta)
	}
	var stored string
	if err := f.Store.DB().QueryRowContext(context.Background(),
		`SELECT COALESCE(meta, '') FROM refs WHERE id = ?`, string(views[0].ID)).Scan(&stored); err != nil {
		t.Fatalf("reading the stored cache: %v", err)
	}
	if strings.Contains(stored, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("the secret reached the refs table: %s", stored)
	}
}

// Helpers.

func crumbTarget(id ledger.CrumbID) ledger.RecordRef {
	return ledger.RecordRef{Kind: ledger.KindCrumb, ID: string(id)}
}

func (f *fixture) attach(target ledger.RecordRef, spec string) ledger.AttachResult {
	f.t.Helper()
	parsed, err := ledger.ParseRefSpec(spec, ledger.RelationSubject)
	if err != nil {
		f.t.Fatalf("parsing %q: %v", spec, err)
	}
	res, err := f.L.AttachReference(context.Background(), ledger.AttachReference{Target: target, Ref: parsed})
	if err != nil {
		f.t.Fatalf("attaching %q to %s: %v", spec, target, err)
	}
	return res
}

func (f *fixture) references(q ledger.ReferenceQuery, o ledger.ReferenceOptions) []ledger.ReferenceView {
	f.t.Helper()
	return references(f.t, f.L, q, o)
}

func references(t *testing.T, l *ledger.Ledger, q ledger.ReferenceQuery, o ledger.ReferenceOptions) []ledger.ReferenceView {
	t.Helper()
	views, err := l.References(context.Background(), q, o)
	if err != nil {
		t.Fatalf("listing references: %v", err)
	}
	return views
}

func viewByKind(t *testing.T, views []ledger.ReferenceView, kind string) ledger.ReferenceView {
	t.Helper()
	for _, v := range views {
		if v.Kind == kind {
			return v
		}
	}
	t.Fatalf("no %s reference in %d view(s)", kind, len(views))
	return ledger.ReferenceView{}
}

// withEnricher rebuilds the fixture's Ledger with an enricher injected. The
// fixture has none because v1 installs none; enrichment is what an adapter adds.
func withEnricher(t *testing.T, f *fixture, e ledger.Enricher) *ledger.Ledger {
	t.Helper()
	cfg, err := ledger.LoadRepoConfig(context.Background(), f.Store)
	if err != nil {
		t.Fatalf("reading repo_config: %v", err)
	}
	r, err := redact.New(redact.Config{Version: cfg.RedactionVersion, Patterns: cfg.RedactPatterns})
	if err != nil {
		t.Fatalf("building the redactor: %v", err)
	}
	return ledger.New(f.Store, ledger.Options{Actor: f.Actor, Redactor: r, Enricher: e, Config: cfg})
}

// fakeEnricher stands in for an adapter. v1 ships none, so the only way to reach
// the enrichment paths is to inject one.
type fakeEnricher struct {
	kind  string
	label string
	meta  []byte
	at    time.Time
	err   error
	calls int
}

func (e *fakeEnricher) Kind() string { return e.kind }

func (e *fakeEnricher) Enrich(_ context.Context, _, _ string) (string, []byte, time.Time, error) {
	e.calls++
	if e.err != nil {
		return "", nil, time.Time{}, e.err
	}
	return e.label, e.meta, e.at, nil
}

// seedRevision writes one Insight revision. Harvest synthesis lands in a later
// slice, and what is under test here is attachment, not how the revision was
// written.
func seedRevision(f *fixture) (ledger.InsightID, ledger.RevisionID) {
	f.t.Helper()
	insight, revision := ledger.NewInsightID(), ledger.NewRevisionID()
	f.write(func(tx ledger.Tx) error {
		return tx.InsertRevision(ledger.InsightRevision{
			ID: revision, InsightID: insight, Revision: 1,
			Title: "references are tracker-neutral", Content: "body",
			ContentHash: strings.Repeat("c", 64), Class: "learning", Confidence: 0.6,
			CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Provenance: f.Actor,
		}, nil)
	})
	return insight, revision
}

func seedProposal(f *fixture, insight ledger.InsightID, revision ledger.RevisionID) ledger.ProposalID {
	f.t.Helper()
	id := ledger.NewProposalID()
	f.write(func(tx ledger.Tx) error {
		_, _, err := tx.UpsertProposal(ledger.Proposal{
			ID: id, InsightID: insight, RevisionID: revision, Class: "adr",
			DestKind: "docs", DestLocator: "docs/adr/", Content: "rendered",
			ContentHash: strings.Repeat("d", 64), Confidence: 0.6,
			RequestedAuthority: ledger.AuthorityAdvisory, PolicyVersion: "1", RedactionVersion: "1",
			CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Provenance: f.Actor,
		})
		return err
	})
	return id
}

func seedValidation(f *fixture, target ledger.RecordRef) ledger.ValidationID {
	f.t.Helper()
	id := ledger.NewValidationID()
	f.write(func(tx ledger.Tx) error {
		return tx.AppendValidation(ledger.Validation{
			ID: id, Target: target, Verdict: ledger.VerdictSupported, Rationale: "measured",
			OccurredAt: time.Now().UTC().Truncate(time.Microsecond), Provenance: f.Actor,
		})
	})
	return id
}
