package dolt

import (
	"context"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dolthub/dolt/go/store/chunks"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// BackupResult is what `bdc backup` reports.
type BackupResult struct {
	Destination   string `json:"destination"`
	Bytes         int64  `json:"bytes"`
	SchemaVersion int    `json:"schema_version"`
}

// Backup pushes the ledger — history included, not just the working set — to
// destURL. A bare filesystem path is accepted and normalised to file://.
func (s *Store) Backup(ctx context.Context, destURL string) (BackupResult, error) {
	dest, err := normalizeRemoteURL(destURL)
	if err != nil {
		return BackupResult{}, err
	}
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return BackupResult{}, err
	}
	if _, err := s.db.ExecContext(ctx, "CALL DOLT_BACKUP('sync-url', ?)", dest); err != nil {
		return BackupResult{}, storageErr(err, "backup to %s failed", dest)
	}
	return BackupResult{Destination: dest, Bytes: remoteSize(dest), SchemaVersion: version}, nil
}

// normalizeRemoteURL turns a filesystem path into an absolute file:// URL and
// leaves any explicit scheme (file, aws, gs, …) untouched.
func normalizeRemoteURL(raw string) (string, error) {
	if raw == "" {
		return "", ledger.Fail(ledger.ErrInvalidInput, "invalid_destination", "destination URL is required")
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && len(u.Scheme) > 1 {
		return raw, nil
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", ledger.FailWith(ledger.ErrInvalidInput, "invalid_destination", err,
			"cannot resolve destination %q", raw)
	}
	return "file://" + abs, nil
}

// remoteSize measures a file:// destination. Non-file destinations report 0
// rather than a guess.
func remoteSize(dest string) int64 {
	path, ok := strings.CutPrefix(dest, "file://")
	if !ok {
		return 0
	}
	return dirSize(path)
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}

// journalBytes is the size of the Dolt chunk journal, which per-transaction
// commits grow fast and DOLT_GC reclaims. A fresh ledger has no journal file
// yet, which reports as 0 rather than as an error.
func journalBytes(dir string) int64 {
	path := filepath.Join(dir, DatabaseName, ".dolt", "noms", chunks.JournalFileID)
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}
