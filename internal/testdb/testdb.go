// Package testdb isolates backend tests in a temporary SQLite file or PostgreSQL schema.
package testdb

import (
	"database/sql"
	"github.com/ucgeorge/switchboard/internal/db"
	"github.com/ucgeorge/switchboard/internal/ids"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func Open(t *testing.T) *sql.DB {
	t.Helper()
	uri := os.Getenv("SWITCHBOARD_TEST_POSTGRES_URL")
	if uri == "" {
		d, e := db.Open(filepath.Join(t.TempDir(), "test.db"))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { d.Close() })
		return d
	}
	admin, e := sql.Open("pgx", uri)
	if e != nil {
		t.Fatal(e)
	}
	schema := "test_" + ids.Random(20)
	if _, e = admin.Exec(`CREATE SCHEMA "` + schema + `"`); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	u, e := url.Parse(uri)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	d, e := db.Open(u.String())
	if e != nil {
		admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`)
		admin.Close()
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close(); admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); admin.Close() })
	return d
}
