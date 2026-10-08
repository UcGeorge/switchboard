// Package db opens the SQLite database and applies embedded migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DefaultDataDir returns the directory holding the database and logs.
// SWITCHBOARD_DATA_DIR overrides it.
func DefaultDataDir() string {
	if v := os.Getenv("SWITCHBOARD_DATA_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".switchboard"
	}
	return filepath.Join(home, ".switchboard")
}

// DefaultPath returns the default database file path.
func DefaultPath() string {
	if v := os.Getenv("SWITCHBOARD_DB"); v != "" {
		return v
	}
	return filepath.Join(DefaultDataDir(), "switchboard.db")
}

// Open opens (creating if needed) the SQLite database at path, configures it
// for concurrent local use (WAL, busy timeout, immediate transactions) and
// applies pending migrations.
func Open(path string) (*sql.DB, error) {
	if IsPostgresURL(path) {
		return openPostgres(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + path + "?" + url.Values{
		"_txlock": {"immediate"},
		"_pragma": {
			"busy_timeout(10000)",
			"journal_mode(WAL)",
			"synchronous(NORMAL)",
			"foreign_keys(ON)",
			"temp_store(MEMORY)",
		},
	}.Encode()
	// url.Values encodes the parens; sqlite driver wants them literal.
	dsn = strings.ReplaceAll(dsn, "%28", "(")
	dsn = strings.ReplaceAll(dsn, "%29", ")")

	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(8)
	d.SetMaxIdleConns(8)
	d.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := Migrate(ctx, d); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// Migrate applies any migration files not yet recorded in schema_migrations.
func Migrate(ctx context.Context, d *sql.DB) error {
	if IsPostgres(d) {
		return migratePostgres(ctx, d)
	}
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("init migrations table: %w", err)
	}
	applied := map[string]bool{}
	rows, err := d.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			name, time.Now().UnixMilli()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
