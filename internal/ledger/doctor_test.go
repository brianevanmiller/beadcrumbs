package ledger_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// TestDoctorDetectsOrphanedPolymorphicTargets is the release gate for the check
// the database cannot make. ref_links.record_id, validations.target_id, and
// authorities.target_id are polymorphic and therefore carry no foreign key, so
// a write path that deletes a record without clearing them leaves rows pointing
// at nothing — and only `bdc doctor` can find them.
//
// The orphan is made with a raw DELETE on purpose: that is exactly what a write
// path that forgot looks like, and PruneCrumbs — the one path that deletes
// today — is asserted elsewhere to leave none.
func TestDoctorDetectsOrphanedPolymorphicTargets(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("a fragment something else came to point at", 0.5)
	target := ledger.RecordRef{Kind: ledger.KindCrumb, ID: string(crumb.ID)}

	f.write(func(tx ledger.Tx) error {
		return tx.AppendValidation(ledger.Validation{
			ID: ledger.NewValidationID(), Target: target, Verdict: ledger.VerdictSupported,
			Rationale: "reproduced", OccurredAt: time.Now().UTC().Truncate(time.Microsecond), Provenance: f.Actor,
		})
	})

	report, err := f.L.Doctor(ctx)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if check := findCheck(t, report, "polymorphic_targets"); check.Status != ledger.StatusOK {
		t.Fatalf("a live target is reported as an orphan: %s", check.Detail)
	}

	if _, err := f.Store.DB().ExecContext(ctx, `DELETE FROM crumbs WHERE id = ?`, string(crumb.ID)); err != nil {
		t.Fatalf("deleting the Crumb behind the validation: %v", err)
	}

	report, err = f.L.Doctor(ctx)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	check := findCheck(t, report, "polymorphic_targets")
	if check.Status != ledger.StatusFail {
		t.Fatalf("an orphaned validation target is reported as %q: %s", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, string(crumb.ID)) {
		t.Fatalf("the check does not name the orphan it found: %s", check.Detail)
	}
	if report.OK {
		t.Fatal("a report with a failing check still says the ledger is ok")
	}
	// Doctor never repairs: the row it named is still there for whoever fixes
	// the write path that produced it.
	if n := f.count(`SELECT COUNT(*) FROM validations WHERE target_id = ?`, string(crumb.ID)); n != 1 {
		t.Fatalf("doctor changed the ledger: %d orphaned validation(s) remain", n)
	}
}

func findCheck(t *testing.T, report ledger.Report, name string) ledger.Check {
	t.Helper()
	for _, c := range report.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q check in %v", name, report.Checks)
	return ledger.Check{}
}
