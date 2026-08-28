package ledger

import (
	"context"
	"fmt"
)

// Report is `bdc doctor`: the storage diagnosis plus the checks only the domain
// can make — the three polymorphic target columns that carry no foreign key,
// and the materialised head revision that nothing else verifies.
//
// StoreReport is embedded rather than copied field by field. A parallel struct
// would be a second place for a field to go missing, and Add is then one
// method with one answer to "what makes a report not OK".
type Report struct {
	StoreReport
	Counts Counts `json:"counts"`
}

// Doctor reports on an open ledger. It is what `bdc doctor` calls: the storage
// half alone cannot see an orphaned polymorphic target or a drifted head, which
// are the two defects the schema cannot express as constraints. It never
// repairs — silently fixing one would hide the write-path bug that produced it.
func (l *Ledger) Doctor(ctx context.Context) (Report, error) {
	store, err := l.store.Diagnose(ctx)
	if err != nil {
		return Report{}, err
	}
	r := Report{StoreReport: store}

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
