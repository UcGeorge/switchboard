package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db"
)

func eventsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "events", Aliases: []string{"event", "log"}, Short: "Read the event log"}

	var f core.EventFilter
	list := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List recent events, newest first",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				rows, _, err := svc.ListEvents(ctx, f)
				if err != nil {
					return err
				}
				if g.json {
					return printJSON(rows)
				}
				t := newTable("TIME", "LEVEL", "KIND", "MESSAGE")
				for _, e := range rows {
					t.row(time.UnixMilli(e.Ts).Format("01-02 15:04:05"), e.Level, e.Kind, clip(e.Message, 100))
				}
				t.flush()
				return nil
			})
		},
	}
	list.Flags().StringVar(&f.Kind, "kind", "", "filter by kind (e.g. request.completed)")
	list.Flags().StringVar(&f.Level, "level", "", "filter by level (info, warn, error)")
	list.Flags().StringVar(&f.RequestID, "request", "", "filter by request id")
	list.Flags().StringVar(&f.ChannelID, "channel", "", "filter by channel id")
	list.Flags().Int64VarP(&f.Limit, "limit", "l", 40, "maximum rows")
	cmd.AddCommand(list)

	cmd.AddCommand(&cobra.Command{
		Use: "tail", Short: "Follow the event log (Ctrl+C to stop)",
		RunE: withService(func(ctx context.Context, svc *core.Service) error {
			var last int64
			if recent, err := svc.RecentEvents(ctx, 10); err == nil {
				for i := len(recent) - 1; i >= 0; i-- {
					e := recent[i]
					fmt.Printf("%s  %-5s %-24s %s\n", time.UnixMilli(e.Ts).Format("15:04:05"), e.Level, e.Kind, e.Message)
					last = e.ID
				}
			}
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-tick.C:
				}
				recent, err := svc.RecentEvents(ctx, 200)
				if err != nil {
					continue
				}
				for i := len(recent) - 1; i >= 0; i-- {
					e := recent[i]
					if e.ID <= last {
						continue
					}
					fmt.Printf("%s  %-5s %-24s %s\n", time.UnixMilli(e.Ts).Format("15:04:05"), e.Level, e.Kind, e.Message)
					last = e.ID
				}
			}
		}),
	})
	return cmd
}

func settingsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "settings", Aliases: []string{"config"}, Short: "View and change runtime settings"}
	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List settings with their current values",
		RunE: withService(func(ctx context.Context, svc *core.Service) error {
			vals := svc.SettingValues()
			if g.json {
				return printJSON(vals)
			}
			t := newTable("KEY", "VALUE", "DESCRIPTION")
			for _, d := range core.SettingDefs {
				t.row(d.Key, orDash(vals[d.Key]), clip(d.Description, 80))
			}
			t.flush()
			return nil
		}),
	})
	cmd.AddCommand(&cobra.Command{
		Use: "get <key>", Short: "Print one setting", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				v, ok := svc.SettingValues()[args[0]]
				if !ok {
					return fmt.Errorf("unknown setting %q (see `switchboard settings list`)", args[0])
				}
				fmt.Println(v)
				return nil
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "set <key> <value>", Short: "Change a setting (the running server applies it within seconds)", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				if err := svc.SetSetting(ctx, args[0], args[1]); err != nil {
					return err
				}
				fmt.Printf("%s = %s\n", args[0], args[1])
				return nil
			})
		},
	})
	return cmd
}

func authCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Dashboard access: password, login links, sessions"}
	cmd.AddCommand(adminTokenCmd())

	var password string
	setPw := &cobra.Command{
		Use: "set-password", Short: "Set the dashboard password",
		RunE: func(cmd *cobra.Command, args []string) error {
			pw := password
			if pw == "" {
				fmt.Fprint(os.Stderr, "New dashboard password (min 8 chars): ")
				if term.IsTerminal(os.Stdin.Fd()) {
					b, err := term.ReadPassword(os.Stdin.Fd())
					fmt.Fprintln(os.Stderr)
					if err != nil {
						return err
					}
					pw = string(b)
				} else {
					line, err := bufio.NewReader(os.Stdin).ReadString('\n')
					if err != nil && line == "" {
						return errors.New("no password provided")
					}
					pw = strings.TrimRight(line, "\r\n")
				}
			}
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				if err := svc.SetPassword(ctx, pw); err != nil {
					return err
				}
				fmt.Println("dashboard password updated")
				return nil
			})
		},
	}
	setPw.Flags().StringVar(&password, "password", "", "the new password (omit to be prompted)")
	cmd.AddCommand(setPw)

	var ttl time.Duration
	link := &cobra.Command{
		Use: "login-link", Short: "Print a one-time dashboard login URL",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				tok, err := svc.CreateLoginToken(ctx, ttl)
				if err != nil {
					return err
				}
				fmt.Println(svc.LoginLinkURL(tok))
				return nil
			})
		},
	}
	link.Flags().DurationVar(&ttl, "ttl", 15*time.Minute, "how long the link stays valid")
	cmd.AddCommand(link)

	cmd.AddCommand(&cobra.Command{
		Use: "logout-all", Short: "Invalidate every dashboard session",
		RunE: withService(func(ctx context.Context, svc *core.Service) error {
			n, err := svc.RevokeAllSessions(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("signed out %d session(s)\n", n)
			return nil
		}),
	})
	return cmd
}

func statsCmd() *cobra.Command {
	var window time.Duration
	cmd := &cobra.Command{
		Use: "stats", Short: "Show queue depth, throughput and latency",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				o, err := svc.Overview(ctx, window)
				if err != nil {
					return err
				}
				if g.json {
					return printJSON(o)
				}
				fmt.Printf("Now        %d queued · %d in flight · %d/%d channels online · %d active keys\n", o.Queued, o.InFlight, o.ChannelsOnline, o.ChannelsOpen, o.KeysActive)
				fmt.Printf("Last %-5s %d requests (%.2f/min) · %d completed · %d failed · %d dropped · success %.1f%%\n", shortDur(window), o.Total, o.RPM, o.Completed, o.Failed, o.Dropped, o.SuccessRate)
				fmt.Printf("Latency    p50 %s · p95 %s · p99 %s   TTFT p50 %s   queue p50 %s\n", fmtDur(o.P50Ms), fmtDur(o.P95Ms), fmtDur(o.P99Ms), fmtDur(o.TTFTP50Ms), fmtDur(o.QueueP50Ms))
				fmt.Printf("Tokens     %d prompt · %d completion\n", o.PromptTokens, o.CompletionTokens)
				if len(o.Models) > 0 {
					fmt.Println()
					t := newTable("MODEL", "REQUESTS", "PROMPT", "COMPLETION")
					for _, m := range o.Models {
						t.row(m.Model, m.Total, m.PromptTokens, m.CompletionTokens)
					}
					t.flush()
				}
				return nil
			})
		},
	}
	cmd.Flags().DurationVarP(&window, "window", "w", time.Hour, "time window (e.g. 15m, 1h, 24h)")
	return cmd
}

// shortDur renders round durations compactly: 15m, 1h, 7d.
func shortDur(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

func dbCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "db", Short: "Database maintenance"}
	cmd.AddCommand(&cobra.Command{
		Use: "path", Short: "Print the database path",
		Run: func(cmd *cobra.Command, args []string) { fmt.Println(db.Describe(dbPath())) },
	})
	cmd.AddCommand(&cobra.Command{
		Use: "backup <file>", Short: "Write a consistent snapshot of the database to a file", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(args[0]); err == nil {
				return fmt.Errorf("%s already exists", args[0])
			}
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				if err := db.Backup(ctx, svc.DB, dbPath(), args[0]); err != nil {
					return err
				}
				fmt.Println("backup written to", args[0])
				return nil
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "prune", Short: "Delete finished requests and events older than the retention period",
		RunE: withService(func(ctx context.Context, svc *core.Service) error {
			svc.Prune(ctx)
			fmt.Printf("pruned records older than %d day(s)\n", svc.Settings().RetentionDays)
			return nil
		}),
	})
	cmd.AddCommand(&cobra.Command{
		Use: "vacuum", Short: "Reclaim free space in the database file",
		RunE: withService(func(ctx context.Context, svc *core.Service) error {
			if _, err := svc.DB.ExecContext(ctx, "VACUUM"); err != nil {
				return err
			}
			fmt.Println("vacuum complete")
			return nil
		}),
	})
	return cmd
}
