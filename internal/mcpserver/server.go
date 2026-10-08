// Package mcpserver exposes the request outbox to agents over MCP
// (Streamable HTTP) with bearer-token authentication.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/ids"
	"github.com/ucgeorge/switchboard/internal/openai"
	"github.com/ucgeorge/switchboard/internal/version"
)

const (
	defaultWaitSeconds = 25
	maxWaitSeconds     = 55
)

// Server is the MCP endpoint.
type Server struct {
	svc         *core.Service
	log         *slog.Logger
	mcp         *mcp.Server
	protected   http.Handler
	unprotected http.Handler
}

// New builds the MCP server and registers tools, prompts and resources.
func New(svc *core.Service, log *slog.Logger) *Server {
	s := &Server{svc: svc, log: log}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "switchboard", Title: "Switchboard", Version: version.Version}, &mcp.ServerOptions{
		Instructions: "Switchboard queues OpenAI-compatible chat requests for agents to answer. " +
			"Call open_channel once, then loop: claim_request (wait_seconds=25) -> answer with stream_delta/complete_request -> repeat. " +
			"Read resource switchboard://guide or the `serve` prompt for the full protocol.",
	})
	s.registerTools()
	s.registerPrompts()
	s.registerResources()

	getServer := func(*http.Request) *mcp.Server { return s.mcp }
	s.protected = mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true, PropagateRequestCancellation: true, Logger: log})
	s.unprotected = mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true, PropagateRequestCancellation: true, Logger: log, DisableLocalhostProtection: true})
	return s
}

// ServeHTTP authenticates the bearer token and dispatches to the transport.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	base := s.svc.BaseURL(r.Host, r.TLS != nil)
	mw := auth.RequireBearerToken(s.verify, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL:    base + "/.well-known/oauth-protected-resource",
		AllowMissingExpiration: true,
	})
	handler := s.protected
	if s.svc.Settings().PublicURL != "" {
		// Behind a tunnel the Host header is the public name while the
		// connection arrives on localhost; the SDK's rebinding guard would
		// reject it.
		handler = s.unprotected
	}
	mw(handler).ServeHTTP(w, r)
}

func (s *Server) verify(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	t, err := s.svc.AuthenticateAgentToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}
	info := &auth.TokenInfo{
		Scopes: core.SplitList(t.Scope),
		UserID: t.ID,
		Extra:  map[string]any{"token_id": t.ID, "token_name": t.Name, "kind": t.Kind},
	}
	if t.ExpiresAt != nil {
		info.Expiration = time.UnixMilli(*t.ExpiresAt)
	}
	return info, nil
}

func tokenID(req *mcp.CallToolRequest) (string, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return "", errors.New("unauthenticated")
	}
	id, _ := req.Extra.TokenInfo.Extra["token_id"].(string)
	if id == "" {
		return "", errors.New("unauthenticated")
	}
	return id, nil
}

// toolErr renders a domain error as a tool result the model can act on
// (isError=true) instead of a protocol failure.
func toolErr(err error) (*mcp.CallToolResult, error) {
	msg := err.Error()
	var state *core.RequestStateError
	switch {
	case errors.As(err, &state):
		msg = fmt.Sprintf("%s. Drop it and claim the next request.", state.Error())
	case errors.Is(err, core.ErrChannelNotFound):
		msg = "channel not found: call open_channel first and use the returned channel_id"
	case errors.Is(err, core.ErrChannelClosed):
		msg = "channel is closed: call open_channel again"
	case errors.Is(err, core.ErrChannelNotOwned):
		msg = "channel belongs to a different agent token: open your own channel"
	case errors.Is(err, core.ErrRequestNotFound):
		msg = "request not found"
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil
}

// tool payloads

type openChannelIn struct {
	Name        string   `json:"name" jsonschema:"Stable name for this agent instance, e.g. 'claude-desktop' or 'worker-2'"`
	Models      []string `json:"models,omitempty" jsonschema:"Model names this agent serves. Default ['*'] serves any model. Globs such as 'gpt-4*' are allowed."`
	Concurrency int      `json:"concurrency,omitempty" jsonschema:"How many requests this agent works on at once (default 1)"`
	Description string   `json:"description,omitempty" jsonschema:"Optional description shown in the dashboard"`
}

type channelOut struct {
	ChannelID    string   `json:"channel_id"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	Models       []string `json:"models"`
	Concurrency  int64    `json:"concurrency"`
	InFlight     int64    `json:"in_flight"`
	QueueDepth   int64    `json:"queue_depth"`
	Reconnected  bool     `json:"reconnected,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
}

type channelRef struct {
	ChannelID string `json:"channel_id" jsonschema:"Channel id returned by open_channel"`
}

type closeChannelIn struct {
	ChannelID string `json:"channel_id" jsonschema:"Channel id returned by open_channel"`
	Reason    string `json:"reason,omitempty" jsonschema:"Why the channel is closing"`
}

type okOut struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type claimIn struct {
	ChannelID   string `json:"channel_id" jsonschema:"Channel id returned by open_channel"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"How long to wait for a request to arrive before returning found=false (default 25, max 55)"`
}

type callerInfo struct {
	APIKeyName string `json:"api_key_name"`
	UserAgent  string `json:"user_agent,omitempty"`
}

type requestPayload struct {
	RequestID      string         `json:"request_id"`
	ConversationID string         `json:"conversation_id,omitempty"`
	TurnIndex      int64          `json:"turn_index"`
	Model          string         `json:"model"`
	Endpoint       string         `json:"endpoint"`
	Stream         bool           `json:"stream"`
	Attempt        int64          `json:"attempt"`
	Deadline       string         `json:"deadline"`
	LeaseExpiresAt string         `json:"lease_expires_at"`
	Messages       []agentMessage `json:"messages"`
	Params         map[string]any `json:"params,omitempty"`
	Caller         callerInfo     `json:"caller"`
	Instructions   string         `json:"instructions"`
}

type claimOut struct {
	Found      bool            `json:"found"`
	Request    *requestPayload `json:"request,omitempty"`
	QueueDepth int64           `json:"queue_depth"`
	Message    string          `json:"message,omitempty"`
}

type deltaIn struct {
	ChannelID string       `json:"channel_id" jsonschema:"Channel id returned by open_channel"`
	RequestID string       `json:"request_id" jsonschema:"Request id from claim_request"`
	Content   string       `json:"content,omitempty" jsonschema:"Next piece of assistant text to stream to the caller"`
	ToolCalls []toolCallIn `json:"tool_calls,omitempty" jsonschema:"Complete tool calls to stream (each is sent as one fragment)"`
}

type deltaOut struct {
	OK            bool `json:"ok"`
	StreamedChars int  `json:"streamed_chars"`
}

type completeIn struct {
	ChannelID    string       `json:"channel_id" jsonschema:"Channel id returned by open_channel"`
	RequestID    string       `json:"request_id" jsonschema:"Request id from claim_request"`
	Content      string       `json:"content,omitempty" jsonschema:"Final assistant message text. May be empty if everything was sent with stream_delta."`
	ToolCalls    []toolCallIn `json:"tool_calls,omitempty" jsonschema:"Tool calls the assistant makes (OpenAI format: id, type='function', function{name, arguments JSON string})"`
	FinishReason string       `json:"finish_reason,omitempty" jsonschema:"stop | tool_calls | length | content_filter (default inferred)"`
	Refusal      string       `json:"refusal,omitempty" jsonschema:"Set instead of content when refusing"`
	Usage        *usageIn     `json:"usage,omitempty" jsonschema:"Optional real token counts {prompt_tokens, completion_tokens}; otherwise estimated"`
}

type completeOut struct {
	OK             bool   `json:"ok"`
	Status         string `json:"status"`
	LatencyMs      int64  `json:"latency_ms"`
	ConversationID string `json:"conversation_id,omitempty"`
	Message        string `json:"message,omitempty"`
}

type failIn struct {
	ChannelID string `json:"channel_id" jsonschema:"Channel id returned by open_channel"`
	RequestID string `json:"request_id" jsonschema:"Request id from claim_request"`
	Reason    string `json:"reason" jsonschema:"Why the request could not be answered"`
	Retryable bool   `json:"retryable,omitempty" jsonschema:"true returns it to the queue for another agent; false fails it for the caller"`
}

type releaseIn struct {
	ChannelID string `json:"channel_id" jsonschema:"Channel id returned by open_channel"`
	RequestID string `json:"request_id" jsonschema:"Request id from claim_request"`
	Reason    string `json:"reason,omitempty" jsonschema:"Why you are handing it back"`
}

type requestRef struct {
	RequestID string `json:"request_id" jsonschema:"Request id"`
}

type requestStateOut struct {
	RequestID      string `json:"request_id"`
	Status         string `json:"status"`
	Model          string `json:"model"`
	ChannelID      string `json:"channel_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Attempt        int64  `json:"attempt"`
	Deadline       string `json:"deadline"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	Error          string `json:"error,omitempty"`
	AgeMs          int64  `json:"age_ms"`
}

type emptyIn struct{}

type queueStatusOut struct {
	Queued         int64            `json:"queued"`
	InFlight       int64            `json:"in_flight"`
	OnlineChannels int64            `json:"online_channels"`
	OldestWaitMs   int64            `json:"oldest_wait_ms"`
	ModelsWaiting  map[string]int64 `json:"models_waiting"`
}

type channelsOut struct {
	Channels []channelOut `json:"channels"`
}

// tool registration

func (s *Server) registerTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "open_channel", Description: "Register this agent instance as a channel that will claim and answer requests. Call once per session; returns the channel_id used by every other tool."}, s.openChannel)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "close_channel", Description: "Take this channel out of service. Any request it still holds is returned to the queue."}, s.closeChannel)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "heartbeat", Description: "Keep the channel online while idle and report queue depth. claim_request already counts as a heartbeat."}, s.heartbeat)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "claim_request", Description: "Wait for and claim the next queued request routed to this channel. Returns found=false after wait_seconds if nothing arrived; call again. Loop on this tool to serve requests."}, s.claimRequest)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "stream_delta", Description: "Send partial assistant output for a claimed request so the caller sees it immediately. Also renews the lease. Finish with complete_request."}, s.streamDelta)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "complete_request", Description: "Deliver the final answer for a claimed request. Content may be empty if all text was already sent via stream_delta."}, s.completeRequest)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "fail_request", Description: "Report that this request cannot be answered. retryable=true hands it to another agent; retryable=false returns an error to the caller."}, s.failRequest)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "release_request", Description: "Hand a claimed request back to the queue without answering (e.g. you are shutting down)."}, s.releaseRequest)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "extend_lease", Description: "Renew the lease on a claimed request when the answer needs more time and you are not yet streaming."}, s.extendLease)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "get_request", Description: "Inspect the current state of a request (e.g. to check whether the caller cancelled it)."}, s.getRequest)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "queue_status", Description: "Show how many requests are waiting, which models they ask for, and how many channels are online."}, s.queueStatus)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "list_channels", Description: "List channels (agent instances) and their status."}, s.listChannels)
}

func (s *Server) channelOut(ctx context.Context, ch sqlcgen.Channel, reconnected bool) channelOut {
	inflight, _ := s.svc.ChannelInFlight(ctx, ch.ID)
	queued, _, _, _ := s.svc.QuickCounts(ctx)
	return channelOut{
		ChannelID: ch.ID, Name: ch.Name, Status: ch.Status, Models: core.ChannelModels(ch), Concurrency: ch.Concurrency,
		InFlight: inflight, QueueDepth: queued, Reconnected: reconnected,
	}
}

func (s *Server) openChannel(ctx context.Context, req *mcp.CallToolRequest, in openChannelIn) (*mcp.CallToolResult, channelOut, error) {
	tid, err := tokenID(req)
	if err != nil {
		return nil, channelOut{}, err
	}
	ch, created, err := s.svc.OpenChannel(ctx, tid, core.ChannelInput{Name: in.Name, Description: in.Description, Models: in.Models, Concurrency: int64(in.Concurrency)})
	if err != nil {
		r, _ := toolErr(err)
		return r, channelOut{}, nil
	}
	out := s.channelOut(ctx, ch, !created)
	out.Instructions = fmt.Sprintf("Channel ready. Now loop: claim_request(channel_id=%q, wait_seconds=%d) -> answer -> complete_request. %d request(s) are waiting.", ch.ID, defaultWaitSeconds, out.QueueDepth)
	return nil, out, nil
}

func (s *Server) closeChannel(ctx context.Context, req *mcp.CallToolRequest, in closeChannelIn) (*mcp.CallToolResult, okOut, error) {
	tid, err := tokenID(req)
	if err != nil {
		return nil, okOut{}, err
	}
	if err := s.svc.CloseChannel(ctx, in.ChannelID, tid, in.Reason); err != nil {
		r, _ := toolErr(err)
		return r, okOut{}, nil
	}
	return nil, okOut{OK: true, Message: "channel closed"}, nil
}

func (s *Server) heartbeat(ctx context.Context, req *mcp.CallToolRequest, in channelRef) (*mcp.CallToolResult, channelOut, error) {
	tid, err := tokenID(req)
	if err != nil {
		return nil, channelOut{}, err
	}
	ch, err := s.svc.Heartbeat(ctx, in.ChannelID, tid)
	if err != nil {
		r, _ := toolErr(err)
		return r, channelOut{}, nil
	}
	return nil, s.channelOut(ctx, ch, false), nil
}

func (s *Server) claimRequest(ctx context.Context, req *mcp.CallToolRequest, in claimIn) (*mcp.CallToolResult, claimOut, error) {
	tid, err := tokenID(req)
	if err != nil {
		return nil, claimOut{}, err
	}
	wait := in.WaitSeconds
	if wait <= 0 {
		wait = defaultWaitSeconds
	}
	if wait > maxWaitSeconds {
		wait = maxWaitSeconds
	}
	r, err := s.svc.ClaimRequest(ctx, in.ChannelID, tid, time.Duration(wait)*time.Second)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, claimOut{Found: false, Message: "wait interrupted; call claim_request again"}, nil
		}
		res, _ := toolErr(err)
		return res, claimOut{}, nil
	}
	queued, _, _, _ := s.svc.QuickCounts(ctx)
	if r == nil {
		return nil, claimOut{Found: false, QueueDepth: queued, Message: fmt.Sprintf("No request arrived in %ds. Call claim_request again to keep serving.", wait)}, nil
	}
	payload, err := s.payload(ctx, *r)
	if err != nil {
		_ = s.svc.FailRequest(ctx, in.ChannelID, tid, r.ID, "request body unreadable: "+err.Error(), false)
		res, _ := toolErr(err)
		return res, claimOut{}, nil
	}
	return nil, claimOut{Found: true, Request: payload, QueueDepth: queued}, nil
}

func (s *Server) payload(ctx context.Context, r sqlcgen.Request) (*requestPayload, error) {
	chat, err := core.ParseRequestBody(r)
	if err != nil {
		return nil, err
	}
	key, _ := s.svc.GetAPIKey(ctx, r.ApiKeyID)
	p := &requestPayload{
		RequestID: r.ID, ConversationID: deref(r.ConversationID), TurnIndex: r.TurnIndex, Model: r.Model, Endpoint: r.Endpoint,
		Stream: r.Stream, Attempt: r.Attempts,
		Deadline: time.UnixMilli(r.TimeoutAt).UTC().Format(time.RFC3339),
		Messages: toAgentMessages(chat.Messages), Params: toAnyMap(chat.Params()),
		Caller: callerInfo{APIKeyName: key.Name, UserAgent: r.UserAgent},
	}
	if r.LeaseExpiresAt != nil {
		p.LeaseExpiresAt = time.UnixMilli(*r.LeaseExpiresAt).UTC().Format(time.RFC3339)
	}
	remaining := time.Until(time.UnixMilli(r.TimeoutAt)).Round(time.Second)
	mode := "then call complete_request with the full answer"
	if r.Stream {
		mode = "stream text with stream_delta as you produce it, then call complete_request"
	}
	p.Instructions = fmt.Sprintf("Reply as model %q to the last message; %s. You have %s before the caller times out. Honour params (tools, response_format, max_tokens) exactly.", r.Model, mode, remaining)
	return p, nil
}

func (s *Server) streamDelta(ctx context.Context, req *mcp.CallToolRequest, in deltaIn) (*mcp.CallToolResult, deltaOut, error) {
	tid, err := tokenID(req)
	if err != nil {
		return nil, deltaOut{}, err
	}
	if in.Content == "" && len(in.ToolCalls) == 0 {
		return nil, deltaOut{OK: true}, nil
	}
	d := openai.Delta{}
	if in.Content != "" {
		c := in.Content
		d.Content = &c
	}
	if len(in.ToolCalls) > 0 {
		calls := openai.Answer{ToolCalls: toToolCalls(in.ToolCalls)}.Normalized(func() string { return ids.Random(16) }).ToolCalls
		d.ToolCalls = openai.ToolCallsAsDeltas(calls, s.streamedToolCalls(in.RequestID))
	}
	if err := s.svc.StreamDelta(ctx, in.ChannelID, tid, in.RequestID, d); err != nil {
		r, _ := toolErr(err)
		return r, deltaOut{}, nil
	}
	return nil, deltaOut{OK: true, StreamedChars: len(in.Content)}, nil
}

// streamedToolCalls returns how many tool-call fragments were already sent,
// so streamed tool calls carry consecutive indexes.
func (s *Server) streamedToolCalls(requestID string) int {
	_, replay, _, unsub := s.svc.SubscribeRequest(requestID)
	unsub()
	n := 0
	for _, d := range replay {
		n += len(d.ToolCalls)
	}
	return n
}

func (s *Server) completeRequest(ctx context.Context, req *mcp.CallToolRequest, in completeIn) (*mcp.CallToolResult, completeOut, error) {
	tid, err := tokenID(req)
	if err != nil {
		return nil, completeOut{}, err
	}
	a := openai.Answer{Content: in.Content, ToolCalls: toToolCalls(in.ToolCalls), FinishReason: in.FinishReason, Refusal: in.Refusal, Usage: in.Usage.toUsage()}
	r, err := s.svc.CompleteRequest(ctx, in.ChannelID, tid, in.RequestID, a)
	if err != nil {
		res, _ := toolErr(err)
		return res, completeOut{}, nil
	}
	var latency int64
	if r.CompletedAt != nil {
		latency = *r.CompletedAt - r.CreatedAt
	}
	return nil, completeOut{OK: true, Status: r.Status, LatencyMs: latency, ConversationID: deref(r.ConversationID), Message: "Delivered. Call claim_request for the next one."}, nil
}

func (s *Server) failRequest(ctx context.Context, req *mcp.CallToolRequest, in failIn) (*mcp.CallToolResult, okOut, error) {
	tid, err := tokenID(req)
	if err != nil {
		return nil, okOut{}, err
	}
	if err := s.svc.FailRequest(ctx, in.ChannelID, tid, in.RequestID, in.Reason, in.Retryable); err != nil {
		r, _ := toolErr(err)
		return r, okOut{}, nil
	}
	msg := "request failed for the caller"
	if in.Retryable {
		msg = "request returned to the queue (if attempts remain)"
	}
	return nil, okOut{OK: true, Message: msg}, nil
}

func (s *Server) releaseRequest(ctx context.Context, req *mcp.CallToolRequest, in releaseIn) (*mcp.CallToolResult, okOut, error) {
	tid, err := tokenID(req)
	if err != nil {
		return nil, okOut{}, err
	}
	if err := s.svc.ReleaseRequest(ctx, in.ChannelID, tid, in.RequestID, in.Reason); err != nil {
		r, _ := toolErr(err)
		return r, okOut{}, nil
	}
	return nil, okOut{OK: true, Message: "request released"}, nil
}

func (s *Server) extendLease(ctx context.Context, req *mcp.CallToolRequest, in releaseIn) (*mcp.CallToolResult, okOut, error) {
	tid, err := tokenID(req)
	if err != nil {
		return nil, okOut{}, err
	}
	if err := s.svc.ExtendLease(ctx, in.ChannelID, tid, in.RequestID); err != nil {
		r, _ := toolErr(err)
		return r, okOut{}, nil
	}
	return nil, okOut{OK: true, Message: fmt.Sprintf("lease extended by %s", s.svc.Settings().Lease)}, nil
}

func (s *Server) getRequest(ctx context.Context, req *mcp.CallToolRequest, in requestRef) (*mcp.CallToolResult, requestStateOut, error) {
	if _, err := tokenID(req); err != nil {
		return nil, requestStateOut{}, err
	}
	r, err := s.svc.GetRequest(ctx, in.RequestID)
	if err != nil {
		res, _ := toolErr(err)
		return res, requestStateOut{}, nil
	}
	out := requestStateOut{
		RequestID: r.ID, Status: r.Status, Model: r.Model, ChannelID: deref(r.ChannelID), ConversationID: deref(r.ConversationID),
		Attempt: r.Attempts, Deadline: time.UnixMilli(r.TimeoutAt).UTC().Format(time.RFC3339), Error: deref(r.ErrorMessage),
		AgeMs: time.Now().UnixMilli() - r.CreatedAt,
	}
	if r.LeaseExpiresAt != nil {
		out.LeaseExpiresAt = time.UnixMilli(*r.LeaseExpiresAt).UTC().Format(time.RFC3339)
	}
	return nil, out, nil
}

func (s *Server) queueStatusOut(ctx context.Context) (queueStatusOut, error) {
	queued, inflight, online, err := s.svc.QuickCounts(ctx)
	if err != nil {
		return queueStatusOut{}, err
	}
	out := queueStatusOut{Queued: queued, InFlight: inflight, OnlineChannels: online, ModelsWaiting: map[string]int64{}}
	active, err := s.svc.ActiveRequests(ctx)
	if err != nil {
		return out, err
	}
	now := time.Now().UnixMilli()
	for _, r := range active {
		if r.Status != core.StatusQueued {
			continue
		}
		out.ModelsWaiting[r.Model]++
		if age := now - r.CreatedAt; age > out.OldestWaitMs {
			out.OldestWaitMs = age
		}
	}
	return out, nil
}

func (s *Server) queueStatus(ctx context.Context, req *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, queueStatusOut, error) {
	if _, err := tokenID(req); err != nil {
		return nil, queueStatusOut{}, err
	}
	out, err := s.queueStatusOut(ctx)
	if err != nil {
		res, _ := toolErr(err)
		return res, queueStatusOut{}, nil
	}
	return nil, out, nil
}

func (s *Server) listChannels(ctx context.Context, req *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, channelsOut, error) {
	if _, err := tokenID(req); err != nil {
		return nil, channelsOut{}, err
	}
	chans, err := s.svc.ListOpenChannels(ctx)
	if err != nil {
		res, _ := toolErr(err)
		return res, channelsOut{}, nil
	}
	out := channelsOut{Channels: []channelOut{}}
	for _, ch := range chans {
		out.Channels = append(out.Channels, s.channelOut(ctx, ch, false))
	}
	return nil, out, nil
}

// prompts & resources

func (s *Server) registerPrompts() {
	s.mcp.AddPrompt(&mcp.Prompt{
		Name:        "serve",
		Title:       "Serve Switchboard requests",
		Description: "Instructions for running the claim/answer loop as a serving agent.",
		Arguments: []*mcp.PromptArgument{
			{Name: "channel_name", Description: "Name for this agent instance (default: a generic name)"},
			{Name: "models", Description: "Comma-separated models to serve (default: any)"},
		},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		name := "agent"
		models := "*"
		if req.Params != nil {
			if v := req.Params.Arguments["channel_name"]; v != "" {
				name = v
			}
			if v := req.Params.Arguments["models"]; v != "" {
				models = v
			}
		}
		text := fmt.Sprintf("You are now a serving agent for Switchboard.\n\nStart by calling open_channel with name=%q and models=[%s]. Then run the loop described below until told to stop.\n\n%s", name, quoteList(models), AgentGuide)
		return &mcp.GetPromptResult{
			Description: "Serve queued OpenAI-compatible requests",
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	})
}

func quoteList(csv string) string {
	items := core.SplitList(csv)
	if len(items) == 0 {
		return `"*"`
	}
	q := make([]string, len(items))
	for i, it := range items {
		q[i] = fmt.Sprintf("%q", it)
	}
	return strings.Join(q, ", ")
}

func (s *Server) registerResources() {
	s.mcp.AddResource(&mcp.Resource{URI: "switchboard://guide", Name: "Serving guide", MIMEType: "text/markdown", Description: "How to claim and answer requests."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/markdown", Text: AgentGuide}}}, nil
		})
	s.mcp.AddResource(&mcp.Resource{URI: "switchboard://status", Name: "Queue status", MIMEType: "application/json", Description: "Queued/in-flight counts and models waiting."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			out, err := s.queueStatusOut(ctx)
			if err != nil {
				return nil, err
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(b)}}}, nil
		})
	s.mcp.AddResource(&mcp.Resource{URI: "switchboard://channels", Name: "Channels", MIMEType: "application/json", Description: "Connected agent instances."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			chans, err := s.svc.ListOpenChannels(ctx)
			if err != nil {
				return nil, err
			}
			out := channelsOut{Channels: []channelOut{}}
			for _, ch := range chans {
				out.Channels = append(out.Channels, s.channelOut(ctx, ch, false))
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(b)}}}, nil
		})
}

// ---- agent-facing wire types ------------------------------------------------
//
// The SDK derives JSON schemas from these Go types and validates every tool
// input and output against them, so they must describe the JSON agents
// actually exchange rather than our internal representation. Message content
// and generation params are free-form JSON (string, parts array, numbers,
// objects), hence `any`. Inputs are lenient about what agents commonly omit.

// agentMessage is a chat message as handed to an agent.
type agentMessage struct {
	Role       string            `json:"role"`
	Content    any               `json:"content"`
	Name       string            `json:"name,omitempty"`
	ToolCalls  []openai.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

func toAgentMessages(msgs []openai.Message) []agentMessage {
	out := make([]agentMessage, 0, len(msgs))
	for _, m := range msgs {
		am := agentMessage{Role: m.Role, Name: m.Name, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID}
		if len(m.Content) > 0 {
			if err := json.Unmarshal(m.Content, &am.Content); err != nil {
				am.Content = string(m.Content)
			}
		}
		out = append(out, am)
	}
	return out
}

func toAnyMap(raw map[string]json.RawMessage) map[string]any {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		var val any
		if err := json.Unmarshal(v, &val); err != nil {
			val = string(v)
		}
		out[k] = val
	}
	return out
}

// toolCallIn is a tool call as submitted by an agent. id and type are
// optional, and arguments may be a JSON string (OpenAI's format) or a JSON
// object, which is what models tend to produce.
type toolCallIn struct {
	ID       string     `json:"id,omitempty" jsonschema:"Optional call id; generated when omitted"`
	Type     string     `json:"type,omitempty" jsonschema:"Always 'function'; may be omitted"`
	Function functionIn `json:"function" jsonschema:"The function to call"`
}

type functionIn struct {
	Name      string `json:"name" jsonschema:"Function name; must be one defined in the request's params.tools"`
	Arguments any    `json:"arguments,omitempty" jsonschema:"Arguments as a JSON object, or as a JSON-encoded string"`
}

func toToolCalls(in []toolCallIn) []openai.ToolCall {
	if len(in) == 0 {
		return nil
	}
	out := make([]openai.ToolCall, 0, len(in))
	for _, tc := range in {
		args := "{}"
		switch v := tc.Function.Arguments.(type) {
		case nil:
		case string:
			if strings.TrimSpace(v) != "" {
				args = v
			}
		default:
			if b, err := json.Marshal(v); err == nil {
				args = string(b)
			}
		}
		out = append(out, openai.ToolCall{ID: tc.ID, Type: tc.Type, Function: openai.FunctionCall{Name: tc.Function.Name, Arguments: args}})
	}
	return out
}

// usageIn is optional real token accounting reported by an agent.
type usageIn struct {
	PromptTokens     int `json:"prompt_tokens,omitempty" jsonschema:"Prompt tokens consumed"`
	CompletionTokens int `json:"completion_tokens,omitempty" jsonschema:"Completion tokens produced"`
}

func (u *usageIn) toUsage() *openai.Usage {
	if u == nil {
		return nil
	}
	return &openai.Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.PromptTokens + u.CompletionTokens}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
