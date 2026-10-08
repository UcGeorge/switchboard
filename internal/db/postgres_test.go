package db

import (
	"strings"
	"testing"
)

func TestPostgresPlaceholders(t *testing.T) {
	for _, tc := range []struct{ in, out string }{{"SELECT ?1, ?1, ?2", "SELECT $1, $1, $2"}, {"SELECT ?, '?', ?", "SELECT $1, '?', $2"}, {"-- name ?\nSELECT CAST(? AS INTEGER)", "-- name ?\nSELECT CAST($1 AS BIGINT)"}} {
		if got := postgresSQL(tc.in); got != tc.out {
			t.Errorf("%q -> %q want %q", tc.in, got, tc.out)
		}
	}
}
func TestPostgresDescriptionRedactsSecrets(t *testing.T) {
	got := Describe("postgresql://user:secret@localhost:5432/app?password=secret&sslmode=require")
	if strings.Contains(got, "secret") || strings.Contains(got, "user") {
		t.Fatal(got)
	}
}
