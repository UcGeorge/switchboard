package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/events"
	"github.com/ucgeorge/switchboard/internal/ids"
	"github.com/ucgeorge/switchboard/internal/openai"
)

// Request statuses.
const (
	StatusQueued    = "queued"
	StatusClaimed   = "claimed"
	StatusStreaming = "streaming"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"
)

// AllStatuses lists statuses in display order.
var AllStatuses = []string{StatusQueued, StatusClaimed, StatusStreaming, StatusCompleted, StatusFailed, StatusCancelled, StatusExpired}

// IsTerminal reports whether a status is final.
func IsTerminal(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusExpired:
		return true
	}
	return false
}

// IsActive reports whether a request is still being worked.
func IsActive(status string) bool { return !IsTerminal(status) }

// Broker errors.
var (
	ErrRequestNotFound = errors.New("request not found")
	ErrQueueFull       = errors.New("queue is full")
)

// RequestStateError reports an operation that conflicts with a request's
// current state (e.g. completing a request another channel holds).
type RequestStateError struct {
	ID     string
	Status string
	Owner  string
}

func (e *RequestStateError) Error() string {
	if e.Owner != "" {
		return fmt.Sprintf("request %s is %s (held by channel %s)", e.ID, e.Status, e.Owner)
	}
	return fmt.Sprintf("request %s is %s", e.ID, e.Status)
}

// SubmitInput is an incoming OpenAI-compatible request.
type SubmitInput struct {
	Key       sqlcgen.ApiKey
	Req       *openai.ChatRequest
	Endpoint  string // "chat.completions" | "completions"
	Body      []byte
	ClientIP  string
	UserAgent string
}

// Outcome is the terminal result of a request as seen by its caller.
type Outcome struct {
	Status string
	Answer *openai.Answer
	Usage  openai.Usage
	Error  string
}

// live request registry (in-memory streaming + completion signalling)

const liveDeltaBuffer = 4096

type liveRequest struct {
	id        string
	mu        sync.Mutex
	deltas    []openai.Delta
	content   strings.Builder
	toolCalls int
	calls     []openai.ToolCall
	subs      map[int]chan openai.Delta
	nextSub   int
	done      chan struct{}
	finished  bool
	outcome   Outcome
}

func newLive(id string) *liveRequest {
	return &liveRequest{id: id, subs: map[int]chan openai.Delta{}, done: make(chan struct{})}
}

func (l *liveRequest) publishDelta(d openai.Delta) (first bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return false
	}
	first = len(l.deltas) == 0
	l.deltas = append(l.deltas, d)
	if d.Content != nil {
		l.content.WriteString(*d.Content)
	}
	l.toolCalls += len(d.ToolCalls)
	for _, call := range d.ToolCalls {
		if call.Function != nil {
			l.calls = append(l.calls, openai.ToolCall{ID: call.ID, Type: call.Type, Function: openai.FunctionCall{Name: call.Function.Name, Arguments: call.Function.Arguments}})
		}
	}

	for _, ch := range l.subs {
		select {
		case ch <- d:
		default:
		}
	}
	return first
}

func (l *liveRequest) finish(o Outcome) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return
	}
	l.finished = true
	l.outcome = o
	close(l.done)
}

func (l *liveRequest) subscribe() (<-chan openai.Delta, []openai.Delta, func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ch := make(chan openai.Delta, liveDeltaBuffer)
	id := l.nextSub
	l.nextSub++
	l.subs[id] = ch
	replay := append([]openai.Delta(nil), l.deltas...)
	return ch, replay, func() {
		l.mu.Lock()
		delete(l.subs, id)
		l.mu.Unlock()
	}
}

func (l *liveRequest) streamed() (string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.content.String(), l.toolCalls
}

func (l *liveRequest) streamedCalls() []openai.ToolCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]openai.ToolCall(nil), l.calls...)
}

func (l *liveRequest) result() (Outcome, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.outcome, l.finished
}

func (s *Service) getLive(id string) *liveRequest {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	return s.live[id]
}

func (s *Service) ensureLive(id string) *liveRequest {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	if l, ok := s.live[id]; ok {
		return l
	}
	l := newLive(id)
	s.live[id] = l
	return l
}

// finishLive publishes the outcome to waiters and drops the entry after a
// grace period so late dashboard subscribers can still read the result.
func (s *Service) finishLive(id string, o Outcome) {
	l := s.ensureLive(id)
	l.finish(o)
	time.AfterFunc(time.Minute, func() {
		s.liveMu.Lock()
		if cur, ok := s.live[id]; ok && cur == l {
			delete(s.live, id)
		}
		s.liveMu.Unlock()
	})
}

// LiveCount reports in-memory tracked requests (diagnostics).
func (s *Service) LiveCount() int {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	return len(s.live)
}

// SubscribeRequest attaches to a request's live delta stream. replay holds
// deltas emitted before subscription; done closes when the request ends.
func (s *Service) SubscribeRequest(id string) (deltas <-chan openai.Delta, replay []openai.Delta, done <-chan struct{}, unsub func()) {
	l := s.ensureLive(id)
	ch, rp, un := l.subscribe()
	return ch, rp, l.done, un
}

// queue signalling

func (s *Service) signalQueue() {
	s.queueMu.Lock()
	close(s.queueCh)
	s.queueCh = make(chan struct{})
	s.queueMu.Unlock()
}

func (s *Service) queueWait() <-chan struct{} {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return s.queueCh
}

// submit

// SubmitRequest records an incoming request, links it to its conversation
// and places it on the queue.
func (s *Service) SubmitRequest(ctx context.Context, in SubmitInput) (sqlcgen.Request, error) {
	st := s.Settings()
	if st.MaxQueueDepth > 0 {
		depth, err := s.Q.CountQueued(ctx)
		if err != nil {
			return sqlcgen.Request{}, err
		}
		if depth >= st.MaxQueueDepth {
			return sqlcgen.Request{}, ErrQueueFull
		}
	}
	model := strings.TrimSpace(in.Req.Model)
	if model == "" {
		if len(st.Models) > 0 {
			model = st.Models[0]
		} else {
			model = "switchboard/auto"
		}
	}
	if in.Endpoint == "" {
		in.Endpoint = "chat.completions"
	}
	timeout := st.RequestTimeout
	if in.Key.TimeoutSeconds > 0 {
		timeout = time.Duration(in.Key.TimeoutSeconds) * time.Second
	}
	msgs := in.Req.Messages
	prefixes := openai.PrefixHashes(msgs)
	now := nowMs()
	id := ids.New("req")

	var created sqlcgen.Request
	var link convLink
	err := s.WithTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		link, err = s.linkConversation(ctx, q, in.Key.ID, id, model, in.Req, prefixes, now)
		if err != nil {
			return err
		}
		created, err = q.CreateRequest(ctx, sqlcgen.CreateRequestParams{
			ID:             id,
			ApiKeyID:       in.Key.ID,
			RouteChannelID: in.Key.PinnedChannelID,
			ConversationID: ptr(link.ConversationID),
			TurnIndex:      link.TurnIndex,
			Model:          model,
			Endpoint:       in.Endpoint,
			Stream:         in.Req.Stream,
			Priority:       in.Key.Priority,
			RequestBody:    string(in.Body),
			HistoryHash:    link.HistoryHash,
			FullHash:       prefixes[len(msgs)],
			MessageCount:   int64(len(msgs)),
			PromptChars:    int64(openai.PromptChars(msgs)),
			PromptTokens:   int64(openai.EstimatePromptTokens(in.Req)),
			MaxAttempts:    st.MaxAttempts,
			ClientIp:       in.ClientIP,
			UserAgent:      in.UserAgent,
			CreatedAt:      now,
			TimeoutAt:      now + timeout.Milliseconds(),
		})
		if err != nil {
			return err
		}
		return q.TouchAPIKey(ctx, sqlcgen.TouchAPIKeyParams{LastUsedAt: ptr(now), ID: in.Key.ID})
	})
	if err != nil {
		return sqlcgen.Request{}, err
	}
	s.ensureLive(id)
	s.publish(events.Event{
		Kind: events.RequestQueued, RequestID: id, APIKeyID: in.Key.ID, ConversationID: link.ConversationID,
		Message: fmt.Sprintf("Queued %s request for %s (%d messages, turn %d)", in.Endpoint, model, len(msgs), link.TurnIndex+1),
		Data:    map[string]any{"model": model, "stream": in.Req.Stream, "messages": len(msgs), "turn": link.TurnIndex, "key": in.Key.Name, "timeout_s": int(timeout.Seconds())},
	})
	s.signalQueue()
	return created, nil
}

// claim

// ClaimRequest hands the next eligible queued request to a channel, waiting
// up to wait for one to arrive. It returns nil when nothing was claimed.
func (s *Service) ClaimRequest(ctx context.Context, channelID, tokenID string, wait time.Duration) (*sqlcgen.Request, error) {
	deadline := time.Now().Add(wait)
	for {
		sig := s.queueWait()
		r, err := s.tryClaim(ctx, channelID, tokenID)
		if err != nil || r != nil {
			return r, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, nil
		}
		timer := time.NewTimer(min(remaining, 5*time.Second))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-sig:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (s *Service) tryClaim(ctx context.Context, channelID, tokenID string) (*sqlcgen.Request, error) {
	var claimed *sqlcgen.Request
	var ch sqlcgen.Channel
	wasOffline := false
	now := nowMs()
	lease := now + s.Settings().Lease.Milliseconds()

	err := s.WithTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		ch, err = s.ownedChannel(ctx, q, channelID, tokenID)
		if err != nil {
			return err
		}
		wasOffline = ch.Status == ChannelOffline
		if err := q.TouchChannel(ctx, sqlcgen.TouchChannelParams{LastSeenAt: now, ID: channelID}); err != nil {
			return err
		}
		if ch.Status == ChannelDraining {
			return nil
		}
		inflight, err := q.CountChannelInFlight(ctx, strp(channelID))
		if err != nil {
			return err
		}
		if inflight >= ch.Concurrency {
			return nil
		}
		cands, err := q.ListClaimableRequests(ctx, sqlcgen.ListClaimableRequestsParams{Now: now, ChannelID: strp(channelID)})
		if err != nil {
			return err
		}
		for _, r := range cands {
			if !ChannelServesModel(ch, r.Model) {
				continue
			}
			n, err := q.ClaimRequest(ctx, sqlcgen.ClaimRequestParams{ChannelID: strp(channelID), ClaimedAt: ptr(now), LeaseExpiresAt: ptr(lease), ID: r.ID})
			if err != nil {
				return err
			}
			if n == 1 {
				rr, err := q.GetRequest(ctx, r.ID)
				if err != nil {
					return err
				}
				claimed = &rr
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if wasOffline {
		s.publish(events.Event{Kind: events.ChannelOnline, ChannelID: ch.ID, Message: fmt.Sprintf("Channel %q back online", ch.Name)})
	}
	if claimed != nil {
		s.publish(events.Event{
			Kind: events.RequestClaimed, RequestID: claimed.ID, ChannelID: ch.ID, APIKeyID: claimed.ApiKeyID, ConversationID: deref(claimed.ConversationID),
			Message: fmt.Sprintf("Claimed by %q (attempt %d, waited %s)", ch.Name, claimed.Attempts, time.Duration(now-claimed.CreatedAt)*time.Millisecond),
			Data:    map[string]any{"channel": ch.Name, "attempt": claimed.Attempts, "queue_ms": now - claimed.CreatedAt},
		})
	}
	return claimed, nil
}

// streaming

// StreamDelta appends partial output from the serving channel. The first
// delta moves the request to streaming and records time-to-first-token.
func (s *Service) StreamDelta(ctx context.Context, channelID, tokenID, requestID string, d openai.Delta) error {
	if _, err := s.ownedChannel(ctx, s.Q, channelID, tokenID); err != nil {
		return err
	}
	now := nowMs()
	lease := now + s.Settings().Lease.Milliseconds()
	n, err := s.Q.MarkRequestStreaming(ctx, sqlcgen.MarkRequestStreamingParams{FirstTokenAt: ptr(now), LeaseExpiresAt: ptr(lease), ID: requestID, ChannelID: strp(channelID)})
	if err != nil {
		return err
	}
	if n == 0 {
		return s.stateError(ctx, requestID)
	}
	_ = s.Q.TouchChannel(ctx, sqlcgen.TouchChannelParams{LastSeenAt: now, ID: channelID})
	live := s.ensureLive(requestID)
	first := live.publishDelta(d)
	if first {
		s.publish(events.Event{Kind: events.RequestStreaming, RequestID: requestID, ChannelID: channelID, Message: "First token received", Data: map[string]any{"ttft_ms": now - s.createdAt(ctx, requestID)}})
	}
	text := ""
	if d.Content != nil {
		text = *d.Content
	}
	s.publish(events.Event{Kind: events.RequestDelta, RequestID: requestID, ChannelID: channelID, Ephemeral: true, Message: "delta",
		Data: map[string]any{"text": text, "tool_calls": len(d.ToolCalls)}})
	return nil
}

func (s *Service) createdAt(ctx context.Context, requestID string) int64 {
	r, err := s.Q.GetRequest(ctx, requestID)
	if err != nil {
		return nowMs()
	}
	return r.CreatedAt
}

// ExtendLease renews a claimed request's lease (agent heartbeat per request).
func (s *Service) ExtendLease(ctx context.Context, channelID, tokenID, requestID string) error {
	if _, err := s.ownedChannel(ctx, s.Q, channelID, tokenID); err != nil {
		return err
	}
	lease := nowMs() + s.Settings().Lease.Milliseconds()
	n, err := s.Q.ExtendRequestLease(ctx, sqlcgen.ExtendRequestLeaseParams{LeaseExpiresAt: ptr(lease), ID: requestID, ChannelID: strp(channelID)})
	if err != nil {
		return err
	}
	if n == 0 {
		return s.stateError(ctx, requestID)
	}
	return nil
}

func (s *Service) stateError(ctx context.Context, requestID string) error {
	r, err := s.Q.GetRequest(ctx, requestID)
	if err != nil {
		if isNoRows(err) {
			return ErrRequestNotFound
		}
		return err
	}
	return &RequestStateError{ID: r.ID, Status: r.Status, Owner: deref(r.ChannelID)}
}

// complete / fail / release / cancel

// CompleteRequest records the final answer, builds the OpenAI response and
// wakes the caller.
func (s *Service) CompleteRequest(ctx context.Context, channelID, tokenID, requestID string, a openai.Answer) (sqlcgen.Request, error) {
	ch, err := s.ownedChannel(ctx, s.Q, channelID, tokenID)
	if err != nil {
		return sqlcgen.Request{}, err
	}
	r, err := s.Q.GetRequest(ctx, requestID)
	if err != nil {
		if isNoRows(err) {
			return sqlcgen.Request{}, ErrRequestNotFound
		}
		return sqlcgen.Request{}, err
	}
	if deref(r.ChannelID) != channelID || !(r.Status == StatusClaimed || r.Status == StatusStreaming) {
		return sqlcgen.Request{}, &RequestStateError{ID: r.ID, Status: r.Status, Owner: deref(r.ChannelID)}
	}

	a = a.Normalized(func() string { return ids.Random(16) })
	live := s.ensureLive(requestID)
	streamedContent, _ := live.streamed()
	if len(a.ToolCalls) == 0 {
		a.ToolCalls = live.streamedCalls()
		if len(a.ToolCalls) > 0 && a.FinishReason == "stop" {
			a.FinishReason = "tool_calls"
		}
		a = a.Normalized(func() string { return ids.Random(16) })
	}
	if a.Content == "" && streamedContent != "" {
		a.Content = streamedContent
	}

	usage := openai.Usage{PromptTokens: int(r.PromptTokens)}
	estimated := true
	if a.Usage != nil && (a.Usage.PromptTokens > 0 || a.Usage.CompletionTokens > 0) {
		usage = *a.Usage
		estimated = false
	} else {
		n := openai.EstimateTokens(a.Content)
		for _, tc := range a.ToolCalls {
			n += openai.EstimateTokens(tc.Function.Name + tc.Function.Arguments)
		}
		usage.CompletionTokens = n
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	created := MsTime(r.CreatedAt)
	var body []byte
	if r.Endpoint == "completions" {
		body, _ = json.Marshal(openai.BuildTextCompletion(r.ID, r.Model, created, a, usage))
	} else {
		body, _ = json.Marshal(openai.BuildChatCompletion(r.ID, r.Model, created, a, usage))
	}
	nextHash := openai.ChainHash(r.FullHash, a.AssistantMessage())
	now := nowMs()

	err = s.WithTx(ctx, func(q *sqlcgen.Queries) error {
		n, err := q.CompleteRequest(ctx, sqlcgen.CompleteRequestParams{
			ResponseBody:     ptr(string(body)),
			ResponseText:     ptr(a.Content),
			NextHash:         ptr(nextHash),
			PromptTokens:     int64(usage.PromptTokens),
			CompletionTokens: int64(usage.CompletionTokens),
			UsageEstimated:   estimated,
			FinishReason:     ptr(a.FinishReason),
			CompletedAt:      ptr(now),
			FirstTokenAt:     ptr(now),
			ID:               r.ID,
			ChannelID:        strp(channelID),
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return &RequestStateError{ID: r.ID, Status: r.Status, Owner: deref(r.ChannelID)}
		}
		if err := q.IncrChannelServed(ctx, sqlcgen.IncrChannelServedParams{TotalLatencyMs: now - r.CreatedAt, ID: channelID}); err != nil {
			return err
		}
		if r.ConversationID != nil {
			if conv, err := q.GetConversation(ctx, *r.ConversationID); err == nil {
				_ = q.BumpConversation(ctx, sqlcgen.BumpConversationParams{
					TurnCount: max(conv.TurnCount, r.TurnIndex+1), LastRequestID: ptr(r.ID), Model: r.Model, UpdatedAt: now, ID: conv.ID,
				})
			}
		}
		return q.TouchChannel(ctx, sqlcgen.TouchChannelParams{LastSeenAt: now, ID: channelID})
	})
	if err != nil {
		return sqlcgen.Request{}, err
	}

	s.finishLive(r.ID, Outcome{Status: StatusCompleted, Answer: &a, Usage: usage})
	ttft := now - r.CreatedAt
	if r.FirstTokenAt != nil {
		ttft = *r.FirstTokenAt - r.CreatedAt
	}
	s.publish(events.Event{
		Kind: events.RequestCompleted, RequestID: r.ID, ChannelID: channelID, APIKeyID: r.ApiKeyID, ConversationID: deref(r.ConversationID),
		Message: fmt.Sprintf("Completed by %q in %s (%s)", ch.Name, time.Duration(now-r.CreatedAt)*time.Millisecond, a.FinishReason),
		Data: map[string]any{"channel": ch.Name, "latency_ms": now - r.CreatedAt, "ttft_ms": ttft, "finish_reason": a.FinishReason,
			"prompt_tokens": usage.PromptTokens, "completion_tokens": usage.CompletionTokens, "usage_estimated": estimated, "tool_calls": len(a.ToolCalls)},
	})
	updated, err := s.Q.GetRequest(ctx, r.ID)
	return updated, err
}

// FailRequest reports that the serving channel could not answer. Retryable
// failures return the request to the queue while attempts remain and nothing
// has been streamed to the caller yet.
func (s *Service) FailRequest(ctx context.Context, channelID, tokenID, requestID, reason string, retryable bool) error {
	if _, err := s.ownedChannel(ctx, s.Q, channelID, tokenID); err != nil {
		return err
	}
	r, err := s.Q.GetRequest(ctx, requestID)
	if err != nil {
		if isNoRows(err) {
			return ErrRequestNotFound
		}
		return err
	}
	if deref(r.ChannelID) != channelID || !IsActive(r.Status) || r.Status == StatusQueued {
		return &RequestStateError{ID: r.ID, Status: r.Status, Owner: deref(r.ChannelID)}
	}
	if reason == "" {
		reason = "agent reported failure"
	}
	_ = s.Q.IncrChannelFailed(ctx, channelID)
	if retryable {
		s.requeueOrFail(ctx, r, reason, true)
	} else {
		s.failNow(ctx, r, reason)
	}
	return nil
}

// ReleaseRequest gives a claimed request back without blame (e.g. the agent
// is shutting down). Partially streamed requests cannot be released.
func (s *Service) ReleaseRequest(ctx context.Context, channelID, tokenID, requestID, reason string) error {
	if _, err := s.ownedChannel(ctx, s.Q, channelID, tokenID); err != nil {
		return err
	}
	r, err := s.Q.GetRequest(ctx, requestID)
	if err != nil {
		if isNoRows(err) {
			return ErrRequestNotFound
		}
		return err
	}
	if deref(r.ChannelID) != channelID || !(r.Status == StatusClaimed || r.Status == StatusStreaming) {
		return &RequestStateError{ID: r.ID, Status: r.Status, Owner: deref(r.ChannelID)}
	}
	if reason == "" {
		reason = "released by agent"
	}
	s.requeueOrFail(ctx, r, reason, true)
	return nil
}

// requeueOrFail returns a request to the queue if it can safely be retried,
// otherwise fails it. Safe means: no partial output reached the caller and
// attempts remain.
func (s *Service) requeueOrFail(ctx context.Context, r sqlcgen.Request, reason string, _ bool) {
	canRetry := r.FirstTokenAt == nil && r.Attempts < r.MaxAttempts
	if canRetry {
		n, err := s.Q.RequeueRequest(ctx, sqlcgen.RequeueRequestParams{ErrorMessage: ptr(reason), ID: r.ID})
		if err == nil && n > 0 {
			s.publish(events.Event{Kind: events.RequestRequeued, Level: events.LevelWarn, RequestID: r.ID, ChannelID: deref(r.ChannelID), APIKeyID: r.ApiKeyID, ConversationID: deref(r.ConversationID),
				Message: fmt.Sprintf("Requeued: %s (attempt %d of %d)", reason, r.Attempts, r.MaxAttempts), Data: map[string]any{"reason": reason, "attempt": r.Attempts}})
			s.signalQueue()
			return
		}
	}
	why := reason
	if r.FirstTokenAt != nil {
		why += " (partial response already streamed; cannot retry)"
	} else if r.Attempts >= r.MaxAttempts {
		why += fmt.Sprintf(" (no retries left after %d attempts)", r.Attempts)
	}
	s.failNow(ctx, r, why)
}

func (s *Service) failNow(ctx context.Context, r sqlcgen.Request, reason string) {
	n, err := s.Q.FailRequest(ctx, sqlcgen.FailRequestParams{ErrorMessage: ptr(reason), CompletedAt: ptr(nowMs()), ID: r.ID})
	if err != nil || n == 0 {
		return
	}
	s.finishLive(r.ID, Outcome{Status: StatusFailed, Error: reason})
	s.publish(events.Event{Kind: events.RequestFailed, Level: events.LevelError, RequestID: r.ID, ChannelID: deref(r.ChannelID), APIKeyID: r.ApiKeyID, ConversationID: deref(r.ConversationID),
		Message: "Failed: " + reason, Data: map[string]any{"reason": reason, "attempts": r.Attempts}})
}

// CancelRequest aborts a request on behalf of its caller or an operator.
func (s *Service) CancelRequest(ctx context.Context, requestID, reason string) error {
	r, err := s.Q.GetRequest(ctx, requestID)
	if err != nil {
		if isNoRows(err) {
			return ErrRequestNotFound
		}
		return err
	}
	if IsTerminal(r.Status) {
		return &RequestStateError{ID: r.ID, Status: r.Status}
	}
	if reason == "" {
		reason = "cancelled"
	}
	n, err := s.Q.CancelRequest(ctx, sqlcgen.CancelRequestParams{ErrorMessage: ptr(reason), CompletedAt: ptr(nowMs()), ID: requestID})
	if err != nil {
		return err
	}
	if n == 0 {
		return s.stateError(ctx, requestID)
	}
	s.finishLive(requestID, Outcome{Status: StatusCancelled, Error: reason})
	s.publish(events.Event{Kind: events.RequestCancelled, Level: events.LevelWarn, RequestID: r.ID, ChannelID: deref(r.ChannelID), APIKeyID: r.ApiKeyID, ConversationID: deref(r.ConversationID),
		Message: "Cancelled: " + reason, Data: map[string]any{"reason": reason, "was": r.Status}})
	return nil
}

// RequeueRequest is the operator action for a stuck in-flight request.
func (s *Service) RequeueRequest(ctx context.Context, requestID, reason string) error {
	r, err := s.Q.GetRequest(ctx, requestID)
	if err != nil {
		if isNoRows(err) {
			return ErrRequestNotFound
		}
		return err
	}
	if r.Status != StatusClaimed && r.Status != StatusStreaming {
		return &RequestStateError{ID: r.ID, Status: r.Status, Owner: deref(r.ChannelID)}
	}
	if reason == "" {
		reason = "requeued by operator"
	}
	if r.ChannelID != nil {
		_ = s.Q.IncrChannelFailed(ctx, *r.ChannelID)
	}
	s.requeueOrFail(ctx, r, reason, false)
	return nil
}

// AnswerRequest lets a human (CLI/dashboard) answer a request directly via
// the "manual" channel. force steals a request another channel holds.
func (s *Service) AnswerRequest(ctx context.Context, requestID string, a openai.Answer, force bool) (sqlcgen.Request, error) {
	r, err := s.Q.GetRequest(ctx, requestID)
	if err != nil {
		if isNoRows(err) {
			return sqlcgen.Request{}, ErrRequestNotFound
		}
		return sqlcgen.Request{}, err
	}
	ch, err := s.manualChannel(ctx)
	if err != nil {
		return sqlcgen.Request{}, err
	}
	now := nowMs()
	lease := now + s.Settings().Lease.Milliseconds()
	switch r.Status {
	case StatusQueued:
	case StatusClaimed, StatusStreaming:
		if deref(r.ChannelID) == ch.ID {
			return s.CompleteRequest(ctx, ch.ID, "", requestID, a)
		}
		if !force {
			return sqlcgen.Request{}, &RequestStateError{ID: r.ID, Status: r.Status, Owner: deref(r.ChannelID)}
		}
		if _, err := s.Q.RequeueRequest(ctx, sqlcgen.RequeueRequestParams{ErrorMessage: ptr("taken over by manual answer"), ID: r.ID}); err != nil {
			return sqlcgen.Request{}, err
		}
	default:
		return sqlcgen.Request{}, &RequestStateError{ID: r.ID, Status: r.Status, Owner: deref(r.ChannelID)}
	}
	n, err := s.Q.ClaimRequest(ctx, sqlcgen.ClaimRequestParams{ChannelID: strp(ch.ID), ClaimedAt: ptr(now), LeaseExpiresAt: ptr(lease), ID: r.ID})
	if err != nil {
		return sqlcgen.Request{}, err
	}
	if n == 0 {
		return sqlcgen.Request{}, s.stateError(ctx, requestID)
	}
	s.publish(events.Event{Kind: events.RequestClaimed, RequestID: r.ID, ChannelID: ch.ID, APIKeyID: r.ApiKeyID, ConversationID: deref(r.ConversationID),
		Message: "Claimed for manual answer", Data: map[string]any{"channel": ch.Name, "queue_ms": now - r.CreatedAt}})
	return s.CompleteRequest(ctx, ch.ID, "", requestID, a)
}

// waiting (caller side)

// WaitRequest blocks until the request reaches a terminal state or ctx ends.
// It listens on the in-memory signal and also polls the database so answers
// written by another process (the CLI) are picked up.
func (s *Service) WaitRequest(ctx context.Context, requestID string) (Outcome, sqlcgen.Request, error) {
	live := s.getLive(requestID)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if live != nil {
			select {
			case <-live.done:
				o, _ := live.result()
				r, err := s.Q.GetRequest(ctx, requestID)
				return o, r, err
			case <-ctx.Done():
				return Outcome{}, sqlcgen.Request{}, ctx.Err()
			case <-ticker.C:
			}
		} else {
			select {
			case <-ctx.Done():
				return Outcome{}, sqlcgen.Request{}, ctx.Err()
			case <-ticker.C:
			}
		}
		r, err := s.Q.GetRequest(ctx, requestID)
		if err != nil {
			return Outcome{}, sqlcgen.Request{}, err
		}
		if IsTerminal(r.Status) {
			// The request was finished by another process (e.g. `switchboard
			// requests answer`). That process recorded the event; tell this
			// process's live subscribers so dashboards refresh.
			o := OutcomeFromRequest(r)
			if live != nil {
				live.finish(o)
			}
			s.publish(events.Event{Kind: "request." + r.Status, RequestID: r.ID, ChannelID: deref(r.ChannelID), APIKeyID: r.ApiKeyID,
				ConversationID: deref(r.ConversationID), Message: "Request " + r.Status + " (updated outside this process)", Ephemeral: true})
			return o, r, nil
		}
	}
}

// OutcomeFromRequest reconstructs an Outcome from a stored terminal request.
func OutcomeFromRequest(r sqlcgen.Request) Outcome {
	o := Outcome{Status: r.Status, Error: deref(r.ErrorMessage)}
	if r.Status == StatusCompleted {
		if a, ok := ParseAnswer(r); ok {
			o.Answer = &a
		}
		o.Usage = openai.Usage{PromptTokens: int(r.PromptTokens), CompletionTokens: int(r.CompletionTokens), TotalTokens: int(r.PromptTokens + r.CompletionTokens)}
	}
	return o
}

// background upkeep

func (s *Service) recoverOnStart(ctx context.Context) error {
	n, err := s.Q.CancelAllActiveRequests(ctx, sqlcgen.CancelAllActiveRequestsParams{
		ErrorMessage: ptr("server restarted; caller connection lost"), CompletedAt: ptr(nowMs()),
	})
	if err != nil {
		return err
	}
	if n > 0 {
		s.publish(events.Event{Kind: events.RequestCancelled, Level: events.LevelWarn, Message: fmt.Sprintf("Cancelled %d in-flight request(s) left over from the previous run", n), Data: map[string]any{"count": n}})
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE channels SET status = 'offline' WHERE status != 'offline' AND closed_at IS NULL AND name != ?`, ManualChannelName)
	return err
}

func (s *Service) reaperLoop(ctx context.Context) {
	defer close(s.reaperDone)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	prune := time.NewTicker(10 * time.Minute)
	defer prune.Stop()
	sweep := time.NewTicker(time.Minute)
	defer sweep.Stop()
	s.prune(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			// Pick up settings changed by another process (the CLI writes
			// straight to the database).
			if err := s.reloadSettings(ctx); err != nil {
				s.Log.Warn("reload settings", "err", err)
			}
			s.reapLeases(ctx)
			s.reapTimeouts(ctx)
			s.markStaleChannels(ctx)
		case <-sweep.C:
			s.limiter.Sweep(10 * time.Minute)
			s.loginLimiter.Sweep(10 * time.Minute)
		case <-prune.C:
			s.prune(ctx)
		}
	}
}

func (s *Service) reapLeases(ctx context.Context) {
	expired, err := s.Q.ListExpiredLeases(ctx, ptr(nowMs()))
	if err != nil {
		s.Log.Warn("list expired leases", "err", err)
		return
	}
	for _, r := range expired {
		s.publish(events.Event{Kind: events.RequestLeaseExpired, Level: events.LevelWarn, RequestID: r.ID, ChannelID: deref(r.ChannelID), APIKeyID: r.ApiKeyID,
			Message: fmt.Sprintf("Lease expired on attempt %d", r.Attempts)})
		if r.ChannelID != nil {
			_ = s.Q.IncrChannelFailed(ctx, *r.ChannelID)
		}
		s.requeueOrFail(ctx, r, "agent lease expired", true)
	}
}

func (s *Service) reapTimeouts(ctx context.Context) {
	timedOut, err := s.Q.ListTimedOutRequests(ctx, nowMs())
	if err != nil {
		s.Log.Warn("list timed out requests", "err", err)
		return
	}
	for _, r := range timedOut {
		reason := fmt.Sprintf("request timed out after %s", time.Duration(r.TimeoutAt-r.CreatedAt)*time.Millisecond)
		n, err := s.Q.ExpireRequest(ctx, sqlcgen.ExpireRequestParams{ErrorMessage: ptr(reason), CompletedAt: ptr(nowMs()), ID: r.ID})
		if err != nil || n == 0 {
			continue
		}
		if r.ChannelID != nil {
			_ = s.Q.IncrChannelFailed(ctx, *r.ChannelID)
		}
		s.finishLive(r.ID, Outcome{Status: StatusExpired, Error: reason})
		s.publish(events.Event{Kind: events.RequestExpired, Level: events.LevelWarn, RequestID: r.ID, ChannelID: deref(r.ChannelID), APIKeyID: r.ApiKeyID, ConversationID: deref(r.ConversationID),
			Message: "Expired: " + reason, Data: map[string]any{"was": r.Status, "attempts": r.Attempts}})
	}
}

func (s *Service) prune(ctx context.Context) {
	st := s.Settings()
	now := nowMs()
	var total int64
	if st.RetentionDays > 0 {
		cutoff := now - (time.Duration(st.RetentionDays) * 24 * time.Hour).Milliseconds()
		if n, err := s.Q.DeleteFinishedRequestsOlderThan(ctx, cutoff); err == nil {
			total += n
		}
		if n, err := s.Q.DeleteEventsOlderThan(ctx, cutoff); err == nil {
			total += n
		}
		if n, err := s.Q.DeleteOrphanConversations(ctx); err == nil {
			total += n
		}
	}
	_, _ = s.Q.DeleteExpiredSessions(ctx, now)
	_, _ = s.Q.DeleteExpiredLoginTokens(ctx, now)
	_, _ = s.Q.DeleteExpiredOAuthCodes(ctx, now)
	_, _ = s.Q.DeleteExpiredOAuthTokens(ctx, sqlcgen.DeleteExpiredOAuthTokensParams{ExpiresAt: ptr(now), CreatedAt: now - st.OAuthRefreshTTL.Milliseconds()})
	if total > 0 {
		s.publish(events.Event{Kind: events.SystemPruned, Message: fmt.Sprintf("Pruned %d record(s) older than %d days", total, st.RetentionDays), Data: map[string]any{"count": total}})
	}
}

// Prune runs retention cleanup immediately (CLI).
func (s *Service) Prune(ctx context.Context) { s.prune(ctx) }

// AllowRequest applies the per-key rate limit.
func (s *Service) AllowRequest(k sqlcgen.ApiKey) (ok bool, remaining int64, retryAfter time.Duration, limit int64) {
	limit = k.RateLimitRpm
	if limit <= 0 {
		limit = s.Settings().DefaultRateLimit
	}
	ok, remaining, retryAfter = s.limiter.Allow(k.ID, limit)
	return ok, remaining, retryAfter, limit
}
