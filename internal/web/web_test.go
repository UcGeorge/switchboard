package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db"
	"github.com/ucgeorge/switchboard/internal/openai"
)

type webFixture struct {
	svc     *core.Service
	srv     *httptest.Server
	session string
	reqDone string
	reqLive string
	reqWait string
	conv    string
	channel string
}

func newFixture(t *testing.T) *webFixture {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := core.New(d, log)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := New(svc, log, "test.db")
	if err != nil {
		t.Fatalf("templates failed to parse: %v", err)
	}
	mux := http.NewServeMux()
	ws.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		svc.Close()
		d.Close()
	})
	if err := svc.SetPassword(ctx, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	f := &webFixture{svc: svc, srv: srv}
	if f.session, err = svc.CreateSession(ctx, "test"); err != nil {
		t.Fatal(err)
	}

	// Seed: a key, an agent token, a channel, and requests in three states.
	key, _, err := svc.CreateAPIKey(ctx, core.APIKeyInput{Name: "seed-app", AllowedModels: []string{"gpt-*"}, RateLimitRPM: 60})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := svc.CreateAgentToken(ctx, "seed-agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.RegisterOAuthClient(ctx, core.OAuthClientInput{Name: "Inspector", RedirectURIs: []string{"http://localhost:6274/cb"}}); err != nil {
		t.Fatal(err)
	}
	ch, _, err := svc.OpenChannel(ctx, tok.ID, core.ChannelInput{Name: "seed-channel", Concurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	f.channel = ch.ID
	submit := func(text string) string {
		body, _ := json.Marshal(map[string]any{"model": "gpt-4o", "temperature": 0.2, "messages": []map[string]string{{"role": "system", "content": "be brief"}, {"role": "user", "content": text}}})
		req, err := openai.ParseChatRequest(body)
		if err != nil {
			t.Fatal(err)
		}
		r, err := svc.SubmitRequest(ctx, core.SubmitInput{Key: key, Req: req, Body: body, Endpoint: "chat.completions", UserAgent: "test"})
		if err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	f.reqDone = submit("first question <script>alert(1)</script>")
	if _, err := svc.ClaimRequest(ctx, ch.ID, tok.ID, time.Second); err != nil {
		t.Fatal(err)
	}
	done, err := svc.CompleteRequest(ctx, ch.ID, tok.ID, f.reqDone, openai.Answer{Content: "an answer", ToolCalls: []openai.ToolCall{{Function: openai.FunctionCall{Name: "lookup", Arguments: `{"q":"x"}`}}}})
	if err != nil {
		t.Fatal(err)
	}
	f.conv = *done.ConversationID
	f.reqLive = submit("second question")
	if _, err := svc.ClaimRequest(ctx, ch.ID, tok.ID, time.Second); err != nil {
		t.Fatal(err)
	}
	part := "partial output"
	if err := svc.StreamDelta(ctx, ch.ID, tok.ID, f.reqLive, openai.Delta{Content: &part}); err != nil {
		t.Fatal(err)
	}
	f.reqWait = submit("third question")
	return f
}

func (f *webFixture) get(t *testing.T, path string, htmx bool) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.srv.URL+path, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.session})
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestPagesRender(t *testing.T) {
	f := newFixture(t)
	pages := []string{
		"/", "/?w=24h", "/requests", "/requests?status=queued&q=third", "/requests/table?status=completed",
		"/requests/" + f.reqDone, "/requests/" + f.reqLive, "/requests/" + f.reqWait, "/requests/" + f.reqWait + "/answer",
		"/conversations", "/conversations/" + f.conv,
		"/channels", "/channels/" + f.channel, "/channels/" + f.channel + "/edit",
		"/keys", "/keys/new", "/agents", "/agents/tokens/new",
		"/events", "/events?level=info", "/events/table", "/settings", "/docs", "/partials/stats",
	}
	for _, path := range pages {
		for _, htmx := range []bool{false, true} {
			status, body := f.get(t, path, htmx)
			if status != http.StatusOK {
				t.Errorf("GET %s (htmx=%v) = %d\n%s", path, htmx, status, firstLines(body, 6))
				continue
			}
			if strings.Contains(body, "template error") || strings.Contains(body, "<no value>") || strings.Contains(body, "%!") {
				t.Errorf("GET %s (htmx=%v) rendered a template problem:\n%s", path, htmx, firstLines(body, 12))
			}
		}
	}
}

func TestRequestPageContent(t *testing.T) {
	f := newFixture(t)
	_, body := f.get(t, "/requests/"+f.reqDone, false)
	for _, want := range []string{"an answer", "lookup", "be brief", "seed-channel", "seed-app", "request.completed"} {
		if !strings.Contains(body, want) {
			t.Errorf("completed request page missing %q", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("user content was not HTML-escaped")
	}
	_, live := f.get(t, "/requests/"+f.reqLive, false)
	if !strings.Contains(live, "partial output") || !strings.Contains(live, `data-live-output="`+f.reqLive+`"`) {
		t.Error("in-flight request page does not show the live stream")
	}
	_, conv := f.get(t, "/conversations/"+f.conv, false)
	if !strings.Contains(conv, "turn 1") || !strings.Contains(conv, "an answer") {
		t.Error("conversation thread missing turn content")
	}
}

func TestAuthRequired(t *testing.T) {
	f := newFixture(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/", "/requests", "/keys", "/settings", "/live", "/keys/new"} {
		resp, err := client.Get(f.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
			t.Errorf("GET %s without session = %d %q, want redirect to /login", path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	// Public endpoints stay reachable.
	for _, path := range []string{"/login", "/healthz", "/static/app.css", "/static/vendor/htmx.min.js"} {
		resp, err := client.Get(f.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestLoginFlows(t *testing.T) {
	f := newFixture(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := client.PostForm(f.srv.URL+"/login", url.Values{"password": {"wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", resp.StatusCode)
	}

	resp, err = client.PostForm(f.srv.URL+"/login", url.Values{"password": {"correct horse battery"}, "next": {"//evil.example"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("login = %d -> %q, want redirect to / (open redirect must be refused)", resp.StatusCode, resp.Header.Get("Location"))
	}
	var gotCookie bool
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie && c.Value != "" && c.HttpOnly {
			gotCookie = true
		}
	}
	if !gotCookie {
		t.Fatal("no HttpOnly session cookie set on login")
	}

	// Magic link works exactly once.
	tok, err := f.svc.CreateLoginToken(context.Background(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{http.StatusSeeOther, http.StatusUnauthorized} {
		resp, err := client.Get(f.srv.URL + "/login?token=" + tok)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("magic link use %d = %d, want %d", i+1, resp.StatusCode, want)
		}
	}
}

func TestCSRFAndActions(t *testing.T) {
	f := newFixture(t)
	post := func(path string, form url.Values, csrf string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.session})
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := post("/keys", url.Values{"name": {"no-csrf"}}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without CSRF token = %d, want 403", resp.StatusCode)
	}

	csrf := csrfFor(f.session)
	resp = post("/keys", url.Values{"name": {"made-in-ui"}, "rate_limit_rpm": {"30"}}, csrf)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "sk-sb-") {
		t.Fatalf("create key = %d\n%s", resp.StatusCode, firstLines(string(body), 8))
	}

	// Answer the queued request from the dashboard.
	resp = post("/requests/"+f.reqWait+"/answer", url.Values{"content": {"typed by a human"}, "finish_reason": {"stop"}}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("HX-Redirect") == "" {
		t.Fatalf("answer = %d redirect=%q", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}
	rec, err := f.svc.GetRequest(context.Background(), f.reqWait)
	if err != nil || rec.Status != core.StatusCompleted || rec.ResponseText == nil || *rec.ResponseText != "typed by a human" {
		t.Fatalf("request after manual answer: %+v %v", rec, err)
	}

	// Cancel the in-flight one.
	resp = post("/requests/"+f.reqLive+"/cancel", url.Values{}, csrf)
	resp.Body.Close()
	if rec, _ := f.svc.GetRequest(context.Background(), f.reqLive); rec.Status != core.StatusCancelled {
		t.Fatalf("status after cancel = %s", rec.Status)
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
