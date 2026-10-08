package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/ucgeorge/switchboard/internal/db"
	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
)

// Store wraps the database handle and generated queries.
type Store struct {
	DB *sql.DB
	Q  *sqlcgen.Queries
}

// NewStore builds a Store over an opened database.
func NewStore(d *sql.DB) *Store {
	return &Store{DB: d, Q: sqlcgen.New(d)}
}

// WithTx runs fn inside an immediate (write-locked) transaction.
func (s *Store) WithTx(ctx context.Context, fn func(q *sqlcgen.Queries) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if db.IsPostgres(s.DB) {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(739216402)"); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := fn(s.Q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ErrNotFound is returned when a lookup matches nothing.
var ErrNotFound = errors.New("not found")

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func nowMs() int64 { return time.Now().UnixMilli() }

func ptr[T any](v T) *T { return &v }

// strp always returns a non-nil pointer. Nullable-column filters in sqlc
// queries treat the empty string as "no filter"; a nil pointer would bind
// NULL, and NULL never compares equal to the empty string.
func strp(s string) *string { return &s }

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefI(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// MsTime converts unix milliseconds to time.Time.
func MsTime(ms int64) time.Time { return time.UnixMilli(ms) }

// MsPtrTime converts an optional unix-millisecond value.
func MsPtrTime(p *int64) *time.Time {
	if p == nil {
		return nil
	}
	t := time.UnixMilli(*p)
	return &t
}

// SplitList parses a comma/space separated list into trimmed, non-empty items.
func SplitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// JoinList is the inverse of SplitList.
func JoinList(items []string) string { return strings.Join(items, ",") }

// likePattern wraps a search term for LIKE; empty stays empty (no filter).
func likePattern(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return "%" + s + "%"
}
