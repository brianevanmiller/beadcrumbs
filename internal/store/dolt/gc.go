package dolt

import (
	"context"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// GCResult is what `bdc doctor` and `bdc gc` report.
type GCResult = ledger.GCResult

// GCThresholdBytes is the journal size past which `bdc doctor` warns and
// capture/harvest trigger GC opportunistically. A commit per transaction grows
// the journal fast, and DOLT_GC is cheap, so the threshold is low.
const GCThresholdBytes int64 = 8 << 20

// GC runs DOLT_GC. It reclaims the journal; it does not rewrite committed
// history, so a pruned Crumb stays readable through AS OF afterwards.
func (s *Store) GC(ctx context.Context) (GCResult, error) {
	before := dirSize(s.loc.Dir)
	started := time.Now()
	if _, err := s.db.ExecContext(ctx, "CALL DOLT_GC()"); err != nil {
		return GCResult{}, storageErr(err, "dolt gc failed")
	}
	elapsed := time.Since(started)
	return GCResult{
		BeforeBytes: before,
		AfterBytes:  dirSize(s.loc.Dir),
		DurationMS:  elapsed.Milliseconds(),
	}, nil
}
