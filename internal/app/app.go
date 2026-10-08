// Package app wires the database, domain service, HTTP surfaces and the TUI
// into the running server.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/ucgeorge/switchboard/internal/api"
	"github.com/ucgeorge/switchboard/internal/control"
	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db"
	"github.com/ucgeorge/switchboard/internal/mcpserver"
	"github.com/ucgeorge/switchboard/internal/oauth"
	"github.com/ucgeorge/switchboard/internal/tui"
	"github.com/ucgeorge/switchboard/internal/version"
	"github.com/ucgeorge/switchboard/internal/web"
)

// Config controls a server run.
type Config struct {
	Addr     string
	DBPath   string
	Headless bool
	Open     bool
	LogLevel string
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// Run starts Switchboard and blocks until ctx is cancelled or the TUI exits.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8080"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = db.DefaultPath()
	}

	dataDir := filepath.Dir(cfg.DBPath)
	if db.IsPostgresURL(cfg.DBPath) {
		dataDir = db.DefaultDataDir()
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	// In TUI mode logs go to a file so they do not corrupt the screen.
	var logOut io.Writer = os.Stderr
	logPath := ""
	if !cfg.Headless {
		logPath = filepath.Join(dataDir, "switchboard.log")
		f, err := openLogFile(logPath)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		defer f.Close()
		logOut = f
	}
	log := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	svc, err := core.New(database, log)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		svc.Close()
		return fmt.Errorf("listen on %s: %w (is another instance running? try --addr)", cfg.Addr, err)
	}
	svc.ListenAddr = ln.Addr().String()

	generatedPassword, err := svc.EnsurePassword(ctx)
	if err != nil {
		ln.Close()
		svc.Close()
		return err
	}

	webSrv, err := web.New(svc, log, db.Describe(cfg.DBPath))
	if err != nil {
		ln.Close()
		svc.Close()
		return err
	}
	mux := http.NewServeMux()
	api.New(svc, log).Register(mux)
	(&control.Server{Service: svc, DBPath: cfg.DBPath}).Register(mux)
	mcpSrv := mcpserver.New(svc, log)
	mux.Handle("/mcp", mcpSrv)
	mux.Handle("/mcp/", mcpSrv)
	oauth.New(svc, log, webSrv.RequireSession).Register(mux)
	webSrv.Register(mux)

	server := &http.Server{
		Handler:           recoverer(log, accessLog(log, mux)),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: streaming completions, long-poll claims and SSE
		// are all long-lived by design.
	}

	if err := svc.Start(ctx); err != nil {
		ln.Close()
		svc.Close()
		return err
	}
	serveErr := make(chan error, 1)
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	base := svc.LocalURL()
	info := tui.Info{
		Version: version.Version, Instance: svc.Settings().InstanceName,
		DashboardURL: base, OpenAIURL: base + "/v1", MCPURL: base + "/mcp",
		DBPath: db.Describe(cfg.DBPath), LogPath: logPath, Password: generatedPassword,
	}
	if tok, err := svc.CreateLoginToken(ctx, 15*time.Minute); err == nil {
		info.LoginURL = svc.LoginLinkURL(tok)
	}
	log.Info("switchboard started", "addr", svc.ListenAddr, "db", db.Describe(cfg.DBPath), "version", version.Version)

	if cfg.Open {
		target := info.LoginURL
		if target == "" {
			target = info.DashboardURL
		}
		_ = tui.OpenBrowser(target)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var runErr error
	if cfg.Headless {
		printHeadless(info)
		select {
		case <-runCtx.Done():
		case err := <-serveErr:
			runErr = err
		}
	} else {
		done := make(chan error, 1)
		go func() { done <- tui.Run(runCtx, svc, info) }()
		select {
		case err := <-done:
			runErr = err
		case err := <-serveErr:
			runErr = err
			cancel()
			<-done
		}
	}

	shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_ = server.Shutdown(shutdownCtx)
	_ = server.Close()
	svc.Close()
	log.Info("switchboard stopped")
	return runErr
}

func printHeadless(info tui.Info) {
	fmt.Printf("%s (switchboard %s) is running\n\n", info.Instance, info.Version)
	fmt.Printf("  Dashboard   %s\n", info.DashboardURL)
	if info.LoginURL != "" {
		fmt.Printf("  Login link  %s  (one-time, 15 min)\n", info.LoginURL)
	}
	fmt.Printf("  OpenAI API  %s\n", info.OpenAIURL)
	fmt.Printf("  MCP         %s\n", info.MCPURL)
	fmt.Printf("  Database    %s\n", info.DBPath)
	if info.Password != "" {
		fmt.Printf("\n  First run: dashboard password is %s\n  Change it with `switchboard auth set-password`.\n", info.Password)
	}
	fmt.Println("\nPress Ctrl+C to stop.")
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush and Unwrap keep streaming (SSE) working through the wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func accessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start).Round(time.Millisecond))
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				log.Error("panic in handler", "path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// openLogFile also works when invoked on a completely new data directory.
func openLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}
