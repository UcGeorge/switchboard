package core

import (
	"context"
	"encoding/json"

	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/openai"
)

// RequestFilter narrows ListRequests. Empty fields are ignored.
type RequestFilter struct {
	Status         string
	APIKeyID       string
	ChannelID      string
	ConversationID string
	Model          string
	Search         string
	Limit          int64
	Offset         int64
}

// ListRequests pages requests newest first with the total matching count.
func (s *Service) ListRequests(ctx context.Context, f RequestFilter) ([]sqlcgen.Request, int64, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	search := likePattern(f.Search)
	rows, err := s.Q.ListRequests(ctx, sqlcgen.ListRequestsParams{
		Status: f.Status, ApiKeyID: f.APIKeyID, ChannelID: strp(f.ChannelID), ConversationID: strp(f.ConversationID),
		Model: f.Model, Search: search, Lim: f.Limit, Off: f.Offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.Q.CountRequests(ctx, sqlcgen.CountRequestsParams{
		Status: f.Status, ApiKeyID: f.APIKeyID, ChannelID: strp(f.ChannelID), ConversationID: strp(f.ConversationID),
		Model: f.Model, Search: search,
	})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// GetRequest fetches one request.
func (s *Service) GetRequest(ctx context.Context, id string) (sqlcgen.Request, error) {
	r, err := s.Q.GetRequest(ctx, id)
	if isNoRows(err) {
		return r, ErrRequestNotFound
	}
	return r, err
}

// RequestEvents returns a request's timeline oldest first.
func (s *Service) RequestEvents(ctx context.Context, id string) ([]sqlcgen.Event, error) {
	return s.Q.ListRequestEvents(ctx, strp(id))
}

// RecentRequests returns the newest n requests.
func (s *Service) RecentRequests(ctx context.Context, n int64) ([]sqlcgen.Request, error) {
	if n <= 0 {
		n = 10
	}
	return s.Q.ListRecentRequests(ctx, n)
}

// ActiveRequests returns everything queued or in flight, oldest first.
func (s *Service) ActiveRequests(ctx context.Context) ([]sqlcgen.Request, error) {
	return s.Q.ListActiveRequests(ctx)
}

// DistinctModels lists every model name seen in requests.
func (s *Service) DistinctModels(ctx context.Context) ([]string, error) {
	return s.Q.DistinctModels(ctx)
}

// EventFilter narrows ListEvents.
type EventFilter struct {
	Kind      string
	Level     string
	RequestID string
	ChannelID string
	APIKeyID  string
	Search    string
	Limit     int64
	Offset    int64
}

// ListEvents pages events newest first with the total matching count.
func (s *Service) ListEvents(ctx context.Context, f EventFilter) ([]sqlcgen.Event, int64, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
	search := likePattern(f.Search)
	rows, err := s.Q.ListEvents(ctx, sqlcgen.ListEventsParams{
		Kind: f.Kind, Level: f.Level, RequestID: strp(f.RequestID), ChannelID: strp(f.ChannelID), ApiKeyID: strp(f.APIKeyID),
		Search: search, Lim: f.Limit, Off: f.Offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.Q.CountEvents(ctx, sqlcgen.CountEventsParams{
		Kind: f.Kind, Level: f.Level, RequestID: strp(f.RequestID), ChannelID: strp(f.ChannelID), ApiKeyID: strp(f.APIKeyID), Search: search,
	})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// RecentEvents returns the newest n events.
func (s *Service) RecentEvents(ctx context.Context, n int64) ([]sqlcgen.Event, error) {
	if n <= 0 {
		n = 20
	}
	return s.Q.ListRecentEvents(ctx, n)
}

// EventKinds lists the distinct event kinds recorded.
func (s *Service) EventKinds(ctx context.Context) ([]string, error) {
	return s.Q.DistinctEventKinds(ctx)
}

// ParseRequestBody decodes a stored request body leniently (no validation).
func ParseRequestBody(r sqlcgen.Request) (*openai.ChatRequest, error) {
	var req openai.ChatRequest
	if err := json.Unmarshal([]byte(r.RequestBody), &req); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(r.RequestBody), &req.Raw)
	if r.Endpoint == "completions" && len(req.Messages) == 0 {
		var legacy openai.CompletionRequest
		if json.Unmarshal([]byte(r.RequestBody), &legacy) == nil {
			return legacy.ToChat(), nil
		}
	}
	return &req, nil
}

// ParseAnswer extracts the assistant's answer from a stored response body.
func ParseAnswer(r sqlcgen.Request) (openai.Answer, bool) {
	if r.ResponseBody == nil {
		return openai.Answer{}, false
	}
	usage := openai.Usage{PromptTokens: int(r.PromptTokens), CompletionTokens: int(r.CompletionTokens), TotalTokens: int(r.PromptTokens + r.CompletionTokens)}
	if r.Endpoint == "completions" {
		var tc openai.TextCompletion
		if err := json.Unmarshal([]byte(*r.ResponseBody), &tc); err != nil || len(tc.Choices) == 0 {
			return openai.Answer{}, false
		}
		return openai.Answer{Content: tc.Choices[0].Text, FinishReason: tc.Choices[0].FinishReason, Usage: &usage}, true
	}
	var cc openai.ChatCompletion
	if err := json.Unmarshal([]byte(*r.ResponseBody), &cc); err != nil || len(cc.Choices) == 0 {
		return openai.Answer{}, false
	}
	m := cc.Choices[0].Message
	a := openai.Answer{ToolCalls: m.ToolCalls, FinishReason: cc.Choices[0].FinishReason, Usage: &usage}
	if m.Content != nil {
		a.Content = *m.Content
	}
	if m.Refusal != nil {
		a.Refusal = *m.Refusal
	}
	return a, true
}
