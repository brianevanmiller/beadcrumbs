package dolt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// TestBusyReturnsTypedError: a ledger held by another process must surface as a
// typed, bounded failure — exit 4 — and never as a hang. The proof measured a
// 16.7s block behind a single writer, which is what MaxOpenWait exists to bound.
func TestBusyReturnsTypedError(t *testing.T) {
	repo := fixtureRepo(t)
	loc := initLedger(t, repo, false)
	holdLedger(t, loc.Dir)

	started := time.Now()
	_, err := Open(context.Background(), loc, Config{
		MaxOpenWait: 500 * time.Millisecond,
		Command:     "capture",
	})
	elapsed := time.Since(started)

	if !errors.Is(err, ledger.ErrBusy) {
		t.Fatalf("expected ledger.ErrBusy, got %v", err)
	}
	var le *ledger.Error
	if !errors.As(err, &le) || le.Code != "ledger_busy" {
		t.Fatalf("expected error code ledger_busy, got %v", err)
	}
	if elapsed > 20*time.Second {
		t.Fatalf("Open waited %s; MaxOpenWait did not bound it", elapsed)
	}
}

// TestDiagnoseReportsLockedByAnotherProcess: doctor is the one command that has
// to stay useful when the engine will not open, so "locked by another process"
// must be a named check rather than a bare error.
func TestDiagnoseReportsLockedByAnotherProcess(t *testing.T) {
	repo := fixtureRepo(t)
	loc := initLedger(t, repo, false)
	holdLedger(t, loc.Dir)

	_, openErr := Open(context.Background(), loc, Config{
		MaxOpenWait: 500 * time.Millisecond,
		Command:     "doctor",
	})
	if !errors.Is(openErr, ledger.ErrBusy) {
		t.Fatalf("expected the ledger to be busy, got %v", openErr)
	}

	report := DiagnoseUnopened(loc, openErr)
	if report.OK {
		t.Fatal("a locked ledger reported OK")
	}
	check := findCheck(t, report, "ledger_lock")
	if check.Status != StatusFail {
		t.Fatalf("ledger_lock status is %q, want %q", check.Status, StatusFail)
	}
	if !strings.Contains(check.Detail, "locked by another process") {
		t.Fatalf("detail does not say what is wrong: %q", check.Detail)
	}
	if !strings.Contains(check.Detail, loc.Dir) {
		t.Fatalf("detail does not name the ledger: %q", check.Detail)
	}
}

// An uninitialised repository is the other unopenable state a user must be able
// to tell apart from a locked one.
func TestDiagnoseReportsUninitialisedLedger(t *testing.T) {
	repo := fixtureRepo(t)
	loc, err := Discover(context.Background(), repo)
	if !errors.Is(err, ledger.ErrNoLedger) {
		t.Fatalf("expected ErrNoLedger, got %v", err)
	}

	report := DiagnoseUnopened(loc, err)
	if report.OK {
		t.Fatal("an uninitialised ledger reported OK")
	}
	check := findCheck(t, report, "ledger_present")
	if check.Status != StatusFail || !strings.Contains(check.Detail, "bdc init") {
		t.Fatalf("ledger_present must fail and point at `bdc init`, got %+v", check)
	}
}

func TestDiagnoseReportsHealthyLedger(t *testing.T) {
	repo := fixtureRepo(t)
	loc := initLedger(t, repo, false)
	store := openLedger(t, loc, Config{})

	report, err := store.Diagnose(context.Background())
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if !report.OK {
		t.Fatalf("a freshly initialised ledger is not OK: %+v", report.Checks)
	}
	if report.SchemaVersion != CurrentSchemaVersion() {
		t.Fatalf("schema version is %d, want %d", report.SchemaVersion, CurrentSchemaVersion())
	}
	for _, name := range []string{"ledger_open", "schema_version", "journal_size", "restore_leftovers"} {
		if findCheck(t, report, name).Status != StatusOK {
			t.Fatalf("check %s is not ok: %+v", name, report.Checks)
		}
	}
}

func findCheck(t *testing.T, report StoreReport, name string) Check {
	t.Helper()
	for _, c := range report.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("report has no check named %q: %+v", name, report.Checks)
	return Check{}
}
