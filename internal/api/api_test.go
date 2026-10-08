package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ucgeorge/switchboard/internal/api"
	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/openai"
	"github.com/ucgeorge/switchboard/internal/testdb"
)

type fixture struct {
	svc *core.Service
	srv *httptest.Server
	key string
	wg  sync.WaitGroup
}

func setup(t *testing.T) *fixture {
	t.Helper()
	d := testdb.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := core.New(d, log)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.New(svc, log).Register(mux)
	srv := httptest.NewServer(mux)
	_, plain, err := svc.CreateAPIKey(context.Background(), core.APIKeyInput{Name: "test-app"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{svc: svc, srv: srv, key: plain}
	t.Cleanup(func() {
		f.wg.Wait()
		srv.Close()
		svc.Close()
		d.Close()
	})
	return f
}

func (f *fixture) post(t *testing.T, path, key, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// agent runs fn against the next claimed request, like an MCP agent would.
func (f *fixture) startAgent(t *testing.T, fn func(channelID, requestID string)) {
	f.wg.Add(1)
	go func() { defer f.wg.Done(); f.agent(t, fn) }()
}

func (f *fixture) agent(t *testing.T, fn func(channelID, requestID string)) {
	t.Helper()
	ctx := context.Background()
	ch, _, err := f.svc.OpenChannel(ctx, "tok", core.ChannelInput{Name: "agent"})
	if err != nil {
		t.Error(err)
		return
	}
	r, err := f.svc.ClaimRequest(ctx, ch.ID, "tok", 5*time.Second)
	if err != nil || r == nil {
		t.Errorf("agent claim: %v %v", r, err)
		return
	}
	fn(ch.ID, r.ID)
}

func TestAuth(t *testing.T) {
	f := setup(t)
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	for name, key := range map[string]string{"missing": "", "wrong": "sk-sb-nope"} {
		resp := f.post(t, "/v1/chat/completions", key, body)
		var e openai.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&e)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || e.Error.Type != openai.ErrTypeAuthentication {
			t.Fatalf("%s key: status %d, error %+v", name, resp.StatusCode, e.Error)
		}
	}
}

func TestValidation(t *testing.T) {
	f := setup(t)
	resp := f.post(t, "/v1/chat/completions", f.key, `{"model":"m","messages":[]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestNonStreaming(t *testing.T) {
	f := setup(t)
	f.startAgent(t, func(ch, id string) {
		if _, err := f.svc.CompleteRequest(context.Background(), ch, "tok", id, openai.Answer{Content: "Hello from the agent"}); err != nil {
			t.Error(err)
		}
	})
	resp := f.post(t, "/v1/chat/completions", f.key, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, b)
	}
	if resp.Header.Get("x-switchboard-request-id") == "" || resp.Header.Get("x-switchboard-conversation-id") == "" {
		t.Fatalf("missing switchboard headers: %v", resp.Header)
	}
	var cc openai.ChatCompletion
	if err := json.NewDecoder(resp.Body).Decode(&cc); err != nil {
		t.Fatal(err)
	}
	if cc.Model != "gpt-4o" || len(cc.Choices) != 1 || *cc.Choices[0].Message.Content != "Hello from the agent" || cc.Usage.TotalTokens == 0 {
		t.Fatalf("unexpected completion: %+v", cc)
	}
	if !strings.HasPrefix(cc.ID, "chatcmpl-") {
		t.Fatalf("id = %q", cc.ID)
	}
}

func readSSE(t *testing.T, body io.Reader) (content string, finish string, sawDone bool, chunks int) {
	t.Helper()
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			sawDone = true
			break
		}
		var ch openai.ChatChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			t.Fatalf("bad chunk %q: %v", data, err)
		}
		chunks++
		for _, c := range ch.Choices {
			if c.Delta.Content != nil {
				content += *c.Delta.Content
			}
			if c.FinishReason != nil {
				finish = *c.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	return
}

func TestStreaming(t *testing.T) {
	f := setup(t)
	f.startAgent(t, func(ch, id string) {
		ctx := context.Background()
		for _, part := range []string{"Hel", "lo ", "world"} {
			p := part
			if err := f.svc.StreamDelta(ctx, ch, "tok", id, openai.Delta{Content: &p}); err != nil {
				t.Error(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		// Empty content: the streamed text is the answer.
		if _, err := f.svc.CompleteRequest(ctx, ch, "tok", id, openai.Answer{}); err != nil {
			t.Error(err)
		}
	})
	resp := f.post(t, "/v1/chat/completions", f.key, `{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	content, finish, done, chunks := readSSE(t, resp.Body)
	if content != "Hello world" || finish != "stop" || !done || chunks < 4 {
		t.Fatalf("content=%q finish=%q done=%v chunks=%d", content, finish, done, chunks)
	}
	// The stored answer is the concatenation of the stream.
	id := resp.Header.Get("x-switchboard-request-id")
	rec, err := f.svc.GetRequest(context.Background(), id)
	if err != nil || rec.ResponseText == nil || *rec.ResponseText != "Hello world" || rec.FirstTokenAt == nil {
		t.Fatalf("stored request: %+v %v", rec, err)
	}
}

// A stream:true caller served by an agent that does not stream still gets a
// well-formed SSE response.
func TestStreamingWithNonStreamingAgent(t *testing.T) {
	f := setup(t)
	f.startAgent(t, func(ch, id string) {
		_, err := f.svc.CompleteRequest(context.Background(), ch, "tok", id, openai.Answer{
			ToolCalls: []openai.ToolCall{{Function: openai.FunctionCall{Name: "get_weather", Arguments: `{"city":"Lagos"}`}}},
		})
		if err != nil {
			t.Error(err)
		}
	})
	resp := f.post(t, "/v1/chat/completions", f.key, `{"model":"m","stream":true,"messages":[{"role":"user","content":"weather?"}],"tools":[{"type":"function","function":{"name":"get_weather"}}]}`)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	out := string(b)
	if !strings.Contains(out, `"get_weather"`) || !strings.Contains(out, `"finish_reason":"tool_calls"`) || !strings.HasSuffix(strings.TrimSpace(out), "data: [DONE]") {
		t.Fatalf("unexpected stream:\n%s", out)
	}
}

func TestAgentFailureMapsToError(t *testing.T) {
	f := setup(t)
	f.startAgent(t, func(ch, id string) {
		if err := f.svc.FailRequest(context.Background(), ch, "tok", id, "model unavailable", false); err != nil {
			t.Error(err)
		}
	})
	resp := f.post(t, "/v1/chat/completions", f.key, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	defer resp.Body.Close()
	var e openai.ErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != http.StatusBadGateway || e.Error.Code == nil || *e.Error.Code != "agent_error" {
		t.Fatalf("status %d error %+v", resp.StatusCode, e.Error)
	}
}

func TestCallerDisconnectCancels(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+f.key)
	errc := make(chan error, 1)
	go func() {
		_, err := http.DefaultClient.Do(req)
		errc <- err
	}()
	var id string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if active, _ := f.svc.ActiveRequests(context.Background()); len(active) == 1 {
			id = active[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("request never queued")
	}
	cancel()
	<-errc
	for time.Now().Before(deadline) {
		if rec, _ := f.svc.GetRequest(context.Background(), id); rec.Status == core.StatusCancelled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec, _ := f.svc.GetRequest(context.Background(), id)
	t.Fatalf("status = %s, want cancelled after caller disconnect", rec.Status)
}

func TestRateLimit(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	k, plain, err := f.svc.CreateAPIKey(ctx, core.APIKeyInput{Name: "limited", RateLimitRPM: 1})
	if err != nil {
		t.Fatal(err)
	}
	// First request consumes the only token; answer it so the call returns.
	f.startAgent(t, func(ch, id string) {
		_, _ = f.svc.CompleteRequest(ctx, ch, "tok", id, openai.Answer{Content: "ok"})
	})
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	first := f.post(t, "/v1/chat/completions", plain, body)
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d", first.StatusCode)
	}
	second := f.post(t, "/v1/chat/completions", plain, body)
	defer second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests || second.Header.Get("Retry-After") == "" {
		t.Fatalf("second status = %d (key %s)", second.StatusCode, k.ID)
	}
}

func TestModels(t *testing.T) {
	f := setup(t)
	if _, _, err := f.svc.OpenChannel(context.Background(), "tok", core.ChannelInput{Name: "a", Models: []string{"gpt-4o", "claude-*"}}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+f.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list openai.ModelList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, m := range list.Data {
		ids[m.ID] = true
	}
	if !ids["gpt-4o"] || !ids["switchboard/auto"] || ids["claude-*"] {
		t.Fatalf("models = %v", ids)
	}
}
