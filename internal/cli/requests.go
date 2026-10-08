package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/openai"
)

func lastPrompt(r sqlcgen.Request) string {
	req, err := core.ParseRequestBody(r)
	if err != nil {
		return ""
	}
	return openai.LastMessage(req.Messages).Text()
}

func latencyOf(r sqlcgen.Request) string {
	if r.CompletedAt == nil {
		return "-"
	}
	return fmtDur(*r.CompletedAt - r.CreatedAt)
}

func requestsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "requests", Aliases: []string{"request", "req"}, Short: "Inspect, answer, cancel and requeue requests"}

	var f core.RequestFilter
	list := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List requests, newest first",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				rows, total, err := svc.ListRequests(ctx, f)
				if err != nil {
					return err
				}
				if g.json {
					return printJSON(map[string]any{"total": total, "requests": rows})
				}
				t := newTable("ID", "STATUS", "MODEL", "TURN", "LATENCY", "TOKENS", "AGE", "PROMPT")
				for _, r := range rows {
					t.row(r.ID, r.Status, r.Model, r.TurnIndex+1, latencyOf(r), fmt.Sprintf("%d/%d", r.PromptTokens, r.CompletionTokens), agoMs(r.CreatedAt), clip(lastPrompt(r), 60))
				}
				t.flush()
				fmt.Printf("\n%d of %d request(s)\n", len(rows), total)
				return nil
			})
		},
	}
	list.Flags().StringVar(&f.Status, "status", "", "filter by status (queued, claimed, streaming, completed, failed, cancelled, expired)")
	list.Flags().StringVar(&f.APIKeyID, "key", "", "filter by API key id")
	list.Flags().StringVar(&f.ChannelID, "channel", "", "filter by channel id")
	list.Flags().StringVar(&f.ConversationID, "conversation", "", "filter by conversation id")
	list.Flags().StringVar(&f.Model, "model", "", "filter by model")
	list.Flags().StringVarP(&f.Search, "search", "s", "", "search prompt/answer text")
	list.Flags().Int64VarP(&f.Limit, "limit", "l", 25, "maximum rows")
	cmd.AddCommand(list)

	cmd.AddCommand(&cobra.Command{
		Use: "pending", Short: "List requests that are queued or in flight (oldest first)",
		RunE: withService(func(ctx context.Context, svc *core.Service) error {
			rows, err := svc.ActiveRequests(ctx)
			if err != nil {
				return err
			}
			if g.json {
				return printJSON(rows)
			}
			t := newTable("ID", "STATUS", "MODEL", "WAITING", "CHANNEL", "PROMPT")
			for _, r := range rows {
				t.row(r.ID, r.Status, r.Model, fmtDur(time.Now().UnixMilli()-r.CreatedAt), orDash(deref(r.ChannelID)), clip(lastPrompt(r), 70))
			}
			t.flush()
			return nil
		}),
	})

	var raw bool
	show := &cobra.Command{
		Use: "show <id>", Short: "Show a request with its messages, answer and timeline", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				r, err := svc.GetRequest(ctx, args[0])
				if err != nil {
					return err
				}
				evs, _ := svc.RequestEvents(ctx, r.ID)
				if g.json {
					return printJSON(map[string]any{"request": r, "events": evs})
				}
				fmt.Printf("%s  %s  %s\n", r.ID, r.Status, r.Model)
				fmt.Printf("  conversation  %s (turn %d)\n  channel       %s\n  created       %s\n  latency       %s\n  tokens        %d prompt / %d completion\n  attempts      %d of %d\n",
					orDash(deref(r.ConversationID)), r.TurnIndex+1, orDash(deref(r.ChannelID)), fmtMs(r.CreatedAt), latencyOf(r), r.PromptTokens, r.CompletionTokens, r.Attempts, r.MaxAttempts)
				if r.ErrorMessage != nil {
					fmt.Printf("  error         %s\n", *r.ErrorMessage)
				}
				if raw {
					fmt.Printf("\n--- request body ---\n%s\n", r.RequestBody)
					if r.ResponseBody != nil {
						fmt.Printf("\n--- response body ---\n%s\n", *r.ResponseBody)
					}
					return nil
				}
				if req, err := core.ParseRequestBody(r); err == nil {
					fmt.Println("\nMessages:")
					for _, m := range req.Messages {
						fmt.Printf("  [%s] %s\n", m.Role, clip(m.Text(), 400))
						for _, tc := range m.ToolCalls {
							fmt.Printf("      -> %s(%s)\n", tc.Function.Name, clip(tc.Function.Arguments, 200))
						}
					}
				}
				if a, ok := core.ParseAnswer(r); ok {
					fmt.Printf("\nAnswer (%s):\n  %s\n", a.FinishReason, a.Content)
					for _, tc := range a.ToolCalls {
						fmt.Printf("  -> %s(%s)\n", tc.Function.Name, tc.Function.Arguments)
					}
				}
				if len(evs) > 0 {
					fmt.Println("\nTimeline:")
					for _, e := range evs {
						fmt.Printf("  %s  %-24s %s\n", time.UnixMilli(e.Ts).Format("15:04:05.000"), e.Kind, e.Message)
					}
				}
				return nil
			})
		},
	}
	show.Flags().BoolVar(&raw, "raw", false, "print raw request/response JSON")
	cmd.AddCommand(show)

	cmd.AddCommand(&cobra.Command{
		Use: "cancel <id>", Short: "Cancel a queued or in-flight request", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				if err := svc.CancelRequest(ctx, args[0], "cancelled from CLI"); err != nil {
					return err
				}
				fmt.Println("cancelled", args[0])
				return nil
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "requeue <id>", Short: "Return an in-flight request to the queue", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				if err := svc.RequeueRequest(ctx, args[0], "requeued from CLI"); err != nil {
					return err
				}
				fmt.Println("requeued", args[0])
				return nil
			})
		},
	})

	var text, file string
	var force bool
	answer := &cobra.Command{
		Use:   "answer <id>",
		Short: "Answer a request yourself (human in the loop)",
		Long:  "Answer a queued request as the model. Provide the reply with --text, --file, or on stdin. The waiting caller receives it as a normal OpenAI response.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			content := text
			switch {
			case file != "":
				b, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				content = string(b)
			case content == "":
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				content = string(b)
			}
			if strings.TrimSpace(content) == "" {
				return errors.New("empty answer: pass --text, --file or pipe the reply on stdin")
			}
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				r, err := svc.AnswerRequest(ctx, args[0], openai.Answer{Content: content}, force)
				if err != nil {
					return err
				}
				fmt.Printf("answered %s (%s); the server delivers it to the caller within ~2s\n", r.ID, latencyOf(r))
				return nil
			})
		},
	}
	answer.Flags().StringVarP(&text, "text", "t", "", "the assistant reply")
	answer.Flags().StringVarP(&file, "file", "f", "", "read the reply from a file")
	answer.Flags().BoolVar(&force, "force", false, "take over a request another channel holds")
	cmd.AddCommand(answer)
	return cmd
}

func conversationsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "conversations", Aliases: []string{"conversation", "conv"}, Short: "Browse reconstructed conversations"}

	var f core.ConversationFilter
	list := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List conversations, most recently active first",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				rows, total, err := svc.ListConversations(ctx, f)
				if err != nil {
					return err
				}
				if g.json {
					return printJSON(map[string]any{"total": total, "conversations": rows})
				}
				t := newTable("ID", "TURNS", "MODEL", "UPDATED", "TITLE")
				for _, c := range rows {
					t.row(c.ID, c.TurnCount, c.Model, agoMs(c.UpdatedAt), clip(c.Title, 70))
				}
				t.flush()
				return nil
			})
		},
	}
	list.Flags().StringVar(&f.APIKeyID, "key", "", "filter by API key id")
	list.Flags().StringVarP(&f.Search, "search", "s", "", "search titles")
	list.Flags().Int64VarP(&f.Limit, "limit", "l", 25, "maximum rows")
	cmd.AddCommand(list)

	cmd.AddCommand(&cobra.Command{
		Use: "show <id>", Short: "Print a conversation as a thread", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				c, err := svc.GetConversation(ctx, args[0])
				if err != nil {
					return err
				}
				reqs, err := svc.ConversationRequests(ctx, c.ID)
				if err != nil {
					return err
				}
				if g.json {
					return printJSON(map[string]any{"conversation": c, "requests": reqs})
				}
				fmt.Printf("%s  %q  (%d turns, %s)\n", c.ID, c.Title, c.TurnCount, c.Model)
				prev := 0
				for i, r := range reqs {
					req, err := core.ParseRequestBody(r)
					if err != nil {
						continue
					}
					msgs := req.Messages
					start := 0
					if i > 0 {
						start = min(prev+1, max(len(msgs)-1, 0))
					}
					fmt.Printf("\n── turn %d · %s · %s · %s\n", r.TurnIndex+1, r.ID, r.Status, latencyOf(r))
					for _, m := range msgs[start:] {
						fmt.Printf("[%s] %s\n", m.Role, m.Text())
					}
					if a, ok := core.ParseAnswer(r); ok {
						fmt.Printf("[assistant] %s\n", a.Content)
						for _, tc := range a.ToolCalls {
							fmt.Printf("  -> %s(%s)\n", tc.Function.Name, tc.Function.Arguments)
						}
					} else if r.ErrorMessage != nil {
						fmt.Printf("[%s] %s\n", r.Status, *r.ErrorMessage)
					}
					prev = len(msgs)
				}
				return nil
			})
		},
	})
	return cmd
}

// sendCmd submits a test request straight into the queue and waits for the
// answer by polling, so it works with or without the server running (a
// running server is still needed for agents to claim it).
func sendCmd() *cobra.Command {
	var model, system string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "send <prompt>",
		Short: "Queue a test request and wait for an agent (or `requests answer`) to reply",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")
			return runWithService(cmd.Context(), func(ctx context.Context, svc *core.Service) error {
				key, err := cliKey(ctx, svc)
				if err != nil {
					return err
				}
				var msgs []openai.Message
				if system != "" {
					c, _ := json.Marshal(system)
					msgs = append(msgs, openai.Message{Role: "system", Content: c})
				}
				c, _ := json.Marshal(prompt)
				msgs = append(msgs, openai.Message{Role: "user", Content: c})
				body, _ := json.Marshal(map[string]any{"model": model, "messages": msgs})
				req, err := openai.ParseChatRequest(body)
				if err != nil {
					return err
				}
				key.TimeoutSeconds = int64(timeout.Seconds())
				r, err := svc.SubmitRequest(ctx, core.SubmitInput{Key: key, Req: req, Endpoint: "chat.completions", Body: body, ClientIP: "cli", UserAgent: "switchboard-cli"})
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "queued %s (model %s); waiting up to %s…\n", r.ID, r.Model, timeout)
				outcome, final, err := svc.WaitRequest(ctx, r.ID)
				if err != nil {
					_ = svc.CancelRequest(context.Background(), r.ID, "cli interrupted")
					return fmt.Errorf("interrupted; request cancelled")
				}
				if g.json {
					return printJSON(final)
				}
				if outcome.Status != core.StatusCompleted {
					return fmt.Errorf("request %s: %s", outcome.Status, outcome.Error)
				}
				if outcome.Answer != nil {
					fmt.Println(outcome.Answer.Content)
				}
				fmt.Fprintf(os.Stderr, "\n(%s, %s)\n", final.ID, latencyOf(final))
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&model, "model", "m", "switchboard/auto", "model name to request")
	cmd.Flags().StringVar(&system, "system", "", "optional system prompt")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for an answer")
	return cmd
}

// cliKey returns (creating once) the internal API key that `send` attributes
// its requests to.
func cliKey(ctx context.Context, svc *core.Service) (sqlcgen.ApiKey, error) {
	keys, err := svc.ListAPIKeys(ctx)
	if err != nil {
		return sqlcgen.ApiKey{}, err
	}
	for _, k := range keys {
		if k.Name == "switchboard-cli" && k.RevokedAt == nil {
			return k, nil
		}
	}
	k, _, err := svc.CreateAPIKey(ctx, core.APIKeyInput{Name: "switchboard-cli"})
	return k, err
}
