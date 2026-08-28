package ledger

import (
	"context"
	"fmt"
)

// Report is `bdc doctor`. It merges the storage diagnosis with the checks only
// the domain can make: the three polymorphic target columns that carry no
// foreign key, and the materialised head revision that nothing else verifies.
type Report struct {
	LedgerPath    string  `json:"ledger_path"`
	Stealth       bool    `json:"stealth"`
	SchemaVersion int     `json:"schema_version"`
	JournalBytes  int64   `json:"journal_bytes"`
	TotalBytes    int64   `json:"total_bytes"`
	GCRecommended bool    `json:"gc_recommended"`
	Counts        Counts  `json:"counts"`
	Checks        []Check `json:"checks"`
	OK            bool    `json:"ok"`
}

// Add appends a check, mirroring StoreReport.Add: only a failure flips OK.
func (r *Report) Add(name, status, detail string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: detail})
	if status == StatusFail {
		r.OK = false
	}
}

// Doctor reports on an open ledger. It never repairs: an orphan or a drifted
// head is a defect in a write path, and silently fixing one would hide the bug
// that produced it.
func (l *Ledger) Doctor(ctx context.Context) (Report, error) {
	store, err := l.store.Diagnose(ctx)
	if err != nil {
		return Report{}, err
	}
	r := Report{
		LedgerPath:    store.LedgerPath,
		Stealth:       store.Stealth,
		SchemaVersion: store.SchemaVersion,
		JournalBytes:  store.JournalBytes,
		TotalBytes:    store.TotalBytes,
		GCRecommended: store.GCRecommended,
		Checks:        store.Checks,
		OK:            store.OK,
	}

	err = l.store.Read(ctx, func(snap Snapshot) error {
		orphans, err := snap.OrphanTargets()
		if err != nil {
			return err
		}
		if len(orphans) == 0 {
			r.Add("polymorphic_targets", StatusOK, "every polymorphic target points at a live record")
		} else {
			r.Add("polymorphic_targets", StatusFail, describeOrphans(orphans))
		}

		drift, err := snap.HeadRevisionDrift()
		if err != nil {
			return err
		}
		if len(drift) == 0 {
			r.Add("head_revision", StatusOK, "every Insight's head matches its latest revision")
		} else {
			d := drift[0]
			r.Add("head_revision", StatusFail, fmt.Sprintf(
				"%d Insight(s) have a head that disagrees with their revisions, starting with %s (head %d, latest revision %d)",
				len(drift), d.InsightID, d.Head, d.MaxRevision))
		}

		r.Counts, err = snap.Counts(CountQuery{})
		return err
	})
	if err != nil {
		return r, err
	}
	return r, nil
}

func describeOrphans(orphans []OrphanRow) string {
	first := orphans[0]
	return fmt.Sprintf("%d row(s) point at a record that no longer exists, starting with %s.%s -> %s:%s",
		len(orphans), first.Table, first.Column, first.RecordKind, first.RecordID)
}
