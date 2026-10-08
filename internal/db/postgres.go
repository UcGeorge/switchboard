package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
)

// PostgreSQL uses the same sqlc-generated domain queries. The driver boundary
// translates their placeholders and portable integer casts, including raw SQL
// executed by management commands and transactions. Schema migrations are native.
//
//go:embed postgres/*.sql
var postgresMigrations embed.FS

type postgresDriver struct{ driver.Driver }
type postgresConn struct{ driver.Conn }

func init() { sql.Register("switchboard-postgres", postgresDriver{stdlib.GetDefaultDriver()}) }
func (d postgresDriver) Open(name string) (driver.Conn, error) {
	c, e := d.Driver.Open(name)
	if e != nil {
		return nil, e
	}
	return postgresConn{c}, nil
}
func (c postgresConn) Prepare(q string) (driver.Stmt, error) { return c.Conn.Prepare(postgresSQL(q)) }
func (c postgresConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, postgresSQL(q))
}
func (c postgresConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, postgresSQL(q), args)
}
func (c postgresConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, postgresSQL(q), args)
}
func (c postgresConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
}
func (c postgresConn) Ping(ctx context.Context) error { return c.Conn.(driver.Pinger).Ping(ctx) }
func (c postgresConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}
func (c postgresConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}
func (c postgresConn) CheckNamedValue(v *driver.NamedValue) error {
	return c.Conn.(driver.NamedValueChecker).CheckNamedValue(v)
}

func postgresSQL(q string) string {
	var b strings.Builder
	ordinal := 0
	quoted := false
	comment := false
	for i := 0; i < len(q); i++ {
		ch := q[i]
		if comment {
			b.WriteByte(ch)
			if ch == '\n' {
				comment = false
			}
			continue
		}
		if !quoted && ch == '-' && i+1 < len(q) && q[i+1] == '-' {
			comment = true
			b.WriteByte(ch)
			continue
		}
		if ch == '\'' {
			b.WriteByte(ch)
			if quoted && i+1 < len(q) && q[i+1] == '\'' {
				i++
				b.WriteByte('\'')
				continue
			}
			quoted = !quoted
			continue
		}
		if ch == '?' && !quoted {
			j := i + 1
			for j < len(q) && q[j] >= '0' && q[j] <= '9' {
				j++
			}
			n := ordinal + 1
			if j > i+1 {
				n, _ = strconv.Atoi(q[i+1 : j])
			}
			ordinal = max(ordinal, n)
			fmt.Fprintf(&b, "$%d", n)
			i = j - 1
			continue
		}
		b.WriteByte(ch)
	}
	return strings.ReplaceAll(b.String(), " AS INTEGER)", " AS BIGINT)")
}
func IsPostgresURL(s string) bool {
	return strings.HasPrefix(s, "postgres://") || strings.HasPrefix(s, "postgresql://")
}
func IsPostgres(d *sql.DB) bool { _, ok := d.Driver().(postgresDriver); return ok }
func Describe(s string) string {
	if !IsPostgresURL(s) {
		return s
	}
	u, e := url.Parse(s)
	if e != nil {
		return "PostgreSQL"
	}
	return "postgresql://" + u.Host + u.Path
}
func openPostgres(uri string) (*sql.DB, error) {
	d, e := sql.Open("switchboard-postgres", uri)
	if e != nil {
		return nil, fmt.Errorf("configure PostgreSQL: %w", e)
	}
	d.SetMaxOpenConns(8)
	d.SetMaxIdleConns(8)
	d.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if e = d.PingContext(ctx); e == nil {
		e = migratePostgres(ctx, d)
	}
	if e != nil {
		d.Close()
		return nil, fmt.Errorf("open PostgreSQL (%s): %w", Describe(uri), redactPostgresError(uri, e))
	}
	return d, nil
}
func migratePostgres(ctx context.Context, d *sql.DB) error {
	tx, e := d.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(739216401)"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at BIGINT NOT NULL)"); e != nil {
		return e
	}
	entries, e := postgresMigrations.ReadDir("postgres")
	if e != nil {
		return e
	}
	for _, entry := range entries {
		var present bool
		if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)", entry.Name()).Scan(&present); e != nil {
			return e
		}
		if present {
			continue
		}
		body, e := postgresMigrations.ReadFile("postgres/" + entry.Name())
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, string(body)); e != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), e)
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES (?, ?)", entry.Name(), time.Now().UnixMilli()); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func redactPostgresError(uri string, err error) error {
	text := strings.ReplaceAll(err.Error(), uri, Describe(uri))
	if u, e := url.Parse(uri); e == nil && u.User != nil {
		if password, ok := u.User.Password(); ok && password != "" {
			text = strings.ReplaceAll(text, password, "[redacted]")
		}
	}
	return fmt.Errorf("%s", text)
}
