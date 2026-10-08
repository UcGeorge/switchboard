package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ucgeorge/switchboard/internal/core"
)

func keysCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "keys", Aliases: []string{"key"}, Short: "Manage API keys used by OpenAI-compatible callers"}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List API keys",
		RunE: withService(func(ctx context.Context, svc *core.Service) error {
			keys, err := svc.ListAPIKeys(ctx)
			if err != nil {
				return err
			}
			if g.json {
				return printJSON(keys)
			}
			t := newTable("ID", "NAME", "KEY", "STATUS", "RPM", "MODELS", "REQUESTS", "LAST USED")
			for _, k := range keys {
				status := "active"
				if k.RevokedAt != nil {
					status = "revoked"
				}
				rpm := "default"
				if k.RateLimitRpm > 0 {
					rpm = fmt.Sprint(k.RateLimitRpm)
				}
				t.row(k.ID, k.Name, k.KeyPrefix, status, rpm, orDash(k.AllowedModels), k.RequestCount, fmtMsPtr(k.LastUsedAt))
			}
			t.flush()
			return nil
		}),
	})

	var in core.APIKeyInput
	var models string
	create := &cobra.Command{
		Use: "create", Short: "Create an API key (the key is printed once)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if in.Name == "" && len(args) > 0 {
				in.Name = args[0]
			}
			in.AllowedModels = core.SplitList(models)
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				k, plain, err := svc.CreateAPIKey(ctx, in)
				if err != nil {
					return err
				}
				if g.json {
					return printJSON(map[string]any{"id": k.ID, "name": k.Name, "key": plain, "base_url": svc.LocalURL() + "/v1"})
				}
				fmt.Printf("Created API key %q (%s)\n\n  %s\n\nStore it now; it is not shown again.\n\n", k.Name, k.ID, plain)
				fmt.Printf("  export OPENAI_BASE_URL=%s/v1\n  export OPENAI_API_KEY=%s\n", svc.LocalURL(), plain)
				return nil
			})
		},
	}
	create.Flags().StringVarP(&in.Name, "name", "n", "", "name for the key (required)")
	create.Flags().Int64Var(&in.RateLimitRPM, "rpm", 0, "requests per minute (0 = default)")
	create.Flags().StringVar(&models, "models", "", "comma-separated allowed models/globs (empty = any)")
	create.Flags().Int64Var(&in.TimeoutSeconds, "timeout", 0, "request timeout in seconds (0 = default)")
	create.Flags().Int64Var(&in.Priority, "priority", 0, "queue priority (higher is claimed first)")
	create.Flags().StringVar(&in.PinnedChannelID, "pin", "", "route only to this channel id")
	cmd.AddCommand(create)

	cmd.AddCommand(&cobra.Command{
		Use: "revoke <id>", Short: "Revoke an API key", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				if err := svc.RevokeAPIKey(ctx, args[0]); err != nil {
					return err
				}
				fmt.Println("revoked", args[0])
				return nil
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "delete <id>", Aliases: []string{"rm"}, Short: "Delete an API key record", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				if err := svc.DeleteAPIKey(ctx, args[0]); err != nil {
					return err
				}
				fmt.Println("deleted", args[0])
				return nil
			})
		},
	})
	return cmd
}

func tokensCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tokens", Aliases: []string{"token"}, Short: "Manage agent tokens accepted by the MCP endpoint"}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List agent tokens (static and OAuth-issued)",
		RunE: withService(func(ctx context.Context, svc *core.Service) error {
			toks, err := svc.ListAgentTokens(ctx)
			if err != nil {
				return err
			}
			if g.json {
				return printJSON(toks)
			}
			t := newTable("ID", "NAME", "KIND", "TOKEN", "STATUS", "LAST USED", "EXPIRES")
			for _, tk := range toks {
				status := "active"
				if tk.RevokedAt != nil {
					status = "revoked"
				}
				t.row(tk.ID, tk.Name, tk.Kind, tk.TokenPrefix, status, fmtMsPtr(tk.LastUsedAt), fmtMsPtr(tk.ExpiresAt))
			}
			t.flush()
			return nil
		}),
	})

	var name string
	create := &cobra.Command{
		Use: "create", Short: "Create a static agent token (printed once)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" && len(args) > 0 {
				name = args[0]
			}
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				tk, plain, err := svc.CreateAgentToken(ctx, name)
				if err != nil {
					return err
				}
				if g.json {
					return printJSON(map[string]any{"id": tk.ID, "name": tk.Name, "token": plain, "mcp_url": svc.LocalURL() + "/mcp"})
				}
				fmt.Printf("Created agent token %q (%s)\n\n  %s\n\nStore it now; it is not shown again.\n\n", tk.Name, tk.ID, plain)
				fmt.Printf("  claude mcp add --transport http switchboard %s/mcp --header \"Authorization: Bearer %s\"\n", svc.LocalURL(), plain)
				return nil
			})
		},
	}
	create.Flags().StringVarP(&name, "name", "n", "", "name for the token (required)")
	cmd.AddCommand(create)

	cmd.AddCommand(&cobra.Command{
		Use: "revoke <id>", Short: "Revoke an agent token", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				if err := svc.RevokeAgentToken(ctx, args[0]); err != nil {
					return err
				}
				fmt.Println("revoked", args[0])
				return nil
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "delete <id>", Aliases: []string{"rm"}, Short: "Delete an agent token record", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				if err := svc.DeleteAgentToken(ctx, args[0]); err != nil {
					return err
				}
				fmt.Println("deleted", args[0])
				return nil
			})
		},
	})
	return cmd
}
