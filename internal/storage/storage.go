// Package storage persists probe results, incidents, outages, and gaps to a
// local SQLite database (pure Go, no cgo) so history survives restarts.
package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/probe"
)

// Outage is a period in which every external destination failed.
type Outage struct {
	ID    int64      `json:"id"`
	Start time.Time  `json:"start"`
	End   *time.Time `json:"end,omitempty"`
	Cause string     `json:"cause"` // "isp" or "local"
}

// Duration returns the outage length, using now for an outage still open.
func (o Outage) Duration(now time.Time) time.Duration {
	end := now
	if o.End != nil {
		end = *o.End
	}
	return end.Sub(o.Start)
}

// Gap is a period with no monitoring (machine asleep or agent not running).
type Gap struct {
	ID     int64     `json:"id"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Reason string    `json:"reason"` // "sleep" or "restart"
}

type DB struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS probes (
  ts_ms INTEGER NOT NULL,
  target TEXT NOT NULL,
  kind TEXT NOT NULL,
  latency_us INTEGER NOT NULL,
  success INTEGER NOT NULL,
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS probes_ts ON probes(ts_ms);
CREATE TABLE IF NOT EXISTS incidents (
  ts_ms INTEGER NOT NULL,
  target TEXT NOT NULL,
  severity TEXT NOT NULL,
  state TEXT NOT NULL,
  message TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS incidents_ts ON incidents(ts_ms);
CREATE TABLE IF NOT EXISTS outages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  start_ms INTEGER NOT NULL,
  end_ms INTEGER,
  cause TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS outages_start ON outages(start_ms);
CREATE TABLE IF NOT EXISTS gaps (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  start_ms INTEGER NOT NULL,
  end_ms INTEGER NOT NULL,
  reason TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS gaps_start ON gaps(start_ms);
`

// DefaultPath returns ~/Library/Application Support/fcomt/fcomt.db.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "fcomt.db"
	}
	return filepath.Join(home, "Library", "Application Support", "fcomt", "fcomt.db")
}

// Open opens (creating if needed) the database at path. Use ":memory:" in tests.
func Open(path string) (*DB, error) {
	dsn := ":memory:"
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	}
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(schema); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	if err := migrate(sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return &DB{db: sqlDB}, nil
}

// migrate applies additive, idempotent schema changes to existing databases.
// Every step checks the current schema first, so running it on every Open is
// safe. Older binaries keep working against a migrated DB because they name
// their INSERT columns and new columns have defaults.
func migrate(db *sql.DB) error {
	has, err := hasColumn(db, "probes", "mode")
	if err != nil {
		return err
	}
	if !has {
		// mode: polling mode of the round (normal | fast | slow). Existing
		// rows were all taken at the regular cadence.
		if _, err := db.Exec(`ALTER TABLE probes ADD COLUMN mode TEXT NOT NULL DEFAULT 'normal'`); err != nil {
			return fmt.Errorf("add probes.mode: %w", err)
		}
	}
	return nil
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// HasColumn reports whether table has column (used by tests and diagnostics).
func (d *DB) HasColumn(table, column string) (bool, error) { return hasColumn(d.db, table, column) }

func (d *DB) Close() error { return d.db.Close() }

func ms(t time.Time) int64     { return t.UTC().UnixMilli() }
func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

func (d *DB) InsertProbes(results []probe.Result) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO probes(ts_ms,target,kind,latency_us,success,error,mode) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, r := range results {
		succ := 0
		if r.Success {
			succ = 1
		}
		if _, err := stmt.Exec(ms(r.Timestamp), r.Target, string(r.Kind), r.Latency.Microseconds(), succ, r.Error, r.NormalizedMode()); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ProbesSince returns probes with timestamp >= since, oldest first.
func (d *DB) ProbesSince(since time.Time) ([]probe.Result, error) {
	rows, err := d.db.Query(`SELECT ts_ms,target,kind,latency_us,success,error,mode FROM probes WHERE ts_ms >= ? ORDER BY ts_ms`, ms(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []probe.Result
	for rows.Next() {
		var ts, lat int64
		var target, kind, errStr, mode string
		var succ int
		if err := rows.Scan(&ts, &target, &kind, &lat, &succ, &errStr, &mode); err != nil {
			return nil, err
		}
		out = append(out, probe.Result{
			Target: target, Kind: probe.Kind(kind), Timestamp: fromMS(ts),
			Latency: time.Duration(lat) * time.Microsecond, Success: succ == 1, Error: errStr, Mode: mode,
		})
	}
	return out, rows.Err()
}

// LastProbeTime returns the timestamp of the newest probe, if any.
func (d *DB) LastProbeTime() (time.Time, bool, error) {
	var v sql.NullInt64
	if err := d.db.QueryRow(`SELECT MAX(ts_ms) FROM probes`).Scan(&v); err != nil {
		return time.Time{}, false, err
	}
	if !v.Valid {
		return time.Time{}, false, nil
	}
	return fromMS(v.Int64), true, nil
}

// FirstProbeTime returns the timestamp of the oldest probe, if any.
func (d *DB) FirstProbeTime() (time.Time, bool, error) {
	var v sql.NullInt64
	if err := d.db.QueryRow(`SELECT MIN(ts_ms) FROM probes`).Scan(&v); err != nil {
		return time.Time{}, false, err
	}
	if !v.Valid {
		return time.Time{}, false, nil
	}
	return fromMS(v.Int64), true, nil
}

func (d *DB) InsertIncident(e incident.Event) error {
	_, err := d.db.Exec(`INSERT INTO incidents(ts_ms,target,severity,state,message) VALUES(?,?,?,?,?)`,
		ms(e.Time), e.Target, e.Severity, e.State, e.Message)
	return err
}

// Incidents returns incidents in [from, to], oldest first. limit<=0 means no limit
// (when limited, the newest rows are returned).
func (d *DB) Incidents(from, to time.Time, limit int) ([]incident.Event, error) {
	q := `SELECT ts_ms,target,severity,state,message FROM incidents WHERE ts_ms >= ? AND ts_ms <= ? ORDER BY ts_ms DESC`
	args := []any{ms(from), ms(to)}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := d.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []incident.Event
	for rows.Next() {
		var ts int64
		var e incident.Event
		if err := rows.Scan(&ts, &e.Target, &e.Severity, &e.State, &e.Message); err != nil {
			return nil, err
		}
		e.Time = fromMS(ts)
		out = append(out, e)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

func (d *DB) OpenOutage(start time.Time, cause string) (int64, error) {
	res, err := d.db.Exec(`INSERT INTO outages(start_ms,cause) VALUES(?,?)`, ms(start), cause)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) CloseOutage(id int64, end time.Time) error {
	_, err := d.db.Exec(`UPDATE outages SET end_ms = ? WHERE id = ?`, ms(end), id)
	return err
}

// CloseDanglingOutages closes any outage left open by a crash/restart at end.
func (d *DB) CloseDanglingOutages(end time.Time) (int64, error) {
	res, err := d.db.Exec(`UPDATE outages SET end_ms = MAX(start_ms, ?) WHERE end_ms IS NULL`, ms(end))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Outages returns outages overlapping [from, to], oldest first.
func (d *DB) Outages(from, to time.Time) ([]Outage, error) {
	rows, err := d.db.Query(`SELECT id,start_ms,end_ms,cause FROM outages
		WHERE start_ms <= ? AND (end_ms IS NULL OR end_ms >= ?) ORDER BY start_ms`, ms(to), ms(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Outage
	for rows.Next() {
		var o Outage
		var start int64
		var end sql.NullInt64
		if err := rows.Scan(&o.ID, &start, &end, &o.Cause); err != nil {
			return nil, err
		}
		o.Start = fromMS(start)
		if end.Valid {
			t := fromMS(end.Int64)
			o.End = &t
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (d *DB) InsertGap(g Gap) error {
	if !g.End.After(g.Start) {
		return errors.New("gap end must be after start")
	}
	_, err := d.db.Exec(`INSERT INTO gaps(start_ms,end_ms,reason) VALUES(?,?,?)`, ms(g.Start), ms(g.End), g.Reason)
	return err
}

// Gaps returns gaps overlapping [from, to], oldest first.
func (d *DB) Gaps(from, to time.Time) ([]Gap, error) {
	rows, err := d.db.Query(`SELECT id,start_ms,end_ms,reason FROM gaps WHERE start_ms <= ? AND end_ms >= ? ORDER BY start_ms`, ms(to), ms(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Gap
	for rows.Next() {
		var g Gap
		var s, e int64
		if err := rows.Scan(&g.ID, &s, &e, &g.Reason); err != nil {
			return nil, err
		}
		g.Start, g.End = fromMS(s), fromMS(e)
		out = append(out, g)
	}
	return out, rows.Err()
}
