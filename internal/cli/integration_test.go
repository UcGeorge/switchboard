package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRemoteInstanceCommands(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "switchboard")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "../../cmd/switchboard")
	if b, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, b)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := ln.Addr().String()
	ln.Close()
	dbpath := filepath.Join(tmp, "new", "server.db")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := exec.CommandContext(ctx, bin, "serve", "--headless", "--addr", addr, "--db", dbpath)
	server.Env = append(os.Environ(), "SWITCHBOARD_NO_UPDATE_CHECK=1", "SWITCHBOARD_CONTROL_TOKEN=integration-admin", "SWITCHBOARD_URL=", "SWITCHBOARD_DB=")
	if e = server.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cancel(); server.Wait() }()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, e := client.Get("http://" + addr + "/healthz")
		if e == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start")
		}
		time.Sleep(30 * time.Millisecond)
	}
	localData := filepath.Join(tmp, "client-must-not-exist")
	run := func(args ...string) string {
		t.Helper()
		c := exec.CommandContext(ctx, bin, append([]string{"--url", "http://" + addr, "--admin-token", "integration-admin"}, args...)...)
		c.Env = append(os.Environ(), "SWITCHBOARD_NO_UPDATE_CHECK=1", "SWITCHBOARD_DATA_DIR="+localData, "SWITCHBOARD_DB=", "SWITCHBOARD_URL=")
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("%v: %v %s", args, e, b)
		}
		return string(b)
	}
	if out := run("mcp", "url"); strings.TrimSpace(out) != "http://"+addr+"/mcp" {
		t.Fatal(out)
	}
	var key map[string]any
	if e = json.Unmarshal([]byte(run("keys", "create", "--name", "remote-app", "--json")), &key); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(run("keys", "list"), "remote-app") {
		t.Fatal("remote key not persisted")
	}
	run("settings", "set", "lease_seconds", "71")
	if strings.TrimSpace(run("settings", "get", "lease_seconds")) != "71" {
		t.Fatal("setting not updated")
	}
	if out := run("mcp", "config", "cursor", "--json"); !strings.Contains(out, addr) || !strings.Contains(out, "sba_") {
		t.Fatal(out)
	}
	if out := run("mcp", "add", "claude-code", "--print"); !strings.Contains(out, addr) {
		t.Fatal(out)
	}
	if out := run("mcp", "config", "codex", "--token", "sba_existing"); !strings.Contains(out, addr) {
		t.Fatal(out)
	}
	run("stats")
	run("channels", "list")
	run("requests", "pending")
	run("conversations", "list")
	run("events", "list")
	run("auth", "set-password", "--password=remote-test-password")
	if out := run("auth", "login-link"); !strings.Contains(out, addr) {
		t.Fatal(out)
	}
	backup := filepath.Join(tmp, "download.db")
	run("db", "backup", backup)
	b, e := os.ReadFile(backup)
	if e != nil || !strings.HasPrefix(string(b), "SQLite format 3") {
		t.Fatal("invalid downloaded backup")
	}
	if _, e = os.Stat(localData); !os.IsNotExist(e) {
		t.Fatal("remote commands created local instance data")
	}
	c := exec.Command(bin, "--url", "http://"+addr, "--admin-token", "wrong", "keys", "list")
	if b, e = c.CombinedOutput(); e == nil || !strings.Contains(string(b), "401") {
		t.Fatalf("wrong token accepted: %s", b)
	}
}
