package mcpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db"
	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/mcpserver"
	"github.com/ucgeorge/switchboard/internal/openai"
)

// These tests drive the server through the real MCP client transport, so the
// SDK's schema validation of tool inputs and outputs is exercised exactly as
// it is for a connected agent.

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

type fixture struct {
	svc *core.Service
	srv *httptest.Server
	key sqlcgen.ApiKey
}

func setup(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := core.New(d, log)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mcpserver.New(svc, log))
	key, _, err := svc.CreateAPIKey(context.Background(), core.APIKeyInput{Name: "caller-app"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Close()
		svc.Close()
		d.Close()
	})
	return &fixture{svc: svc, srv: srv, key: key}
}

func (f *fixture) connect(t *testing.T, tokenName string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	_, plain, err := f.svc.CreateAgentToken(ctx, tokenName)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             f.srv.URL,
		HTTPClient:           &http.Client{Transport: bearer{token: plain, base: http.DefaultTransport}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func (f *fixture) submit(t *testing.T, body string) sqlcgen.Request {
	t.Helper()
	req, err := openai.ParseChatRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.SubmitRequest(context.Background(), core.SubmitInput{Key: f.key, Req: req, Body: []byte(body), Endpoint: "chat.completions", UserAgent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// call invokes a tool and decodes its structured result into out.
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	if out != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: decode result: %v", name, err)
		}
	}
	return res
}

func errText(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestUnauthorized(t *testing.T) {
	f := setup(t)
	resp, err := http.Post(f.srv.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatalf("status %d, WWW-Authenticate %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}

func TestServeLoop(t *testing.T) {
	f := setup(t)
	s := f.connect(t, "agent-a")
	ctx := context.Background()

	tools, err := s.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range tools.Tools {
		have[tl.Name] = true
	}
	for _, want := range []string{"open_channel", "claim_request", "stream_delta", "complete_request", "fail_request", "release_request", "extend_lease", "heartbeat", "get_request", "queue_status", "list_channels", "close_channel"} {
		if !have[want] {
			t.Errorf("tool %q not registered", want)
		}
	}

	var ch struct {
		ChannelID string `json:"channel_id"`
		Status    string `json:"status"`
	}
	call(t, s, "open_channel", map[string]any{"name": "agent-a", "concurrency": 2}, &ch)
	if ch.ChannelID == "" || ch.Status != "online" {
		t.Fatalf("open_channel = %+v", ch)
	}

	// A realistic request: string content, multi-part content, numeric and
	// object params, and a tool definition. All of it must survive output
	// schema validation on the way to the agent.
	rec := f.submit(t, `{
		"model": "gpt-4o", "temperature": 0.7, "max_tokens": 256, "stream": true,
		"response_format": {"type": "json_object"},
		"tools": [{"type": "function", "function": {"name": "lookup", "parameters": {"type": "object"}}}],
		"messages": [
			{"role": "system", "content": "Be terse."},
			{"role": "user", "content": [{"type": "text", "text": "What is in this image?"}, {"type": "image_url", "image_url": {"url": "https://example.com/a.png"}}]},
			{"role": "assistant", "content": null, "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "lookup", "arguments": "{\"q\":\"a\"}"}}]},
			{"role": "tool", "tool_call_id": "call_1", "content": "a cat"}
		]}`)

	var claim struct {
		Found   bool `json:"found"`
		Request struct {
			RequestID string           `json:"request_id"`
			Model     string           `json:"model"`
			Stream    bool             `json:"stream"`
			Messages  []map[string]any `json:"messages"`
			Params    map[string]any   `json:"params"`
			Caller    map[string]any   `json:"caller"`
		} `json:"request"`
	}
	res := call(t, s, "claim_request", map[string]any{"channel_id": ch.ChannelID, "wait_seconds": 5}, &claim)
	if res.IsError {
		t.Fatalf("claim_request failed: %s", errText(res))
	}
	r := claim.Request
	if !claim.Found || r.RequestID != rec.ID || r.Model != "gpt-4o" || !r.Stream || len(r.Messages) != 4 {
		t.Fatalf("claim = %+v", claim)
	}
	if r.Messages[0]["content"] != "Be terse." {
		t.Errorf("string content = %#v", r.Messages[0]["content"])
	}
	if parts, ok := r.Messages[1]["content"].([]any); !ok || len(parts) != 2 {
		t.Errorf("multi-part content = %#v", r.Messages[1]["content"])
	}
	if r.Messages[2]["content"] != nil || r.Messages[2]["tool_calls"] == nil {
		t.Errorf("assistant tool-call message = %#v", r.Messages[2])
	}
	if r.Messages[3]["tool_call_id"] != "call_1" {
		t.Errorf("tool message = %#v", r.Messages[3])
	}
	if r.Params["temperature"] != 0.7 || r.Params["max_tokens"] != float64(256) || r.Params["tools"] == nil || r.Params["response_format"] == nil {
		t.Errorf("params = %#v", r.Params)
	}
	if _, leaked := r.Params["messages"]; leaked {
		t.Error("params must not repeat messages")
	}
	if r.Caller["api_key_name"] != "caller-app" {
		t.Errorf("caller = %#v", r.Caller)
	}

	// Nothing else is queued, so a second claim long-polls and comes back empty.
	var empty struct {
		Found bool `json:"found"`
	}
	call(t, s, "claim_request", map[string]any{"channel_id": ch.ChannelID, "wait_seconds": 1}, &empty)
	if empty.Found {
		t.Fatal("claimed a request that does not exist")
	}

	if res := call(t, s, "stream_delta", map[string]any{"channel_id": ch.ChannelID, "request_id": rec.ID, "content": "It is "}, nil); res.IsError {
		t.Fatalf("stream_delta: %s", errText(res))
	}
	if res := call(t, s, "extend_lease", map[string]any{"channel_id": ch.ChannelID, "request_id": rec.ID}, nil); res.IsError {
		t.Fatalf("extend_lease: %s", errText(res))
	}

	// Tool call without id/type and with object arguments; partial usage.
	var done struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	res = call(t, s, "complete_request", map[string]any{
		"channel_id": ch.ChannelID, "request_id": rec.ID, "content": "It is a cat.",
		"tool_calls": []map[string]any{{"function": map[string]any{"name": "lookup", "arguments": map[string]any{"q": "cat breeds"}}}},
		"usage":      map[string]any{"prompt_tokens": 42, "completion_tokens": 7},
	}, &done)
	if res.IsError || !done.OK || done.Status != core.StatusCompleted {
		t.Fatalf("complete_request = %+v %s", done, errText(res))
	}

	stored, err := f.svc.GetRequest(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := core.ParseAnswer(stored)
	if !ok || a.Content != "It is a cat." || len(a.ToolCalls) != 1 {
		t.Fatalf("stored answer = %+v", a)
	}
	tc := a.ToolCalls[0]
	if !strings.HasPrefix(tc.ID, "call_") || tc.Type != "function" || tc.Function.Arguments != `{"q":"cat breeds"}` || a.FinishReason != "tool_calls" {
		t.Errorf("tool call = %+v finish=%s", tc, a.FinishReason)
	}
	if stored.UsageEstimated || stored.PromptTokens != 42 || stored.CompletionTokens != 7 {
		t.Errorf("usage: estimated=%v prompt=%d completion=%d", stored.UsageEstimated, stored.PromptTokens, stored.CompletionTokens)
	}

	var state struct {
		Status string `json:"status"`
	}
	call(t, s, "get_request", map[string]any{"request_id": rec.ID}, &state)
	if state.Status != core.StatusCompleted {
		t.Errorf("get_request status = %q", state.Status)
	}
	var qs struct {
		Queued         int64 `json:"queued"`
		OnlineChannels int64 `json:"online_channels"`
	}
	call(t, s, "queue_status", map[string]any{}, &qs)
	if qs.Queued != 0 || qs.OnlineChannels != 1 {
		t.Errorf("queue_status = %+v", qs)
	}
}

func TestToolErrorsAreActionable(t *testing.T) {
	f := setup(t)
	s := f.connect(t, "agent-a")

	res := call(t, s, "claim_request", map[string]any{"channel_id": "ch_missing", "wait_seconds": 1}, nil)
	if !res.IsError || !strings.Contains(errText(res), "open_channel") {
		t.Fatalf("unknown channel: isError=%v text=%q", res.IsError, errText(res))
	}

	var ch struct {
		ChannelID string `json:"channel_id"`
	}
	call(t, s, "open_channel", map[string]any{"name": "agent-a"}, &ch)
	rec := f.submit(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	call(t, s, "claim_request", map[string]any{"channel_id": ch.ChannelID, "wait_seconds": 5}, nil)

	// The caller gives up while the agent is working.
	if err := f.svc.CancelRequest(context.Background(), rec.ID, "caller disconnected"); err != nil {
		t.Fatal(err)
	}
	res = call(t, s, "complete_request", map[string]any{"channel_id": ch.ChannelID, "request_id": rec.ID, "content": "too late"}, nil)
	if !res.IsError || !strings.Contains(errText(res), "cancelled") {
		t.Fatalf("completing a cancelled request: isError=%v text=%q", res.IsError, errText(res))
	}
}

func TestChannelsAreIsolatedPerToken(t *testing.T) {
	f := setup(t)
	a := f.connect(t, "agent-a")
	b := f.connect(t, "agent-b")

	var ch struct {
		ChannelID string `json:"channel_id"`
		Name      string `json:"name"`
	}
	call(t, a, "open_channel", map[string]any{"name": "shared-name"}, &ch)
	f.submit(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)

	res := call(t, b, "claim_request", map[string]any{"channel_id": ch.ChannelID, "wait_seconds": 1}, nil)
	if !res.IsError || !strings.Contains(errText(res), "different agent token") {
		t.Fatalf("foreign token drove another agent's channel: isError=%v text=%q", res.IsError, errText(res))
	}

	// Agent B asking for the same name gets its own distinct channel.
	var other struct {
		ChannelID string `json:"channel_id"`
		Name      string `json:"name"`
	}
	call(t, b, "open_channel", map[string]any{"name": "shared-name"}, &other)
	if other.ChannelID == ch.ChannelID || other.Name == ch.Name {
		t.Fatalf("second agent was given the first agent's channel: %+v", other)
	}
}

func TestRevokedTokenIsRejected(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	tok, plain, err := f.svc.CreateAgentToken(ctx, "short-lived")
	if err != nil {
		t.Fatal(err)
	}
	post := func() int {
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Authorization", "Bearer "+plain)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := post(); got != http.StatusOK {
		t.Fatalf("valid token = %d, want 200", got)
	}
	if err := f.svc.RevokeAgentToken(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if got := post(); got != http.StatusUnauthorized {
		t.Fatalf("revoked token = %d, want 401", got)
	}
}

func TestPromptAndResources(t *testing.T) {
	f := setup(t)
	s := f.connect(t, "agent-a")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := s.GetPrompt(ctx, &mcp.GetPromptParams{Name: "serve", Arguments: map[string]string{"channel_name": "desk-1", "models": "gpt-4o"}})
	if err != nil {
		t.Fatal(err)
	}
	text := p.Messages[0].Content.(*mcp.TextContent).Text
	for _, want := range []string{`"desk-1"`, `"gpt-4o"`, "claim_request", "complete_request"} {
		if !strings.Contains(text, want) {
			t.Errorf("serve prompt missing %q", want)
		}
	}

	guide, err := s.ReadResource(ctx, &mcp.ReadResourceParams{URI: "switchboard://guide"})
	if err != nil || !strings.Contains(guide.Contents[0].Text, "open_channel") {
		t.Fatalf("guide resource: %v", err)
	}
	status, err := s.ReadResource(ctx, &mcp.ReadResourceParams{URI: "switchboard://status"})
	if err != nil {
		t.Fatal(err)
	}
	var qs map[string]any
	if err := json.Unmarshal([]byte(status.Contents[0].Text), &qs); err != nil || qs["queued"] != float64(0) {
		t.Fatalf("status resource = %q (%v)", status.Contents[0].Text, err)
	}
}
