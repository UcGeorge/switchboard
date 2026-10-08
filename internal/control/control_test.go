package control

import (
	"context"
	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db"
	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/secret"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowlist(t *testing.T) {
	for _, a := range [][]string{{"keys", "list", "--json"}, {"send", "hello"}, {"requests", "answer", "req_x", "--text=hello"}, {"auth", "set-password", "--password=abcdefgh"}} {
		if !Allowed(a) {
			t.Errorf("valid command rejected: %v", a)
		}
	}
	for _, a := range [][]string{{}, {"serve"}, {"db", "backup", "/etc/file"}, {"keys", "list", "--db=/etc/foo"}, {"mcp", "add", "claude-code"}, {"requests", "answer", "x", "--file=/etc/passwd"}, {"auth", "set-password"}, {"events", "tail"}} {
		if Allowed(a) {
			t.Errorf("unsafe command accepted: %v", a)
		}
	}
}
func TestAuthentication(t *testing.T) {
	d, e := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	s, e := core.New(d, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.Q.SetSetting(context.Background(), sqlcgen.SetSettingParams{Key: TokenHashSetting, Value: secret.Hash("admin-secret")})
	// Refresh service cache from the database.
	s.Close()
	s, e = core.New(d, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	mux := http.NewServeMux()
	(&Server{Service: s, DBPath: "unused"}).Register(mux)
	for _, tc := range []struct {
		token, origin string
		want          int
	}{{"", "", 401}, {"wrong", "", 401}, {"admin-secret", "https://evil.example", 403}, {"admin-secret", "", 400}} {
		r := httptest.NewRequest("POST", "/admin/commands", strings.NewReader(`{"args":["serve"]}`))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("status %d want %d", w.Code, tc.want)
		}
	}
}
