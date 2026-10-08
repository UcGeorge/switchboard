package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ucgeorge/switchboard/internal/core"
)

func channelsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "channels", Aliases: []string{"channel", "ch"}, Short: "Inspect and control agent channels"}

	var all bool
	list := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List channels",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				chans, err := svc.ListChannels(ctx)
				if err != nil {
					return err
				}
				if g.json {
					return printJSON(chans)
				}
				t := newTable("ID", "NAME", "STATUS", "MODELS", "IN FLIGHT", "SERVED", "FAILED", "AVG LATENCY", "LAST SEEN")
				for _, c := range chans {
					if c.ClosedAt != nil && !all {
						continue
					}
					status := c.Status
					if c.ClosedAt != nil {
						status = "closed"
					}
					inflight, _ := svc.ChannelInFlight(ctx, c.ID)
					t.row(c.ID, c.Name, status, c.Models, fmt.Sprintf("%d/%d", inflight, c.Concurrency), c.ServedCount, c.FailedCount, fmtDur(core.AvgLatencyMs(c)), agoMs(c.LastSeenAt))
				}
				t.flush()
				return nil
			})
		},
	}
	list.Flags().BoolVarP(&all, "all", "a", false, "include closed channels")
	cmd.AddCommand(list)

	cmd.AddCommand(&cobra.Command{
		Use: "show <id>", Short: "Show one channel", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				c, err := svc.GetChannel(ctx, args[0])
				if err != nil {
					return err
				}
				if g.json {
					return printJSON(c)
				}
				inflight, _ := svc.ChannelInFlight(ctx, c.ID)
				fmt.Printf("%s  %s\n", c.ID, c.Name)
				fmt.Printf("  status       %s\n  models       %s\n  concurrency  %d (in flight %d)\n", c.Status, c.Models, c.Concurrency, inflight)
				fmt.Printf("  served       %d (failed %d, avg %s)\n", c.ServedCount, c.FailedCount, fmtDur(core.AvgLatencyMs(c)))
				fmt.Printf("  opened       %s\n  last seen    %s\n", fmtMs(c.CreatedAt), agoMs(c.LastSeenAt))
				if c.ClosedAt != nil {
					fmt.Printf("  closed       %s\n", fmtMsPtr(c.ClosedAt))
				}
				return nil
			})
		},
	})

	action := func(use, short, done string, fn func(ctx context.Context, svc *core.Service, id string) error) *cobra.Command {
		return &cobra.Command{
			Use: use + " <id>", Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
					if err := fn(ctx, svc, args[0]); err != nil {
						return err
					}
					fmt.Println(done, args[0])
					return nil
				})
			},
		}
	}
	cmd.AddCommand(
		action("drain", "Stop a channel from claiming new requests", "draining", func(ctx context.Context, svc *core.Service, id string) error {
			return svc.SetChannelDraining(ctx, id, true)
		}),
		action("resume", "Let a draining channel claim again", "resumed", func(ctx context.Context, svc *core.Service, id string) error {
			return svc.SetChannelDraining(ctx, id, false)
		}),
		action("close", "Close a channel and requeue its in-flight requests", "closed", func(ctx context.Context, svc *core.Service, id string) error {
			return svc.CloseChannel(ctx, id, "", "closed from CLI")
		}),
		action("delete", "Delete a channel record", "deleted", func(ctx context.Context, svc *core.Service, id string) error {
			return svc.DeleteChannel(ctx, id)
		}),
	)
	return cmd
}
