package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/mcpserver"
)

const mcpServerName = "switchboard"

func mcpURL(svc *core.Service) string {
	if g.url != "" {
		base, _ := remoteBase()
		return base + "/mcp"
	}
	return svc.LocalURL() + "/mcp"
}
func withMCP(ctx context.Context, fn func(context.Context, *core.Service) error) error {
	if g.url != "" {
		return fn(ctx, nil)
	}
	return runWithService(ctx, fn)
}

// tokenFor returns the bearer token to embed in a config: the one supplied,
// or a freshly minted token named after the client.
func tokenFor(ctx context.Context, svc *core.Service, supplied, name string) (string, bool, error) {
	if supplied != "" {
		return supplied, false, nil
	}
	if g.url != "" {
		result, err := remoteCall(ctx, []string{"tokens", "create", "--name=" + name, "--json"})
		if err != nil {
			return "", false, err
		}
		var out struct {
			Token string `json:"token"`
		}
		if err = json.Unmarshal([]byte(result.Stdout), &out); err != nil {
			return "", false, err
		}
		return out.Token, true, nil
	}
	_, plain, err := svc.CreateAgentToken(ctx, name)
	return plain, true, err
}

func mcpSnippet(client, url, token string) (string, error) {
	switch client {
	case "claude-code", "claude":
		return fmt.Sprintf("claude mcp add --transport http %s %s --header \"Authorization: Bearer %s\"", mcpServerName, url, token), nil
	case "vscode":
		b, _ := json.MarshalIndent(map[string]any{"servers": map[string]any{mcpServerName: map[string]any{"type": "http", "url": url, "headers": map[string]string{"Authorization": "Bearer " + token}}}}, "", "  ")
		return string(b), nil
	case "cursor", "windsurf", "json":
		return fmt.Sprintf(`{
  "mcpServers": {
    "%s": {
      "url": "%s",
      "headers": { "Authorization": "Bearer %s" }
    }
  }
}`, mcpServerName, url, token), nil
	case "claude-desktop":
		return fmt.Sprintf(`{
  "mcpServers": {
    "%s": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "%s", "--header", "Authorization: Bearer %s"]
    }
  }
}`, mcpServerName, url, token), nil
	case "codex":
		return fmt.Sprintf(`# ~/.codex/config.toml  (export SWITCHBOARD_TOKEN=%s)
[mcp_servers.%s]
url = "%s"
bearer_token_env_var = "SWITCHBOARD_TOKEN"`, token, mcpServerName, url), nil
	}
	return "", fmt.Errorf("unknown client %q (use claude-code, claude-desktop, cursor, windsurf, vscode, codex or json)", client)
}

func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "mcp", Short: "Connect agents to the MCP endpoint"}

	cmd.AddCommand(&cobra.Command{
		Use: "url", Short: "Print the MCP endpoint URL",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withMCP(cmd.Context(), func(ctx context.Context, svc *core.Service) error { fmt.Println(mcpURL(svc)); return nil })
		},
	})

	var token, tokenName string
	config := &cobra.Command{
		Use:   "config [client]",
		Short: "Print a ready-to-paste MCP configuration for a client",
		Long: `Print MCP configuration for a client: claude-code, claude-desktop, cursor,
windsurf, vscode, codex or json (default). A new agent token is created
unless --token is given. OAuth-capable clients can skip the token entirely
and just add the URL from 'switchboard mcp url'.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := "json"
			if len(args) == 1 {
				client = args[0]
			}
			if _, err := mcpSnippet(client, "", ""); err != nil {
				return err
			}
			return withMCP(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				name := tokenName
				if name == "" {
					name = client
				}
				tok, created, err := tokenFor(ctx, svc, token, name)
				if err != nil {
					return err
				}
				snippet, _ := mcpSnippet(client, mcpURL(svc), tok)
				if g.json {
					return printJSON(map[string]any{"client": client, "url": mcpURL(svc), "token": tok, "token_created": created, "config": snippet})
				}
				if created {
					fmt.Fprintf(os.Stderr, "Created agent token %q. It is embedded below and not shown again.\n\n", name)
				}
				fmt.Println(snippet)
				return nil
			})
		},
	}
	config.Flags().StringVar(&token, "token", "", "use an existing agent token instead of creating one")
	config.Flags().StringVar(&tokenName, "token-name", "", "name for the created token (default: the client name)")
	cmd.AddCommand(config)

	var addToken, addName, scope string
	var printOnly bool
	add := &cobra.Command{
		Use:   "add <client>",
		Short: "Register the MCP server with a client (currently: claude-code)",
		Long: `Create an agent token and register Switchboard with the client. For
claude-code this runs 'claude mcp add' for you. For other clients use
'switchboard mcp config <client>' and paste the output.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := args[0]
			if client != "claude-code" && client != "claude" {
				return fmt.Errorf("automatic registration supports claude-code; run `switchboard mcp config %s` and paste the result", client)
			}
			return withMCP(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				name := addName
				if name == "" {
					name = "claude-code"
				}
				tok, created, err := tokenFor(ctx, svc, addToken, name)
				if err != nil {
					return err
				}
				url := mcpURL(svc)
				argv := []string{"mcp", "add", "--transport", "http", "--scope", scope, mcpServerName, url, "--header", "Authorization: Bearer " + tok}
				snippet, _ := mcpSnippet("claude-code", url, tok)
				bin, lookErr := exec.LookPath("claude")
				if printOnly || lookErr != nil {
					if lookErr != nil && !printOnly {
						fmt.Fprintln(os.Stderr, "`claude` was not found on PATH; run this yourself:")
					}
					fmt.Println(snippet)
					return nil
				}
				c := exec.CommandContext(ctx, bin, argv...)
				c.Stdout, c.Stderr = os.Stdout, os.Stderr
				if err := c.Run(); err != nil {
					return fmt.Errorf("claude mcp add failed: %w\nRun it manually:\n  %s", err, snippet)
				}
				if created {
					fmt.Printf("\nRegistered %q with Claude Code using new agent token %q.\n", mcpServerName, name)
				}
				fmt.Println("Next: `switchboard skill install`, then ask Claude Code to \"serve Switchboard requests\".")
				return nil
			})
		},
	}
	add.Flags().StringVar(&addToken, "token", "", "use an existing agent token instead of creating one")
	add.Flags().StringVar(&addName, "name", "", "name for the created token (default claude-code)")
	add.Flags().StringVar(&scope, "scope", "user", "claude mcp scope: local, user or project")
	add.Flags().BoolVar(&printOnly, "print", false, "print the command instead of running it")
	cmd.AddCommand(add)
	return cmd
}

// SkillMarkdown is the Claude Code skill that teaches an agent the serving
// loop. It wraps the same guide exposed over MCP.
func SkillMarkdown() string {
	return `---
name: switchboard-agent
description: Serve queued OpenAI-compatible chat requests from a Switchboard server over MCP. Use when asked to "serve Switchboard requests", act as the model behind Switchboard, drain the Switchboard queue, or answer requests from the switchboard MCP server.
---

` + mcpserver.AgentGuide + `
## Tool names

The tools live on the MCP server named ` + "`switchboard`" + `: open_channel, claim_request,
stream_delta, complete_request, fail_request, release_request, extend_lease,
heartbeat, get_request, queue_status, list_channels, close_channel.

## Operating notes for this skill

- Start immediately: open_channel, then claim_request with wait_seconds=25.
- Stay in the loop without asking the user between requests. Report a short
  one-line summary after each answered request (id, model, latency).
- If the user gave a limit ("serve 10 requests", "serve for 15 minutes"),
  stop at that limit and call close_channel.
- If the MCP server is not connected, tell the user to run
  ` + "`switchboard mcp add claude-code`" + ` and restart the session.
`
}

func skillCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "skill", Short: "Install the agent skill that teaches the serving loop"}

	var project string
	var force bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the switchboard-agent skill for Claude Code",
		Long: `Write the switchboard-agent skill to ~/.claude/skills (default) or to a
project's .claude/skills directory with --project. The skill tells the agent
how to open a channel and run the claim/answer loop.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var base string
			if project != "" {
				base = filepath.Join(project, ".claude", "skills")
			} else {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				base = filepath.Join(home, ".claude", "skills")
			}
			dir := filepath.Join(base, "switchboard-agent")
			path := filepath.Join(dir, "SKILL.md")
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(SkillMarkdown()), 0o644); err != nil {
				return err
			}
			fmt.Println("installed skill at", path)
			return nil
		},
	}
	install.Flags().StringVar(&project, "project", "", "install into this project directory instead of your home directory")
	install.Flags().BoolVar(&force, "force", false, "overwrite an existing skill")
	cmd.AddCommand(install)

	cmd.AddCommand(&cobra.Command{
		Use: "print", Short: "Print the skill markdown",
		Run: func(cmd *cobra.Command, args []string) { fmt.Print(SkillMarkdown()) },
	})
	return cmd
}
