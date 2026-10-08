// Package api serves the OpenAI-compatible HTTP surface (/v1/...).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/openai"
)

const maxBodyBytes = 16 << 20

// Handler implements the /v1 endpoints.
type Handler struct {
	svc *core.Service
	log *slog.Logger
}

// New creates the API handler.
func New(svc *core.Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// Register mounts the routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("POST /v1/chat/completions", h.cors(h.auth(h.chatCompletions)))
	mux.Handle("POST /v1/completions", h.cors(h.auth(h.completions)))
	mux.Handle("GET /v1/models", h.cors(h.auth(h.models)))
	mux.Handle("GET /v1/models/{id}", h.cors(h.auth(h.model)))
	mux.Handle("GET /v1/requests/{id}", h.cors(h.auth(h.requestStatus)))
	mux.Handle("OPTIONS /v1/", h.cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	mux.Handle("/v1/", h.cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openai.WriteError(w, http.StatusNotFound, openai.ErrTypeInvalidRequest, "unknown_url",
			fmt.Sprintf("Unknown endpoint %s %s. Supported: POST /v1/chat/completions, POST /v1/completions, GET /v1/models.", r.Method, r.URL.Path))
	})))
}

type ctxKey int

const keyCtx ctxKey = iota

func keyFrom(ctx context.Context) sqlcgen.ApiKey {
	k, _ := ctx.Value(keyCtx).(sqlcgen.ApiKey)
	return k
}

func (h *Handler) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Api-Key, OpenAI-Organization, OpenAI-Project, X-Stainless-Arch, X-Stainless-Lang, X-Stainless-OS, X-Stainless-Package-Version, X-Stainless-Runtime, X-Stainless-Runtime-Version, X-Stainless-Retry-Count, X-Stainless-Timeout")
		w.Header().Set("Access-Control-Expose-Headers", "x-request-id, x-switchboard-request-id, x-switchboard-conversation-id, openai-processing-ms, x-ratelimit-limit-requests, x-ratelimit-remaining-requests, retry-after")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if ah := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(ah), "bearer ") {
			token = strings.TrimSpace(ah[7:])
		} else if xk := r.Header.Get("X-Api-Key"); xk != "" {
			token = strings.TrimSpace(xk)
		}
		if token == "" {
			openai.WriteError(w, http.StatusUnauthorized, openai.ErrTypeAuthentication, "missing_api_key",
				"Missing API key. Send it as 'Authorization: Bearer sk-sb-...'. Create one in the Switchboard dashboard or with 'switchboard keys create'.")
			return
		}
		k, err := h.svc.AuthenticateAPIKey(r.Context(), token)
		if err != nil {
			switch {
			case errors.Is(err, core.ErrKeyRevoked):
				openai.WriteError(w, http.StatusUnauthorized, openai.ErrTypeAuthentication, "api_key_revoked", "This API key has been revoked.")
			case errors.Is(err, core.ErrKeyInvalid):
				openai.WriteError(w, http.StatusUnauthorized, openai.ErrTypeAuthentication, "invalid_api_key", "Incorrect API key provided.")
			default:
				h.log.Error("authenticate api key", "err", err)
				openai.WriteError(w, http.StatusInternalServerError, openai.ErrTypeServer, "internal_error", "Could not validate API key.")
			}
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyCtx, k)))
	})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			openai.WriteError(w, http.StatusRequestEntityTooLarge, openai.ErrTypeInvalidRequest, "body_too_large", "Request body exceeds 16 MB.")
		} else {
			openai.WriteError(w, http.StatusBadRequest, openai.ErrTypeInvalidRequest, "invalid_body", "Could not read request body.")
		}
		return nil, false
	}
	return body, true
}

// chat completions

func (h *Handler) chatCompletions(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	req, err := openai.ParseChatRequest(body)
	if err != nil {
		openai.WriteError(w, http.StatusBadRequest, openai.ErrTypeInvalidRequest, "invalid_request", err.Error())
		return
	}
	h.serve(w, r, req, body, "chat.completions")
}

func (h *Handler) completions(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	legacy, err := openai.ParseCompletionRequest(body)
	if err != nil {
		openai.WriteError(w, http.StatusBadRequest, openai.ErrTypeInvalidRequest, "invalid_request", err.Error())
		return
	}
	h.serve(w, r, legacy.ToChat(), body, "completions")
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, req *openai.ChatRequest, body []byte, endpoint string) {
	key := keyFrom(r.Context())
	st := h.svc.Settings()
	model := strings.TrimSpace(req.Model)
	if model == "" && len(st.Models) > 0 {
		model = st.Models[0]
	}
	if !core.KeyAllowsModel(key, model) {
		openai.WriteError(w, http.StatusForbidden, openai.ErrTypePermission, "model_not_allowed",
			fmt.Sprintf("API key %q is not allowed to use model %q.", key.Name, model))
		return
	}
	if !st.AcceptAnyModel && !h.modelKnown(r.Context(), model) {
		openai.WriteError(w, http.StatusNotFound, openai.ErrTypeNotFound, "model_not_found",
			fmt.Sprintf("The model %q does not exist or no channel serves it. See GET /v1/models.", model))
		return
	}

	allowed, remaining, retryAfter, limit := h.svc.AllowRequest(key)
	if limit > 0 {
		w.Header().Set("x-ratelimit-limit-requests", strconv.FormatInt(limit, 10))
		w.Header().Set("x-ratelimit-remaining-requests", strconv.FormatInt(remaining, 10))
	}
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		openai.WriteError(w, http.StatusTooManyRequests, openai.ErrTypeRateLimit, "rate_limit_exceeded",
			fmt.Sprintf("Rate limit of %d requests/minute exceeded for API key %q. Retry in %s.", limit, key.Name, retryAfter.Round(time.Second)))
		return
	}

	rec, err := h.svc.SubmitRequest(r.Context(), core.SubmitInput{
		Key: key, Req: req, Endpoint: endpoint, Body: body, ClientIP: clientIP(r), UserAgent: r.UserAgent(),
	})
	if err != nil {
		if errors.Is(err, core.ErrQueueFull) {
			w.Header().Set("Retry-After", "5")
			openai.WriteError(w, http.StatusTooManyRequests, openai.ErrTypeRateLimit, "queue_full", "The request queue is full. Retry shortly.")
			return
		}
		h.log.Error("submit request", "err", err)
		openai.WriteError(w, http.StatusInternalServerError, openai.ErrTypeServer, "internal_error", "Could not queue the request.")
		return
	}
	w.Header().Set("x-request-id", rec.ID)
	w.Header().Set("x-switchboard-request-id", rec.ID)
	if rec.ConversationID != nil {
		w.Header().Set("x-switchboard-conversation-id", *rec.ConversationID)
	}

	if req.Stream {
		h.stream(w, r, rec, req, endpoint == "completions")
		return
	}
	h.wait(w, r, rec)
}

func (h *Handler) modelKnown(ctx context.Context, model string) bool {
	for _, m := range h.advertisedModels(ctx) {
		if strings.EqualFold(m, model) {
			return true
		}
	}
	chans, _ := h.svc.ListOpenChannels(ctx)
	for _, c := range chans {
		if core.ChannelServesModel(c, model) {
			return true
		}
	}
	return false
}

// wait serves a non-streaming request: block until the outcome, then write
// the stored OpenAI response verbatim.
func (h *Handler) wait(w http.ResponseWriter, r *http.Request, rec sqlcgen.Request) {
	outcome, final, err := h.svc.WaitRequest(r.Context(), rec.ID)
	if err != nil {
		// Caller went away; release the request so no agent works for nothing.
		_ = h.svc.CancelRequest(context.Background(), rec.ID, "caller disconnected")
		return
	}
	switch outcome.Status {
	case core.StatusCompleted:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("openai-processing-ms", strconv.FormatInt(latencyMs(final), 10))
		w.WriteHeader(http.StatusOK)
		if final.ResponseBody != nil {
			_, _ = io.WriteString(w, *final.ResponseBody)
		}
	default:
		status, typ, code, msg := outcomeError(outcome)
		openai.WriteError(w, status, typ, code, msg)
	}
}

func latencyMs(r sqlcgen.Request) int64 {
	if r.CompletedAt == nil {
		return 0
	}
	return *r.CompletedAt - r.CreatedAt
}

func outcomeError(o core.Outcome) (int, string, string, string) {
	switch o.Status {
	case core.StatusExpired:
		return http.StatusGatewayTimeout, openai.ErrTypeServer, "timeout", "No agent answered in time: " + o.Error
	case core.StatusCancelled:
		return http.StatusServiceUnavailable, openai.ErrTypeServer, "request_cancelled", "The request was cancelled: " + o.Error
	default:
		return http.StatusBadGateway, openai.ErrTypeServer, "agent_error", "The serving agent failed: " + o.Error
	}
}

// stream serves a streaming request as SSE chunks, replaying anything the
// agent emitted before the subscription and reconciling the final answer.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request, rec sqlcgen.Request, req *openai.ChatRequest, legacy bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		openai.WriteError(w, http.StatusInternalServerError, openai.ErrTypeServer, "no_streaming", "Streaming is not supported by this connection.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	created := core.MsTime(rec.CreatedAt)
	deltas, replay, done, unsub := h.svc.SubscribeRequest(rec.ID)
	defer unsub()

	var streamed strings.Builder
	streamedToolCalls := 0
	sentRole := false
	write := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	emit := func(d openai.Delta, finish *string, usage *openai.Usage) {
		if d.Content != nil {
			streamed.WriteString(*d.Content)
		}
		streamedToolCalls += len(d.ToolCalls)
		if legacy {
			text := ""
			if d.Content != nil {
				text = *d.Content
			}
			chunk := map[string]any{
				"id": "cmpl-" + strings.TrimPrefix(rec.ID, "req_"), "object": "text_completion", "created": created.Unix(), "model": rec.Model,
				"choices": []map[string]any{{"text": text, "index": 0, "logprobs": nil, "finish_reason": finish}},
			}
			if usage != nil {
				chunk["usage"] = usage
			}
			write(chunk)
			return
		}
		if !sentRole {
			d.Role = "assistant"
			sentRole = true
		}
		write(openai.BuildChunk(rec.ID, rec.Model, created, d, finish, usage))
	}

	for _, d := range replay {
		emit(d, nil, nil)
	}
	keepalive := h.svc.Settings().StreamKeepalive
	if keepalive <= 0 {
		keepalive = 15 * time.Second
	}
	ka := time.NewTicker(keepalive)
	defer ka.Stop()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()

loop:
	for {
		select {
		case d := <-deltas:
			emit(d, nil, nil)
		case <-done:
			break loop
		case <-ka.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		case <-poll.C:
			if cur, err := h.svc.GetRequest(r.Context(), rec.ID); err == nil && core.IsTerminal(cur.Status) {
				break loop
			}
		case <-r.Context().Done():
			_ = h.svc.CancelRequest(context.Background(), rec.ID, "caller disconnected")
			return
		}
	}
	// Drain deltas that raced with completion.
	for {
		select {
		case d := <-deltas:
			emit(d, nil, nil)
			continue
		default:
		}
		break
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	outcome, final, err := h.svc.WaitRequest(ctx, rec.ID)
	if err != nil {
		write(openai.ErrorResponse{Error: openai.ErrorBody{Message: "lost track of the request", Type: openai.ErrTypeServer}})
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	switch outcome.Status {
	case core.StatusCompleted:
		var a openai.Answer
		if outcome.Answer != nil {
			a = *outcome.Answer
		} else if parsed, ok := core.ParseAnswer(final); ok {
			a = parsed
		}
		got := streamed.String()
		if a.Content != "" {
			switch {
			case got == "":
				c := a.Content
				emit(openai.Delta{Content: &c}, nil, nil)
			case strings.HasPrefix(a.Content, got) && len(a.Content) > len(got):
				rest := a.Content[len(got):]
				emit(openai.Delta{Content: &rest}, nil, nil)
			}
		}
		if len(a.ToolCalls) > streamedToolCalls {
			emit(openai.Delta{ToolCalls: openai.ToolCallsAsDeltas(a.ToolCalls[streamedToolCalls:], streamedToolCalls)}, nil, nil)
		}
		if !sentRole && !legacy {
			empty := ""
			emit(openai.Delta{Content: &empty}, nil, nil)
		}
		finish := a.FinishReason
		if finish == "" {
			finish = "stop"
		}
		emit(openai.Delta{}, &finish, nil)
		if req.StreamOptions != nil && req.StreamOptions.IncludeUsage && !legacy {
			write(openai.UsageOnlyChunk(rec.ID, rec.Model, created, outcome.Usage))
		}
	default:
		status, typ, code, msg := outcomeError(outcome)
		codeStr := code
		write(map[string]any{"error": openai.ErrorBody{Message: msg, Type: typ, Code: &codeStr}, "status": status})
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// models

func (h *Handler) advertisedModels(ctx context.Context) []string {
	set := map[string]bool{}
	for _, m := range h.svc.Settings().Models {
		set[m] = true
	}
	chans, _ := h.svc.ListOpenChannels(ctx)
	for _, c := range chans {
		for _, m := range core.ChannelModels(c) {
			if m == "*" || strings.ContainsAny(m, "*?[") {
				continue
			}
			set[m] = true
		}
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func (h *Handler) models(w http.ResponseWriter, r *http.Request) {
	created := h.svc.StartedAt.Unix()
	list := openai.ModelList{Object: "list", Data: []openai.Model{}}
	key := keyFrom(r.Context())
	for _, m := range h.advertisedModels(r.Context()) {
		if !core.KeyAllowsModel(key, m) {
			continue
		}
		list.Data = append(list.Data, openai.Model{ID: m, Object: "model", Created: created, OwnedBy: "switchboard"})
	}
	openai.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) model(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, m := range h.advertisedModels(r.Context()) {
		if strings.EqualFold(m, id) {
			openai.WriteJSON(w, http.StatusOK, openai.Model{ID: m, Object: "model", Created: h.svc.StartedAt.Unix(), OwnedBy: "switchboard"})
			return
		}
	}
	if h.svc.Settings().AcceptAnyModel {
		openai.WriteJSON(w, http.StatusOK, openai.Model{ID: id, Object: "model", Created: h.svc.StartedAt.Unix(), OwnedBy: "switchboard"})
		return
	}
	openai.WriteError(w, http.StatusNotFound, openai.ErrTypeNotFound, "model_not_found", fmt.Sprintf("The model %q does not exist.", id))
}

// requestStatus is a Switchboard extension: callers can poll a request they
// submitted (useful for async integrations and debugging).
func (h *Handler) requestStatus(w http.ResponseWriter, r *http.Request) {
	key := keyFrom(r.Context())
	rec, err := h.svc.GetRequest(r.Context(), r.PathValue("id"))
	if err != nil || rec.ApiKeyID != key.ID {
		openai.WriteError(w, http.StatusNotFound, openai.ErrTypeNotFound, "request_not_found", "No such request for this API key.")
		return
	}
	out := map[string]any{
		"id": rec.ID, "object": "switchboard.request", "status": rec.Status, "model": rec.Model, "stream": rec.Stream,
		"created": rec.CreatedAt / 1000, "conversation_id": rec.ConversationID, "turn": rec.TurnIndex, "attempts": rec.Attempts,
		"channel_id": rec.ChannelID, "claimed_at": rec.ClaimedAt, "first_token_at": rec.FirstTokenAt, "completed_at": rec.CompletedAt,
		"error": rec.ErrorMessage,
	}
	if rec.Status == core.StatusCompleted && rec.ResponseBody != nil {
		out["response"] = json.RawMessage(*rec.ResponseBody)
	}
	openai.WriteJSON(w, http.StatusOK, out)
}
