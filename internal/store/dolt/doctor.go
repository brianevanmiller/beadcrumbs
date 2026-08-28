package dolt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// Check statuses and the report shape belong to the storage port, so a caller
// above this package never learns which engine produced one. Only StatusFail
// makes a report not OK; a warning is actionable but not broken.
const (
	StatusOK   = ledger.StatusOK
	StatusWarn = ledger.StatusWarn
	StatusFail = ledger.StatusFail
)

type (
	Check       = ledger.Check
	StoreReport = ledger.StoreReport
)

// Diagnose reports on an open ledger.
func (s *Store) Diagnose(ctx context.Context) (StoreReport, error) {
	r := StoreReport{LedgerPath: s.loc.Dir, Stealth: s.loc.Stealth, OK: true}
	r.Add("ledger_open", StatusOK, fmt.Sprintf("engine open at %s", s.loc.Dir))

	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return r, err
	}
	r.SchemaVersion = version
	switch want := CurrentSchemaVersion(); {
	case version == want:
		r.Add("schema_version", StatusOK, fmt.Sprintf("schema version %d", version))
	case version < want:
		r.Add("schema_version", StatusFail,
			fmt.Sprintf("ledger is at schema version %d, this build expects %d; run `bdc migrate`", version, want))
	default:
		r.Add("schema_version", StatusFail,
			fmt.Sprintf("ledger is at schema version %d, newer than this build's %d; upgrade bdc", version, want))
	}

	r.TotalBytes = dirSize(s.loc.Dir)
	r.JournalBytes = journalBytes(s.loc.Dir)
	if r.JournalBytes > GCThresholdBytes {
		r.GCRecommended = true
		r.Add("journal_size", StatusWarn,
			fmt.Sprintf("chunk journal is %d bytes, past the %d byte threshold; run `bdc gc`", r.JournalBytes, GCThresholdBytes))
	} else {
		r.Add("journal_size", StatusOK, fmt.Sprintf("chunk journal is %d bytes", r.JournalBytes))
	}

	addLeftoverChecks(&r, s.loc)
	return r, nil
}

// DiagnoseUnopened reports on a ledger the engine could not open. This is the
// path that has to stay useful: "locked by another process" and "not
// initialised" are the two states a user most needs named, and neither is
// reachable from an open engine.
func DiagnoseUnopened(loc Location, openErr error) StoreReport {
	r := StoreReport{LedgerPath: loc.Dir, Stealth: loc.Stealth, OK: true}
	switch {
	case errors.Is(openErr, ledger.ErrBusy):
		r.Add("ledger_lock", StatusFail,
			fmt.Sprintf("ledger %s is locked by another process; retry when that command finishes", loc.Dir))
	case errors.Is(openErr, ledger.ErrNoLedger):
		r.Add("ledger_present", StatusFail,
			fmt.Sprintf("no ledger at %s; run `bdc init`", loc.Dir))
	default:
		r.Add("ledger_open", StatusFail, openErr.Error())
	}
	r.TotalBytes = dirSize(loc.Dir)
	r.JournalBytes = journalBytes(loc.Dir)
	addLeftoverChecks(&r, loc)
	return r
}

// addLeftoverChecks surfaces an interrupted restore. Restore's rename-aside is
// its commit point, so both directories on disk is a recoverable state — but
// only if doctor names the aside path.
func addLeftoverChecks(r *StoreReport, loc Location) {
	leftovers, err := restoreLeftovers(loc)
	if err != nil {
		// "I could not look" is not "there is nothing there": reporting the
		// second would hide the ledger copy that still holds the data.
		r.Add("restore_leftovers", StatusWarn,
			fmt.Sprintf("cannot read %s to check for an interrupted restore: %v", filepath.Dir(loc.Dir), err))
		return
	}
	if len(leftovers) == 0 {
		r.Add("restore_leftovers", StatusOK, "no interrupted restore")
		return
	}
	r.Add("restore_leftovers", StatusWarn,
		fmt.Sprintf("interrupted restore left %s; the live ledger is authoritative — remove the leftovers once you have confirmed it",
			strings.Join(leftovers, ", ")))
}

func restoreLeftovers(loc Location) ([]string, error) {
	parent, base := filepath.Dir(loc.Dir), filepath.Base(loc.Dir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, base+asideSuffix) || strings.HasPrefix(name, base+stagingSuffix) {
			out = append(out, filepath.Join(parent, name))
		}
	}
	return out, nil
}
