package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ucgeorge/switchboard/internal/db"
	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/openai"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	svc, err := New(d, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() {
		svc.Close()
		d.Close()
	})
	return svc
}

func msg(role, text string) openai.Message {
	c, _ := json.Marshal(text)
	return openai.Message{Role: role, Content: c}
}

func submit(t *testing.T, svc *Service, key sqlcgen.ApiKey, model string, msgs ...openai.Message) sqlcgen.Request {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"model": model, "messages": msgs})
	req, err := openai.ParseChatRequest(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r, err := svc.SubmitRequest(context.Background(), SubmitInput{Key: key, Req: req, Body: body, Endpoint: "chat.completions"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return r
}

func mustKey(t *testing.T, svc *Service) sqlcgen.ApiKey {
	t.Helper()
	k, plain, err := svc.CreateAPIKey(context.Background(), APIKeyInput{Name: "test"})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	got, err := svc.AuthenticateAPIKey(context.Background(), plain)
	if err != nil || got.ID != k.ID {
		t.Fatalf("authenticate key: %v", err)
	}
	return k
}

func mustChannel(t *testing.T, svc *Service, name string, models ...string) sqlcgen.Channel {
	t.Helper()
	ch, _, err := svc.OpenChannel(context.Background(), "tok_test", ChannelInput{Name: name, Models: models})
	if err != nil {
		t.Fatalf("open channel: %v", err)
	}
	return ch
}

func TestSubmitClaimComplete(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	key := mustKey(t, svc)
	ch := mustChannel(t, svc, "agent")

	r := submit(t, svc, key, "gpt-4o", msg("user", "hello"))
	if r.Status != StatusQueued {
		t.Fatalf("status = %s, want queued", r.Status)
	}

	claimed, err := svc.ClaimRequest(ctx, ch.ID, "tok_test", time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if claimed.ID != r.ID || claimed.Status != StatusClaimed || claimed.Attempts != 1 {
		t.Fatalf("unexpected claim: %+v", claimed)
	}

	done, err := svc.CompleteRequest(ctx, ch.ID, "tok_test", r.ID, openai.Answer{Content: "hi there"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if done.Status != StatusCompleted || done.ResponseBody == nil {
		t.Fatalf("unexpected completion: %+v", done)
	}
	var cc openai.ChatCompletion
	if err := json.Unmarshal([]byte(*done.ResponseBody), &cc); err != nil {
		t.Fatalf("response body: %v", err)
	}
	if cc.Object != "chat.completion" || *cc.Choices[0].Message.Content != "hi there" || cc.Choices[0].FinishReason != "stop" {
		t.Fatalf("unexpected response: %+v", cc)
	}

	outcome, _, err := svc.WaitRequest(ctx, r.ID)
	if err != nil || outcome.Status != StatusCompleted || outcome.Answer.Content != "hi there" {
		t.Fatalf("wait: %+v %v", outcome, err)
	}

	// A second completion must be rejected.
	if _, err := svc.CompleteRequest(ctx, ch.ID, "tok_test", r.ID, openai.Answer{Content: "again"}); err == nil {
		t.Fatal("expected error completing twice")
	}
}

func TestConversationLinking(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	key := mustKey(t, svc)
	ch := mustChannel(t, svc, "agent")

	answer := func(r sqlcgen.Request, text string) {
		t.Helper()
		if _, err := svc.ClaimRequest(ctx, ch.ID, "tok_test", time.Second); err != nil {
			t.Fatalf("claim: %v", err)
		}
		if _, err := svc.CompleteRequest(ctx, ch.ID, "tok_test", r.ID, openai.Answer{Content: text}); err != nil {
			t.Fatalf("complete: %v", err)
		}
	}

	r1 := submit(t, svc, key, "m", msg("system", "be brief"), msg("user", "capital of France?"))
	answer(r1, "Paris.")
	r2 := submit(t, svc, key, "m", msg("system", "be brief"), msg("user", "capital of France?"), msg("assistant", "Paris."), msg("user", "and Spain?"))
	answer(r2, "Madrid.")
	r3 := submit(t, svc, key, "m", msg("system", "be brief"), msg("user", "capital of France?"), msg("assistant", "Paris."), msg("user", "and Spain?"), msg("assistant", "Madrid."), msg("user", "and Italy?"))

	if *r1.ConversationID != *r2.ConversationID || *r2.ConversationID != *r3.ConversationID {
		t.Fatalf("turns not linked: %s %s %s", *r1.ConversationID, *r2.ConversationID, *r3.ConversationID)
	}
	if r1.TurnIndex != 0 || r2.TurnIndex != 1 || r3.TurnIndex != 2 {
		t.Fatalf("turn indexes = %d %d %d", r1.TurnIndex, r2.TurnIndex, r3.TurnIndex)
	}

	// A different first message starts a new conversation.
	other := submit(t, svc, key, "m", msg("system", "be brief"), msg("user", "what is 2+2?"))
	if *other.ConversationID == *r1.ConversationID {
		t.Fatal("unrelated request joined the conversation")
	}
	// The identical opening prompt sent fresh is also its own conversation.
	fresh := submit(t, svc, key, "m", msg("system", "be brief"), msg("user", "capital of France?"))
	if *fresh.ConversationID == *r1.ConversationID {
		t.Fatal("single-message request should not merge by root")
	}

	conv, err := svc.GetConversation(ctx, *r1.ConversationID)
	if err != nil || conv.TurnCount != 3 || conv.Title != "capital of France?" {
		t.Fatalf("conversation: %+v %v", conv, err)
	}
}

func TestRetryableFailureRequeues(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	key := mustKey(t, svc)
	ch := mustChannel(t, svc, "agent")
	r := submit(t, svc, key, "m", msg("user", "x"))

	for attempt := 1; attempt <= 3; attempt++ {
		c, err := svc.ClaimRequest(ctx, ch.ID, "tok_test", time.Second)
		if err != nil || c == nil {
			t.Fatalf("claim %d: %v %v", attempt, c, err)
		}
		if err := svc.FailRequest(ctx, ch.ID, "tok_test", r.ID, "boom", true); err != nil {
			t.Fatalf("fail %d: %v", attempt, err)
		}
	}
	got, _ := svc.GetRequest(ctx, r.ID)
	if got.Status != StatusFailed {
		t.Fatalf("after max attempts status = %s, want failed", got.Status)
	}
	outcome, _, _ := svc.WaitRequest(ctx, r.ID)
	if outcome.Status != StatusFailed {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestStreamedRequestIsNotRetried(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	key := mustKey(t, svc)
	ch := mustChannel(t, svc, "agent")
	r := submit(t, svc, key, "m", msg("user", "x"))
	if _, err := svc.ClaimRequest(ctx, ch.ID, "tok_test", time.Second); err != nil {
		t.Fatal(err)
	}
	part := "partial"
	if err := svc.StreamDelta(ctx, ch.ID, "tok_test", r.ID, openai.Delta{Content: &part}); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if err := svc.FailRequest(ctx, ch.ID, "tok_test", r.ID, "crashed", true); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetRequest(ctx, r.ID)
	if got.Status != StatusFailed {
		t.Fatalf("status = %s, want failed (output was already streamed)", got.Status)
	}
}

func TestLeaseExpiryRequeues(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if err := svc.SetSetting(ctx, KeyLease, "0"); err != nil {
		t.Fatal(err)
	}
	key := mustKey(t, svc)
	ch := mustChannel(t, svc, "agent")
	r := submit(t, svc, key, "m", msg("user", "x"))
	if _, err := svc.ClaimRequest(ctx, ch.ID, "tok_test", time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	svc.reapLeases(ctx)
	got, _ := svc.GetRequest(ctx, r.ID)
	if got.Status != StatusQueued || got.ChannelID != nil {
		t.Fatalf("after lease expiry: status=%s channel=%v", got.Status, got.ChannelID)
	}
}

func TestTimeoutExpires(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	key := mustKey(t, svc)
	r := submit(t, svc, key, "m", msg("user", "x"))
	if _, err := svc.DB.ExecContext(ctx, `UPDATE requests SET timeout_at = ? WHERE id = ?`, nowMs()-1, r.ID); err != nil {
		t.Fatal(err)
	}
	svc.reapTimeouts(ctx)
	outcome, _, err := svc.WaitRequest(ctx, r.ID)
	if err != nil || outcome.Status != StatusExpired {
		t.Fatalf("outcome = %+v %v", outcome, err)
	}
}

func TestModelRoutingAndConcurrency(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	key := mustKey(t, svc)
	gpt := mustChannel(t, svc, "gpt-agent", "gpt-4*")
	any := mustChannel(t, svc, "any-agent", "*")

	claude := submit(t, svc, key, "claude-x", msg("user", "a"))
	g1 := submit(t, svc, key, "gpt-4o", msg("user", "b"))
	g2 := submit(t, svc, key, "gpt-4o-mini", msg("user", "c"))

	// The gpt channel skips the claude request and takes the first gpt one.
	c, err := svc.ClaimRequest(ctx, gpt.ID, "tok_test", 0)
	if err != nil || c == nil || c.ID != g1.ID {
		t.Fatalf("gpt channel claimed %v (%v), want %s", c, err, g1.ID)
	}
	// Concurrency 1: nothing more until it finishes.
	if c2, _ := svc.ClaimRequest(ctx, gpt.ID, "tok_test", 0); c2 != nil {
		t.Fatalf("claimed %s beyond concurrency limit", c2.ID)
	}
	// The wildcard channel takes the oldest request: the claude one.
	c3, err := svc.ClaimRequest(ctx, any.ID, "tok_test", 0)
	if err != nil || c3 == nil || c3.ID != claude.ID {
		t.Fatalf("wildcard channel claimed %v (%v), want %s", c3, err, claude.ID)
	}
	if _, err := svc.CompleteRequest(ctx, gpt.ID, "tok_test", g1.ID, openai.Answer{Content: "ok"}); err != nil {
		t.Fatal(err)
	}
	c4, _ := svc.ClaimRequest(ctx, gpt.ID, "tok_test", 0)
	if c4 == nil || c4.ID != g2.ID {
		t.Fatalf("gpt channel next claim = %v, want %s", c4, g2.ID)
	}
}

func TestOwnershipAndCancel(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	key := mustKey(t, svc)
	ch := mustChannel(t, svc, "agent")
	r := submit(t, svc, key, "m", msg("user", "x"))

	// Another token cannot drive this channel.
	if _, err := svc.ClaimRequest(ctx, ch.ID, "tok_other", 0); !errors.Is(err, ErrChannelNotOwned) {
		t.Fatalf("err = %v, want ErrChannelNotOwned", err)
	}
	if _, err := svc.ClaimRequest(ctx, ch.ID, "tok_test", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelRequest(ctx, r.ID, "caller disconnected"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.CompleteRequest(ctx, ch.ID, "tok_test", r.ID, openai.Answer{Content: "late"})
	var state *RequestStateError
	if !errors.As(err, &state) || state.Status != StatusCancelled {
		t.Fatalf("err = %v, want cancelled state error", err)
	}
}

func TestLongPollWakesOnSubmit(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	key := mustKey(t, svc)
	ch := mustChannel(t, svc, "agent")

	got := make(chan *sqlcgen.Request, 1)
	go func() {
		r, _ := svc.ClaimRequest(ctx, ch.ID, "tok_test", 5*time.Second)
		got <- r
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	r := submit(t, svc, key, "m", msg("user", "x"))
	select {
	case c := <-got:
		if c == nil || c.ID != r.ID {
			t.Fatalf("claimed %v, want %s", c, r.ID)
		}
		if waited := time.Since(start); waited > time.Second {
			t.Fatalf("long-poll took %s to wake", waited)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("long-poll did not wake on submit")
	}
}

func TestManualAnswer(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	key := mustKey(t, svc)
	r := submit(t, svc, key, "m", msg("user", "x"))
	done, err := svc.AnswerRequest(ctx, r.ID, openai.Answer{Content: "from a human"}, false)
	if err != nil || done.Status != StatusCompleted {
		t.Fatalf("answer: %+v %v", done, err)
	}
	a, ok := ParseAnswer(done)
	if !ok || a.Content != "from a human" {
		t.Fatalf("answer = %+v", a)
	}
}

func TestOAuthPKCEFlow(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	client, _, err := svc.RegisterOAuthClient(ctx, OAuthClientInput{Name: "inspector", RedirectURIs: []string{"http://localhost:9999/cb"}})
	if err != nil {
		t.Fatal(err)
	}
	// verifier/challenge pair from RFC 7636 appendix B.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	code, err := svc.IssueAuthCode(ctx, client.ID, "http://localhost:9999/cb", challenge, "S256", "mcp", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExchangeAuthCode(ctx, code, client.ID, "http://localhost:9999/cb", "wrong-verifier"); err == nil {
		t.Fatal("expected PKCE failure")
	}
	pair, err := svc.ExchangeAuthCode(ctx, code, client.ID, "http://localhost:9999/cb", verifier)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if _, err := svc.ExchangeAuthCode(ctx, code, client.ID, "http://localhost:9999/cb", verifier); err == nil {
		t.Fatal("authorization code must be single-use")
	}
	tok, err := svc.AuthenticateAgentToken(ctx, pair.AccessToken)
	if err != nil || tok.Kind != TokenKindOAuth {
		t.Fatalf("authenticate: %+v %v", tok, err)
	}
	next, err := svc.RefreshOAuthToken(ctx, pair.RefreshToken, client.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := svc.AuthenticateAgentToken(ctx, pair.AccessToken); err == nil {
		t.Fatal("old access token should be invalid after rotation")
	}
	if _, err := svc.AuthenticateAgentToken(ctx, next.AccessToken); err != nil {
		t.Fatalf("rotated token: %v", err)
	}
}

func TestOfflineChannelCannotBeTakenOver(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	first := mustChannel(t, svc, "private-agent")
	if err := svc.Q.SetChannelStatus(ctx, sqlcgen.SetChannelStatusParams{ID: first.ID, Status: ChannelOffline}); err != nil {
		t.Fatal(err)
	}
	other, _, err := svc.OpenChannel(ctx, "different-token", ChannelInput{Name: "private-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == first.ID {
		t.Fatal("foreign token took over offline channel")
	}
	original, _ := svc.GetChannel(ctx, first.ID)
	if deref(original.AgentTokenID) != "tok_test" {
		t.Fatal("original owner changed")
	}
}
