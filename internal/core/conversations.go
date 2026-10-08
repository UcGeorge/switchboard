package core

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/ids"
	"github.com/ucgeorge/switchboard/internal/openai"
)

// rootMatchWindow bounds how far back a root-hash match may reach when the
// chain match fails (e.g. the client rewrote an assistant turn).
const rootMatchWindow = 24 * time.Hour

// ConversationFilter narrows ListConversations.
type ConversationFilter struct {
	APIKeyID string
	Search   string
	Limit    int64
	Offset   int64
}

type convLink struct {
	ConversationID string
	TurnIndex      int64
	HistoryHash    string
}

// linkConversation attaches an incoming request to the conversation it
// continues. Strategy, in order:
//
//  1. Chain match: for each prefix of the messages that ends in an assistant
//     turn, look for a previous request whose (messages + answer) hash equals
//     that prefix. The longest match wins. This is exact when clients replay
//     history verbatim, including tool-call rounds.
//  2. Root match: same API key, same system prompt + first user message,
//     updated within the last 24h.
//  3. Otherwise a new conversation is created.
func (s *Service) linkConversation(ctx context.Context, q *sqlcgen.Queries, apiKeyID, requestID, model string, req *openai.ChatRequest, prefixes []string, now int64) (convLink, error) {
	msgs := req.Messages
	history := prefixes[len(msgs)-1]

	for k := len(msgs) - 1; k >= 1; k-- {
		if msgs[k-1].Role != "assistant" {
			continue
		}
		parent, err := q.FindRequestByNextHash(ctx, sqlcgen.FindRequestByNextHashParams{ApiKeyID: apiKeyID, NextHash: strp(prefixes[k])})
		if err != nil || parent.ConversationID == nil {
			continue
		}
		conv, err := q.GetConversation(ctx, *parent.ConversationID)
		if err != nil {
			continue
		}
		turn := parent.TurnIndex + 1
		if err := q.BumpConversation(ctx, sqlcgen.BumpConversationParams{
			TurnCount: max(conv.TurnCount, turn+1), LastRequestID: ptr(requestID), Model: model, UpdatedAt: now, ID: conv.ID,
		}); err != nil {
			return convLink{}, err
		}
		return convLink{ConversationID: conv.ID, TurnIndex: turn, HistoryHash: prefixes[k]}, nil
	}

	root := openai.RootHash(apiKeyID, msgs)
	// Only a continuation (it carries earlier assistant turns) may fall back
	// to root matching. An opening request never does, otherwise unrelated
	// chats that share a system prompt and first message would be merged.
	continuation := false
	for _, m := range msgs {
		if m.Role == "assistant" {
			continuation = true
			break
		}
	}
	if continuation {
		conv, err := q.FindConversationByRoot(ctx, sqlcgen.FindConversationByRootParams{
			ApiKeyID: apiKeyID, RootHash: root, UpdatedAt: now - rootMatchWindow.Milliseconds(),
		})
		if err == nil {
			turn := conv.TurnCount
			if err := q.BumpConversation(ctx, sqlcgen.BumpConversationParams{
				TurnCount: turn + 1, LastRequestID: ptr(requestID), Model: model, UpdatedAt: now, ID: conv.ID,
			}); err != nil {
				return convLink{}, err
			}
			return convLink{ConversationID: conv.ID, TurnIndex: turn, HistoryHash: history}, nil
		}
	}

	conv, err := q.CreateConversation(ctx, sqlcgen.CreateConversationParams{
		ID:            ids.New("conv"),
		ApiKeyID:      apiKeyID,
		RootHash:      root,
		Title:         TitleFrom(openai.FirstUserText(msgs)),
		Model:         model,
		TurnCount:     1,
		LastRequestID: ptr(requestID),
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	if err != nil {
		return convLink{}, err
	}
	return convLink{ConversationID: conv.ID, TurnIndex: 0, HistoryHash: history}, nil
}

// TitleFrom derives a short single-line title from message text.
func TitleFrom(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "(untitled)"
	}
	const limit = 80
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// GetConversation fetches a conversation.
func (s *Service) GetConversation(ctx context.Context, id string) (sqlcgen.Conversation, error) {
	c, err := s.Q.GetConversation(ctx, id)
	if isNoRows(err) {
		return c, ErrNotFound
	}
	return c, err
}

// ListConversations pages conversations newest-activity first.
func (s *Service) ListConversations(ctx context.Context, f ConversationFilter) ([]sqlcgen.Conversation, int64, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	search := likePattern(f.Search)
	rows, err := s.Q.ListConversations(ctx, sqlcgen.ListConversationsParams{ApiKeyID: f.APIKeyID, Search: search, Lim: f.Limit, Off: f.Offset})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.Q.CountConversations(ctx, sqlcgen.CountConversationsParams{ApiKeyID: f.APIKeyID, Search: search})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ConversationRequests returns a conversation's requests in turn order.
func (s *Service) ConversationRequests(ctx context.Context, id string) ([]sqlcgen.Request, error) {
	return s.Q.ListConversationRequests(ctx, strp(id))
}

// DeleteConversation removes the conversation record (requests keep the id).
func (s *Service) DeleteConversation(ctx context.Context, id string) error {
	n, err := s.Q.DeleteConversation(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
