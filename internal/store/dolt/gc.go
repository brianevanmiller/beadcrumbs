package dolt

import (
	"context"
	"time"
)

// GCResult is what `bdc doctor` and `bdc gc` report.
type GCResult struct {
	BeforeBytes int64 `json:"before_bytes"`
	AfterBytes  int64 `json:"after_bytes"`
	DurationMS  int64 `json:"duration_ms"`
}

// GCThresholdBytes is the journal size past which `bdc doctor` warns and
// capture/harvest trigger GC opportunistically. Per-transaction commits reach
// tens of megabytes within a few thousand rows, and DOLT_GC reclaims that in
// well under a second, so the threshold is set low enough to keep the journal
// from ever dominating the ledger.
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
