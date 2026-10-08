package app

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFirstRunLogCreatesDataDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "nested", "switchboard.log")
	f, err := openLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("startup\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	f, err = openLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("restart\n")
	f.Close()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "startup\nrestart\n" {
		t.Fatalf("log: %q %v", b, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Dir(path))
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("directory permissions: %v", info.Mode())
		}
		info, _ = os.Stat(path)
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("log permissions: %v", info.Mode())
		}
	}
}

func TestFreshServerStartsAndStops(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	path := filepath.Join(t.TempDir(), "never-created", "instance.db")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- Run(ctx, Config{Addr: addr, DBPath: path, Headless: true}) }()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		select {
		case err := <-result:
			t.Fatalf("startup failed: %v", err)
		default:
		}
		resp, err := client.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				started = true
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !started {
		t.Fatal("server never became healthy")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not complete")
	}
}
