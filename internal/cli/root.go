// Package cli is the switchboard command line. Management commands work
// directly against the SQLite database, so they behave the same whether or
// not the server is running; the running server picks changes up immediately.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ucgeorge/switchboard/internal/app"
	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db"
	"github.com/ucgeorge/switchboard/internal/version"
)

type globals struct {
	dbPath     string
	addr       string
	json       bool
	url        string
	adminToken string
}

var g globals

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Execute runs the CLI.
func Execute() int {
	root := newRoot()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	var serve serveFlags
	root := &cobra.Command{
		Use:   "switchboard",
		Short: "OpenAI-compatible API served by MCP-connected agents",
		Long: `Switchboard exposes an OpenAI-compatible API whose requests are queued and
answered by agents connected over MCP. Run it with no arguments to start the
server with a terminal status screen and the web dashboard.`,
		SilenceUsage:      true,
		PersistentPreRunE: remotePreRun,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), serve)
		},
	}
	root.PersistentFlags().StringVar(&g.dbPath, "db", envOr("SWITCHBOARD_DB", ""), "path to the SQLite database (default ~/.switchboard/switchboard.db)")
	root.PersistentFlags().StringVar(&g.addr, "addr", envOr("SWITCHBOARD_ADDR", "127.0.0.1:8080"), "listen address of the server")
	root.PersistentFlags().BoolVar(&g.json, "json", false, "print machine-readable JSON")
	root.PersistentFlags().StringVar(&g.url, "url", envOr("SWITCHBOARD_URL", ""), "remote instance base URL")
	root.PersistentFlags().StringVar(&g.adminToken, "admin-token", envOr("SWITCHBOARD_ADMIN_TOKEN", ""), "remote administration token (prefer environment variable)")
	addServeFlags(root, &serve)

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the server (API, MCP endpoint, dashboard) with the terminal UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), serve)
		},
	}
	addServeFlags(serveCmd, &serve)

	root.AddCommand(serveCmd, keysCmd(), tokensCmd(), channelsCmd(), requestsCmd(), conversationsCmd(), sendCmd(),
		eventsCmd(), settingsCmd(), authCmd(), mcpCmd(), skillCmd(), statsCmd(), dbCmd(),
		&cobra.Command{Use: "version", Short: "Print the version", Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("switchboard", version.String())
		}})
	return root
}

type serveFlags struct {
	headless bool
	open     bool
	logLevel string
}

func addServeFlags(cmd *cobra.Command, f *serveFlags) {
	cmd.Flags().BoolVar(&f.headless, "headless", false, "run without the terminal UI (log to stderr)")
	cmd.Flags().BoolVar(&f.open, "open", false, "open the dashboard in a browser on start")
	cmd.Flags().StringVar(&f.logLevel, "log-level", envOr("SWITCHBOARD_LOG_LEVEL", "info"), "log level: debug, info, warn, error")
}

func runServe(ctx context.Context, f serveFlags) error {
	headless := f.headless
	if !headless {
		// Without a terminal there is nothing to draw on.
		if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			headless = true
		}
	}
	return app.Run(ctx, app.Config{Addr: g.addr, DBPath: dbPath(), Headless: headless, Open: f.open, LogLevel: f.logLevel})
}

func dbPath() string {
	if g.dbPath != "" {
		return g.dbPath
	}
	return db.DefaultPath()
}

// withService opens the database for a one-shot command.
func withService(fn func(ctx context.Context, svc *core.Service) error) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		return runWithService(cmd.Context(), fn)
	}
}

func runWithService(ctx context.Context, fn func(ctx context.Context, svc *core.Service) error) error {
	database, err := db.Open(dbPath())
	if err != nil {
		return fmt.Errorf("open database %s: %w", dbPath(), err)
	}
	defer database.Close()
	svc, err := core.New(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	svc.ListenAddr = g.addr
	defer svc.Close()
	return fn(ctx, svc)
}

// output helpers

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type table struct {
	w *tabwriter.Writer
}

func newTable(headers ...string) *table {
	t := &table{w: tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)}
	fmt.Fprintln(t.w, strings.Join(headers, "\t"))
	return t
}

func (t *table) row(cols ...any) {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = fmt.Sprint(c)
	}
	fmt.Fprintln(t.w, strings.Join(parts, "\t"))
}

func (t *table) flush() { _ = t.w.Flush() }

func fmtMs(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
}

func fmtMsPtr(p *int64) string {
	if p == nil {
		return "-"
	}
	return fmtMs(*p)
}

func fmtDur(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return (time.Duration(ms) * time.Millisecond).Round(time.Millisecond).String()
}

func agoMs(ms int64) string {
	d := time.Since(time.UnixMilli(ms)).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
