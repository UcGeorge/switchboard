package db_test

import (
	"context"
	"database/sql"
	"github.com/ucgeorge/switchboard/internal/db"
	"github.com/ucgeorge/switchboard/internal/ids"
	"github.com/ucgeorge/switchboard/internal/testdb"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeBackup(t *testing.T) {
	ctx := context.Background()
	source := os.Getenv("SWITCHBOARD_TEST_POSTGRES_URL")
	var d *sql.DB
	if source != "" {
		if _, e := exec.LookPath("pg_dump"); e != nil {
			t.Skip("pg_dump unavailable")
		}
		admin, e := sql.Open("pgx", source)
		if e != nil {
			t.Fatal(e)
		}
		defer admin.Close()
		name := "backup_" + ids.Random(16)
		if _, e = admin.Exec(`CREATE DATABASE "` + name + `"`); e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(source)
		u.Path = "/" + name
		source = u.String()
		d, e = db.Open(source)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { d.Close(); admin.Exec(`DROP DATABASE "` + name + `"`) }()
	} else {
		d = testdb.Open(t)
	}
	file := filepath.Join(t.TempDir(), "backup")
	if e := db.Backup(ctx, d, source, file); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	magic := "SQLite format 3"
	if db.IsPostgres(d) {
		magic = "PGDMP"
	}
	if !strings.HasPrefix(string(b), magic) {
		t.Fatal("invalid backup format")
	}
}
