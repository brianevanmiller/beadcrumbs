package dolt

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	doltcli "github.com/dolthub/dolt/go/cmd/dolt/cli"
	"github.com/dolthub/dolt/go/store/nbs"
	embeddeddolt "github.com/dolthub/driver"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

const (
	// Commit identity is fixed. Using the user's Git identity would write
	// personal identity into a store they cannot see in `git status`.
	commitName  = "beadcrumbs"
	commitEmail = "bdc@localhost"

	defaultMaxOpenWait = 15 * time.Second
	defaultMaxOpenHold = 30 * time.Second
)

// schemaFS holds the numbered migrations. Each script is named NNN_<name>.sql
// and is responsible for recording its own row in schema_meta as its last
// statement — that is what makes SchemaVersion the migration state and keeps the
// runner from having to know the table's shape.
//
//go:embed all:schema
var schemaFS embed.FS

func init() {
	// The embedded engine inherits Dolt's CLI output plumbing, and DOLT_BACKUP
	// writes to it directly. stdout carries the JSON envelope and nothing else,
	// so this is a correctness fix, not cosmetics: a stray newline from the
	// engine would corrupt `bdc backup --json`.
	doltcli.CliOut = io.Discard
}

// Config bounds the two lock properties that cannot be conventions.
type Config struct {
	// MaxOpenWait bounds backoff against a directory locked by another process.
	// Exhaustion is ledger.ErrBusy, never a hang. Defaults to 15s; gc, backup,
	// and restore legitimately run long and set their own larger bound.
	MaxOpenWait time.Duration

	// MaxOpenHold is the one-command invariant. A handle still open after it
	// reports a structured invariant violation naming Command; the "no engine
	// across agent turns" rule is otherwise unenforceable. Defaults to 30s.
	MaxOpenHold time.Duration

	// Command names the invocation in a violation report and in the Dolt commit
	// message every write transaction ends with.
	Command string

	// ActorKind is "human" or "agent". It appears in the commit message and
	// nowhere else: the commit carries who acted, never what they wrote.
	ActorKind string

	// OnViolation receives invariant violations. Nil logs to stderr.
	OnViolation func(Violation)
}

func (c Config) withDefaults() Config {
	if c.MaxOpenWait <= 0 {
		c.MaxOpenWait = defaultMaxOpenWait
	}
	if c.MaxOpenHold <= 0 {
		c.MaxOpenHold = defaultMaxOpenHold
	}
	if c.Command == "" {
		c.Command = "unknown"
	}
	return c
}

// Store is one short-lived embedded Dolt engine: open -> one bounded
// transaction -> close. Embedded Dolt holds an exclusive write lock on the
// database directory for the whole life of the engine, so a Store must never be
// held across agent turns.
type Store struct {
	loc Location
	cfg Config
	db  *sql.DB
	con *embeddeddolt.Connector

	release   func()
	watchdog  *watchdog
	closeOnce sync.Once
	closeErr  error
}

// Open opens the ledger at loc.Dir. A second Open while a handle is live in this
// process panics: that is a programming error, not a user condition.
func Open(ctx context.Context, loc Location, cfg Config) (*Store, error) {
	cfg = cfg.withDefaults()
	if !ledgerExists(loc.Dir) {
		return nil, ledger.Fail(ledger.ErrNoLedger, "no_ledger_uninitialized",
			"no ledger at %s; run `bdc init`", loc.Dir)
	}

	release := acquireProcessLock(loc.Dir, cfg.Command)
	db, con, err := openEngine(ctx, loc.Dir, DatabaseName, cfg.MaxOpenWait)
	if err != nil {
		release()
		return nil, err
	}

	s := &Store{loc: loc, cfg: cfg, db: db, con: con, release: release}
	s.watchdog = startWatchdog(cfg, loc.Dir)
	return s, nil
}

// DB is the test-only escape hatch. The port deliberately cannot express the
// assertions the release gate needs — a `dolt_history_*` scan, a constraint
// violation, a raw row count — and proving those through the domain API would
// prove only that the Go half agrees with itself.
func (s *Store) DB() *sql.DB { return s.db }

// Close is idempotent and unconditional: cmd/bdc closes from a defer that also
// runs on a recovered panic.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		if s.watchdog != nil {
			s.watchdog.stop()
		}
		var errs []error
		if err := s.db.Close(); err != nil {
			errs = append(errs, err)
		}
		if err := s.con.Close(); err != nil {
			errs = append(errs, err)
		}
		s.release()
		if len(errs) > 0 {
			s.closeErr = storageErr(errors.Join(errs...), "closing ledger %s", s.loc.Dir)
		}
	})
	return s.closeErr
}

// SchemaVersion is MAX(schema_meta.version), or 0 when the table does not exist
// yet — an initialised-but-unmigrated ledger, not an error.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return schemaVersion(ctx, s.db)
}

// Commit ends a successful write transaction. The driver makes no commits on its
// own, so without this there is no versioned history and backup/restore carry
// only a working set. --allow-empty keeps the commit unconditional; the message
// carries the command name and actor kind and never domain content.
func (s *Store) Commit(ctx context.Context, message string) error {
	if _, err := s.db.ExecContext(ctx, "CALL DOLT_COMMIT('-A', '--allow-empty', '-m', ?)", message); err != nil {
		return storageErr(err, "dolt commit failed")
	}
	return nil
}

// openEngine is the single place a Dolt engine is constructed. Every statement
// is issued singly — migrations are split before they are applied — so no
// caller needs the driver's MultiStatements mode.
func openEngine(ctx context.Context, dir, database string, wait time.Duration) (*sql.DB, *embeddeddolt.Connector, error) {
	bo := backoff.NewExponentialBackOff()
	bo.MaxElapsedTime = wait
	bo.MaxInterval = time.Second

	con, err := embeddeddolt.NewConnector(embeddeddolt.Config{
		Directory:       dir,
		CommitName:      commitName,
		CommitEmail:     commitEmail,
		Database:        database,
		// Non-nil BackOff is what makes journal lock contention return
		// nbs.ErrDatabaseLocked instead of blocking for the holder's lifetime.
		BackOff: bo,
	})
	if err != nil {
		return nil, nil, storageErr(err, "cannot open ledger at %s", dir)
	}

	db := sql.OpenDB(con)
	// One connection: the engine is exclusive anyway, and a pool would let two
	// sessions interleave inside what must be one bounded transaction.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	// The engine opens lazily on first Connect, so the lock outcome is only
	// knowable here. Give backoff its full budget plus room to report.
	openCtx, cancel := context.WithTimeout(ctx, wait+5*time.Second)
	defer cancel()
	if err := db.PingContext(openCtx); err != nil {
		_ = db.Close()
		_ = con.Close()
		if errors.Is(err, nbs.ErrDatabaseLocked) {
			return nil, nil, ledger.FailWith(ledger.ErrBusy, "ledger_busy", err,
				"ledger %s is locked by another process; waited %s", dir, wait)
		}
		return nil, nil, storageErr(err, "cannot open ledger at %s", dir)
	}
	return db, con, nil
}

// createDatabase creates the Dolt database and applies the embedded schema. It
// runs on its own engine because the database does not exist yet.
func createDatabase(ctx context.Context, loc Location) error {
	release := acquireProcessLock(loc.Dir, "init")
	defer release()

	// Two engines, in sequence. The database cannot be selected before it
	// exists, and the driver's current database is fixed at Connect: a `USE`
	// issued afterwards does not survive to the next statement.
	if err := withEngine(ctx, loc.Dir, "", func(db *sql.DB) error {
		if _, err := db.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+DatabaseName); err != nil {
			return storageErr(err, "cannot create database %s in %s", DatabaseName, loc.Dir)
		}
		return nil
	}); err != nil {
		return err
	}

	return withEngine(ctx, loc.Dir, DatabaseName, func(db *sql.DB) error {
		if _, err := applySchema(ctx, db); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "CALL DOLT_COMMIT('-A', '--allow-empty', '-m', ?)", "bdc init: schema"); err != nil {
			return storageErr(err, "dolt commit failed after init")
		}
		return nil
	})
}

// withEngine opens an engine, runs fn, and always closes. Callers must already
// hold the process lock.
func withEngine(ctx context.Context, dir, database string, fn func(*sql.DB) error) error {
	db, con, err := openEngine(ctx, dir, database, defaultMaxOpenWait)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
		_ = con.Close()
	}()
	return fn(db)
}

type migration struct {
	version int
	name    string
	body    string
}

// CurrentSchemaVersion is the highest embedded migration version, and therefore
// what a freshly initialised ledger reports.
func CurrentSchemaVersion() int {
	ms, err := migrations()
	if err != nil || len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].version
}

func migrations() ([]migration, error) {
	entries, err := schemaFS.ReadDir("schema")
	if err != nil {
		return nil, fmt.Errorf("reading embedded schema: %w", err)
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		num, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("embedded schema %q is not NNN_<name>.sql", e.Name())
		}
		v, err := strconv.Atoi(num)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("embedded schema %q has no positive version prefix", e.Name())
		}
		body, err := fs.ReadFile(schemaFS, path.Join("schema", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading embedded schema %q: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: e.Name(), body: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// applySchema brings a freshly created database up to the current version. The
// work lives in applyPending so `bdc init` and `bdc migrate` cannot drift apart.
func applySchema(ctx context.Context, db *sql.DB) (int, error) {
	res, err := applyPending(ctx, db)
	return res.To, err
}

func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_meta").Scan(&v)
	if err != nil {
		if isMissingTable(err) {
			return 0, nil
		}
		return 0, storageErr(err, "cannot read schema version")
	}
	return int(v.Int64), nil
}

// isMissingTable recognises an unmigrated ledger. Dolt reports a missing table
// as a go-mysql-server analyzer error whose text is the stable part of the
// contract; there is no typed error to match on.
func isMissingTable(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "table not found")
}
