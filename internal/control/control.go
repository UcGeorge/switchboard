// Package control exposes authenticated administration for remote CLI clients.
// Commands run as isolated subprocesses of the same binary, never through a shell.
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db"
	"github.com/ucgeorge/switchboard/internal/secret"
)

const TokenHashSetting = "admin.control_token_hash"

type Request struct {
	Args []string `json:"args"`
}
type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}
type Server struct {
	Service *core.Service
	DBPath  string
	Binary  string
}

// Allowed restricts administration to explicit instance operations. Client-side
// file reads and registration commands are never executed on the server.
func Allowed(args []string) bool {
	if len(args) == 0 || len(args) > 100 {
		return false
	}
	allowed := map[string]string{
		"keys": "list ls create revoke delete rm", "tokens": "list ls create revoke delete rm",
		"channels": "list ls show drain resume close delete", "requests": "list ls pending show answer cancel requeue",
		"conversations": "list ls show", "events": "list ls", "settings": "list ls get set",
		"auth": "set-password login-link logout-all", "db": "path prune vacuum", "stats": "", "send": "",
	}
	group := args[0]
	ops, ok := allowed[group]
	if !ok {
		return false
	}
	if group != "stats" && group != "send" {
		if len(args) < 2 || !strings.Contains(" "+ops+" ", " "+args[1]+" ") {
			return false
		}
	}
	for _, a := range args {
		if len(a) > 1<<20 {
			return false
		}
		for _, f := range []string{"--db", "--database-url", "--addr", "--url", "--admin-token", "--file", "--headless", "--open"} {
			if a == f || strings.HasPrefix(a, f+"=") {
				return false
			}
		}
	}
	if group == "auth" && args[1] == "set-password" {
		found := false
		for _, a := range args {
			if strings.HasPrefix(a, "--password=") {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *Server) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		hash, ok := s.Service.RawSetting(TokenHashSetting)
		if configured := os.Getenv("SWITCHBOARD_CONTROL_TOKEN"); configured != "" {
			hash = secret.Hash(configured)
			ok = true
		}
		if !ok || token == "" || !secret.Equal(hash, secret.Hash(token)) {
			http.Error(w, "invalid administration token", http.StatusUnauthorized)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			http.Error(w, "browser administration requests are not allowed", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/commands", s.authenticate(s.command))
	mux.HandleFunc("GET /admin/backup", s.authenticate(s.backup))
}
func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	var in Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in); err != nil || !Allowed(in.Args) {
		http.Error(w, "unsupported administration command", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	binary := s.Binary
	if binary == "" {
		var err error
		binary, err = os.Executable()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	args := append([]string{"--addr", r.Host}, in.Args...)
	cmd := exec.CommandContext(ctx, binary, args...)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "SWITCHBOARD_DB=") && !strings.HasPrefix(v, "DATABASE_URL=") && !strings.HasPrefix(v, "SWITCHBOARD_DATABASE_URL=") && !strings.HasPrefix(v, "SWITCHBOARD_URL=") && !strings.HasPrefix(v, "SWITCHBOARD_ADMIN_TOKEN=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	if db.IsPostgresURL(s.DBPath) {
		cmd.Env = append(cmd.Env, "SWITCHBOARD_DATABASE_URL="+s.DBPath)
	} else {
		cmd.Env = append(cmd.Env, "SWITCHBOARD_DB="+s.DBPath)
	}
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = 1
		if stderr.Len() == 0 {
			fmt.Fprint(&stderr, err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(Result{stdout.String(), stderr.String(), code})
}

// Prevent an unbounded command listing from exhausting the server's memory.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 16<<20 {
		return 0, fmt.Errorf("command output exceeds 16 MiB; use pagination")
	}
	return b.Buffer.Write(p)
}
func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	dir, err := os.MkdirTemp("", "switchboard-backup-")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer os.RemoveAll(dir)
	path := dir + "/backup.db"
	if err = db.Backup(r.Context(), s.Service.DB, s.DBPath, path); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	io.Copy(w, f)
}
