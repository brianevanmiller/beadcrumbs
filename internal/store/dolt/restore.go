package dolt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

const (
	asideSuffix   = ".old-"
	stagingSuffix = ".restore-"

	// Restore unpacks a whole ledger and verifies it twice, so it sets a larger
	// lock budget than an ordinary command.
	restoreMaxOpenWait = 2 * time.Minute

	// evictMaxOpenWait bounds the check that nothing holds the live ledger. It
	// is short on purpose: this is not a queue to join, it is a question with
	// two answers, and "someone is writing" has to reach the user as exit 4.
	evictMaxOpenWait = 2 * time.Second
)

// RestoreOptions configures Restore. Force is what authorises the
// aside-and-swap over an existing ledger.
type RestoreOptions struct {
	Force bool
}

// RestoreResult is what `bdc restore` reports.
type RestoreResult struct {
	Restored      string `json:"restored"`
	SchemaVersion int    `json:"schema_version"`
	Records       int    `json:"records"`
}

// Restore replaces the ledger directory with a copy unpacked from srcURL. It is
// a lifecycle operation, not a write, and must never run against an open engine.
//
// Every step before the first rename is discardable. The first rename is the
// commit point: an interruption after it leaves both directories on disk, which
// `bdc doctor` reports as a recoverable state naming the aside path.
func Restore(ctx context.Context, loc Location, srcURL string, o RestoreOptions) (RestoreResult, error) {
	src, err := normalizeRemoteURL(srcURL)
	if err != nil {
		return RestoreResult{}, err
	}
	if ledgerExists(loc.Dir) && !o.Force {
		return RestoreResult{}, ledger.Fail(ledger.ErrInvalidInput, "invalid_restore_would_replace",
			"a ledger already exists at %s; pass --force to replace it", loc.Dir)
	}

	release := acquireProcessLock(loc.Dir, "restore")
	defer release()

	stamp := time.Now().UTC().Format("20060102T150405Z")
	parent, base := filepath.Dir(loc.Dir), filepath.Base(loc.Dir)
	staging := filepath.Join(parent, base+stagingSuffix+stamp)
	aside := filepath.Join(parent, base+asideSuffix+stamp)

	if err := os.MkdirAll(staging, 0o700); err != nil {
		return RestoreResult{}, storageErr(err, "cannot create staging directory %s", staging)
	}
	stagingLive := true
	defer func() {
		if stagingLive {
			_ = os.RemoveAll(staging)
		}
	}()

	if err := unpack(ctx, staging, src); err != nil {
		return RestoreResult{}, err
	}
	version, records, err := verify(ctx, staging)
	if err != nil {
		return RestoreResult{}, err
	}
	// A backup written by a newer build is rejected here, while the staging copy
	// is still discardable. After the swap the previous ledger is gone and the
	// mismatch is only reportable, not recoverable.
	if want := CurrentSchemaVersion(); version > want {
		return RestoreResult{}, ledger.Fail(ledger.ErrIntegrity, "integrity_schema_version",
			"the backup is at schema version %d, newer than this build's %d; upgrade bdc before restoring it",
			version, want)
	}
	if err := syncTree(staging); err != nil {
		return RestoreResult{}, err
	}
	if err := assertUnheld(ctx, loc.Dir); err != nil {
		return RestoreResult{}, err
	}

	// Commit point. From here a failure leaves recoverable state on disk rather
	// than an empty ledger directory.
	movedAside := false
	if _, err := os.Stat(loc.Dir); err == nil {
		if err := os.Rename(loc.Dir, aside); err != nil {
			return RestoreResult{}, storageErr(err, "cannot move %s aside to %s", loc.Dir, aside)
		}
		movedAside = true
		if err := syncPath(parent); err != nil {
			return RestoreResult{}, err
		}
	}
	if err := os.Rename(staging, loc.Dir); err != nil {
		if movedAside {
			_ = os.Rename(aside, loc.Dir)
		}
		return RestoreResult{}, storageErr(err, "cannot move %s into place at %s", staging, loc.Dir)
	}
	stagingLive = false
	// Each rename is durable only once the directory that holds the name is,
	// so the parent is synced after both — otherwise a crash can leave the swap
	// half-recorded even though every byte of the ledger is on disk.
	if err := syncPath(parent); err != nil {
		return RestoreResult{}, err
	}

	if _, _, err := verify(ctx, loc.Dir); err != nil {
		return RestoreResult{}, ledger.FailWith(ledger.ErrIntegrity, "integrity_restore_unverified", err,
			"restored ledger at %s failed verification; the previous ledger is at %s", loc.Dir, aside)
	}
	if movedAside {
		if err := os.RemoveAll(aside); err != nil {
			return RestoreResult{}, storageErr(err, "restore succeeded but cannot remove %s", aside)
		}
	}
	return RestoreResult{Restored: loc.Dir, SchemaVersion: version, Records: records}, nil
}

// unpack pulls the backup into dir as database DatabaseName. DOLT_BACKUP
// 'restore' is the only operation that needs no current database, which is why
// the engine opens with an empty database name.
func unpack(ctx context.Context, dir, srcURL string) error {
	db, con, err := openEngine(ctx, dir, "", restoreMaxOpenWait)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
		_ = con.Close()
	}()
	if _, err := db.ExecContext(ctx, "CALL DOLT_BACKUP('restore', ?, ?)", srcURL, DatabaseName); err != nil {
		return ledger.FailWith(ledger.ErrIntegrity, "integrity_restore_failed", err,
			"cannot restore %s into %s", srcURL, dir)
	}
	return nil
}

// verify opens a candidate ledger directory and reads back the two facts that
// prove it is usable: its schema version and its record counts.
func verify(ctx context.Context, dir string) (version, records int, err error) {
	if !ledgerExists(dir) {
		return 0, 0, ledger.Fail(ledger.ErrIntegrity, "integrity_restore_no_database",
			"%s holds no %s database after restore", dir, DatabaseName)
	}
	db, con, err := openEngine(ctx, dir, DatabaseName, restoreMaxOpenWait)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		_ = db.Close()
		_ = con.Close()
	}()
	if version, err = schemaVersion(ctx, db); err != nil {
		return 0, 0, err
	}
	if records, err = countRecords(ctx, db); err != nil {
		return 0, 0, err
	}
	return version, records, nil
}

// countRecords sums every base table in the ledger database. It reads the table
// list from information_schema so it stays correct as the schema grows.
func countRecords(ctx context.Context, db *sql.DB) (int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = ? AND table_type = 'BASE TABLE' ORDER BY table_name`, DatabaseName)
	if err != nil {
		return 0, storageErr(err, "cannot list tables")
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return 0, storageErr(err, "cannot read table list")
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, storageErr(err, "cannot read table list")
	}
	rows.Close()

	total := 0
	for _, name := range names {
		var n int
		// Table names come from information_schema, not from user input.
		if err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM `%s`", name)).Scan(&n); err != nil {
			return 0, storageErr(err, "cannot count rows in %s", name)
		}
		total += n
	}
	return total, nil
}

// assertUnheld refuses to swap a ledger another process has open. POSIX lets a
// directory be renamed and unlinked under an open handle, so without this the
// holder survives pointing at deleted inodes and everything it writes
// afterwards is silently lost. Taking Dolt's own exclusive directory lock and
// dropping it immediately is the only way to ask; anything but ErrBusy means
// nobody else can be using the directory either, and a restore over a ledger
// too broken to open is exactly the case --force exists for.
func assertUnheld(ctx context.Context, dir string) error {
	if !ledgerExists(dir) {
		return nil
	}
	db, con, err := openEngine(ctx, dir, DatabaseName, evictMaxOpenWait)
	if err != nil {
		if errors.Is(err, ledger.ErrBusy) {
			return err
		}
		return nil
	}
	_ = db.Close()
	_ = con.Close()
	return nil
}

// syncTree makes a directory's contents durable: every regular file inside it,
// then the directory itself. Syncing only the directory flushes its name entries
// and none of the chunk files Dolt just unpacked into it.
func syncTree(dir string) error {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return syncPath(path)
	})
	if err != nil {
		if _, ok := err.(*ledger.Error); ok {
			return err
		}
		return storageErr(err, "cannot walk %s to make it durable", dir)
	}
	return syncPath(dir)
}

// syncPath forces one file or directory to stable storage. On darwin fsync(2)
// only hands the data to the drive, which is why fullSync exists.
func syncPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return storageErr(err, "cannot open %s for fsync", path)
	}
	defer f.Close()
	if err := fullSync(f); err != nil {
		return storageErr(err, "cannot fsync %s", path)
	}
	return nil
}
