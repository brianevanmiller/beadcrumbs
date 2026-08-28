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

// Check statuses. Anything but statusOK makes the report's OK false only for
// statusFail; a warning is actionable but not broken.
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
)

// Check is one named diagnostic with a status and an actionable detail. Detail
// is prose for a human; Name is what a script matches on.
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

func (r *StoreReport) add(name, status, detail string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: detail})
	if status == StatusFail {
		r.OK = false
	}
}

// Diagnose reports on an open ledger.
func (s *Store) Diagnose(ctx context.Context) (StoreReport, error) {
	r := StoreReport{LedgerPath: s.loc.Dir, Stealth: s.loc.Stealth, OK: true}
	r.add("ledger_open", StatusOK, fmt.Sprintf("engine open at %s", s.loc.Dir))

	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return r, err
	}
	r.SchemaVersion = version
	switch want := CurrentSchemaVersion(); {
	case version == want:
		r.add("schema_version", StatusOK, fmt.Sprintf("schema version %d", version))
	case version < want:
		r.add("schema_version", StatusFail,
			fmt.Sprintf("ledger is at schema version %d, this build expects %d; run `bdc init`", version, want))
	default:
		r.add("schema_version", StatusFail,
			fmt.Sprintf("ledger is at schema version %d, newer than this build's %d; upgrade bdc", version, want))
	}

	r.TotalBytes = dirSize(s.loc.Dir)
	r.JournalBytes = journalBytes(s.loc.Dir)
	if r.JournalBytes > GCThresholdBytes {
		r.GCRecommended = true
		r.add("journal_size", StatusWarn,
			fmt.Sprintf("chunk journal is %d bytes, past the %d byte threshold; run `bdc gc`", r.JournalBytes, GCThresholdBytes))
	} else {
		r.add("journal_size", StatusOK, fmt.Sprintf("chunk journal is %d bytes", r.JournalBytes))
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
		r.add("ledger_lock", StatusFail,
			fmt.Sprintf("ledger %s is locked by another process; retry when that command finishes", loc.Dir))
	case errors.Is(openErr, ledger.ErrNoLedger):
		r.add("ledger_present", StatusFail,
			fmt.Sprintf("no ledger at %s; run `bdc init`", loc.Dir))
	default:
		r.add("ledger_open", StatusFail, openErr.Error())
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
	leftovers := restoreLeftovers(loc)
	if len(leftovers) == 0 {
		r.add("restore_leftovers", StatusOK, "no interrupted restore")
		return
	}
	r.add("restore_leftovers", StatusWarn,
		fmt.Sprintf("interrupted restore left %s; the live ledger is authoritative — remove the leftovers once you have confirmed it",
			strings.Join(leftovers, ", ")))
}

func restoreLeftovers(loc Location) []string {
	parent, base := filepath.Dir(loc.Dir), filepath.Base(loc.Dir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, base+asideSuffix) || strings.HasPrefix(name, base+stagingSuffix) {
			out = append(out, filepath.Join(parent, name))
		}
	}
	return out
}
