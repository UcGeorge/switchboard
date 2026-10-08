// Package openai holds the OpenAI-compatible wire types, parsing and
// response builders, plus the message normalization used to reconstruct
// conversations across requests.
package openai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Requests

// Message is a chat message as sent by callers. Content is kept raw because
// it may be a string or an array of content parts.
type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Refusal    *string         `json:"refusal,omitempty"`
}

// ToolCall is a function tool invocation emitted by the assistant.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall names a function and carries its JSON-encoded arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// StreamOptions mirrors OpenAI's stream_options object.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ChatRequest is POST /v1/chat/completions. Unknown fields are preserved in
// Raw so they can be forwarded to the serving agent untouched.
type ChatRequest struct {
	Model               string                     `json:"model"`
	Messages            []Message                  `json:"messages"`
	Stream              bool                       `json:"stream,omitempty"`
	StreamOptions       *StreamOptions             `json:"stream_options,omitempty"`
	Temperature         *float64                   `json:"temperature,omitempty"`
	TopP                *float64                   `json:"top_p,omitempty"`
	MaxTokens           *int                       `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                       `json:"max_completion_tokens,omitempty"`
	N                   *int                       `json:"n,omitempty"`
	Stop                json.RawMessage            `json:"stop,omitempty"`
	Tools               json.RawMessage            `json:"tools,omitempty"`
	ToolChoice          json.RawMessage            `json:"tool_choice,omitempty"`
	ResponseFormat      json.RawMessage            `json:"response_format,omitempty"`
	User                string                     `json:"user,omitempty"`
	Metadata            map[string]any             `json:"metadata,omitempty"`
	Raw                 map[string]json.RawMessage `json:"-"`
}

var validRoles = map[string]bool{"system": true, "developer": true, "user": true, "assistant": true, "tool": true, "function": true}

// ParseChatRequest decodes and validates a chat completion body.
func ParseChatRequest(body []byte) (*ChatRequest, error) {
	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := json.Unmarshal(body, &req.Raw); err != nil {
		return nil, fmt.Errorf("invalid JSON object: %w", err)
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("'messages' must be a non-empty array")
	}
	for i, m := range req.Messages {
		if !validRoles[m.Role] {
			return nil, fmt.Errorf("messages[%d].role %q is not valid", i, m.Role)
		}
	}
	if req.N != nil && *req.N > 1 {
		return nil, errors.New("'n' > 1 is not supported")
	}
	return &req, nil
}

// Params returns the generation parameters (everything except the routing and
// transport fields) for forwarding to the serving agent.
func (r *ChatRequest) Params() map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(r.Raw))
	for k, v := range r.Raw {
		switch k {
		case "model", "messages", "stream", "stream_options":
			continue
		}
		out[k] = v
	}
	return out
}

// CompletionRequest is the legacy POST /v1/completions body.
type CompletionRequest struct {
	Model       string                     `json:"model"`
	Prompt      json.RawMessage            `json:"prompt"`
	Suffix      string                     `json:"suffix,omitempty"`
	MaxTokens   *int                       `json:"max_tokens,omitempty"`
	Temperature *float64                   `json:"temperature,omitempty"`
	TopP        *float64                   `json:"top_p,omitempty"`
	Stream      bool                       `json:"stream,omitempty"`
	Stop        json.RawMessage            `json:"stop,omitempty"`
	User        string                     `json:"user,omitempty"`
	Raw         map[string]json.RawMessage `json:"-"`
}

// ParseCompletionRequest decodes a legacy completion body.
func ParseCompletionRequest(body []byte) (*CompletionRequest, error) {
	var req CompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := json.Unmarshal(body, &req.Raw); err != nil {
		return nil, fmt.Errorf("invalid JSON object: %w", err)
	}
	if _, err := req.PromptText(); err != nil {
		return nil, err
	}
	return &req, nil
}

// PromptText flattens prompt (string or array of strings) into text.
func (r *CompletionRequest) PromptText() (string, error) {
	if len(r.Prompt) == 0 || string(r.Prompt) == "null" {
		return "", nil
	}
	var s string
	if json.Unmarshal(r.Prompt, &s) == nil {
		return s, nil
	}
	var arr []string
	if json.Unmarshal(r.Prompt, &arr) == nil {
		return strings.Join(arr, "\n"), nil
	}
	return "", errors.New("'prompt' must be a string or an array of strings")
}

// ToChat converts a legacy completion into a chat request.
func (r *CompletionRequest) ToChat() *ChatRequest {
	text, _ := r.PromptText()
	content, _ := json.Marshal(text)
	chat := &ChatRequest{
		Model:       r.Model,
		Messages:    []Message{{Role: "user", Content: content}},
		Stream:      r.Stream,
		Temperature: r.Temperature,
		TopP:        r.TopP,
		MaxTokens:   r.MaxTokens,
		Stop:        r.Stop,
		User:        r.User,
		Raw:         map[string]json.RawMessage{},
	}
	for k, v := range r.Raw {
		switch k {
		case "prompt", "suffix", "echo", "logprobs", "best_of":
			continue
		}
		chat.Raw[k] = v
	}
	chat.Raw["messages"], _ = json.Marshal(chat.Messages)
	return chat
}

// Message text, normalization and conversation hashing

// Text flattens message content into plain text. Non-text parts are
// represented by a bracketed placeholder so they still influence hashing.
func (m Message) Text() string {
	if len(m.Content) == 0 || string(m.Content) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &parts) == nil {
		var sb strings.Builder
		for i, p := range parts {
			if i > 0 {
				sb.WriteByte('\n')
			}
			switch p.Type {
			case "text", "":
				sb.WriteString(p.Text)
			case "image_url":
				sb.WriteString("[image]")
			case "input_audio":
				sb.WriteString("[audio]")
			case "file":
				sb.WriteString("[file]")
			default:
				sb.WriteByte('[')
				sb.WriteString(p.Type)
				sb.WriteByte(']')
			}
		}
		return sb.String()
	}
	return string(m.Content)
}

// Normalize renders a message into a canonical string: role, trimmed text,
// tool call names+arguments. IDs are excluded so client-side re-serialization
// does not break chain matching.
func Normalize(m Message) string {
	var sb strings.Builder
	sb.WriteString(m.Role)
	sb.WriteByte(0)
	sb.WriteString(strings.TrimSpace(m.Text()))
	sb.WriteByte(0)
	for _, tc := range m.ToolCalls {
		sb.WriteString(tc.Function.Name)
		sb.WriteByte(1)
		sb.WriteString(strings.TrimSpace(tc.Function.Arguments))
		sb.WriteByte(0)
	}
	return sb.String()
}

// ChainHash extends a running hash with one more message.
func ChainHash(prev string, m Message) string {
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte{'\n'})
	h.Write([]byte(Normalize(m)))
	return hex.EncodeToString(h.Sum(nil))
}

// PrefixHashes returns chain hashes for every prefix of msgs:
// hashes[k] covers msgs[:k]; hashes[0] is the empty prefix.
func PrefixHashes(msgs []Message) []string {
	hashes := make([]string, len(msgs)+1)
	prev := ""
	for i, m := range msgs {
		prev = ChainHash(prev, m)
		hashes[i+1] = prev
	}
	return hashes
}

// RootHash identifies the start of a conversation: the leading system/
// developer messages plus the first user message, scoped to an API key.
func RootHash(apiKeyID string, msgs []Message) string {
	prev := "root:" + apiKeyID
	sawUser := false
	for _, m := range msgs {
		switch m.Role {
		case "system", "developer":
			if sawUser {
				continue
			}
			prev = ChainHash(prev, m)
		case "user":
			prev = ChainHash(prev, m)
			sawUser = true
		}
		if sawUser {
			break
		}
	}
	return prev
}

// FirstUserText returns the first user message's text (for titles).
func FirstUserText(msgs []Message) string {
	for _, m := range msgs {
		if m.Role == "user" {
			return strings.TrimSpace(m.Text())
		}
	}
	if len(msgs) > 0 {
		return strings.TrimSpace(msgs[0].Text())
	}
	return ""
}

// LastMessage returns the final message, or a zero Message.
func LastMessage(msgs []Message) Message {
	if len(msgs) == 0 {
		return Message{}
	}
	return msgs[len(msgs)-1]
}

// EstimateTokens approximates a token count from text length. It is marked
// as an estimate everywhere it is displayed.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// EstimatePromptTokens approximates the prompt size of a chat request.
func EstimatePromptTokens(r *ChatRequest) int {
	n := 3
	for _, m := range r.Messages {
		n += 4 + EstimateTokens(Normalize(m))
	}
	n += EstimateTokens(string(r.Tools)) / 1
	return n
}

// PromptChars counts the characters in all message text.
func PromptChars(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Text())
		for _, tc := range m.ToolCalls {
			n += len(tc.Function.Arguments) + len(tc.Function.Name)
		}
	}
	return n
}

// Answers (what a serving agent submits)

// Answer is the agent's response to a request before it is wrapped in the
// OpenAI envelope.
type Answer struct {
	Content      string     `json:"content"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Refusal      string     `json:"refusal,omitempty"`
	Usage        *Usage     `json:"usage,omitempty"`
}

// Normalized fills defaults: tool call ids/types and finish reason.
func (a Answer) Normalized(newID func() string) Answer {
	for i := range a.ToolCalls {
		if a.ToolCalls[i].ID == "" {
			a.ToolCalls[i].ID = "call_" + newID()
		}
		if a.ToolCalls[i].Type == "" {
			a.ToolCalls[i].Type = "function"
		}
		if a.ToolCalls[i].Function.Arguments == "" {
			a.ToolCalls[i].Function.Arguments = "{}"
		}
	}
	if a.FinishReason == "" {
		if len(a.ToolCalls) > 0 {
			a.FinishReason = "tool_calls"
		} else {
			a.FinishReason = "stop"
		}
	}
	return a
}

// AssistantMessage renders the answer as the assistant message a client will
// append to its history, for chain hashing.
func (a Answer) AssistantMessage() Message {
	content, _ := json.Marshal(a.Content)
	m := Message{Role: "assistant", Content: content, ToolCalls: a.ToolCalls}
	if a.Content == "" && len(a.ToolCalls) > 0 {
		m.Content = json.RawMessage("null")
	}
	return m
}

// Responses

// Usage is the token accounting block.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ResponseMessage is the assistant message inside a completion.
type ResponseMessage struct {
	Role      string     `json:"role"`
	Content   *string    `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Refusal   *string    `json:"refusal"`
}

// ChatChoice is one completion choice.
type ChatChoice struct {
	Index        int             `json:"index"`
	Message      ResponseMessage `json:"message"`
	Logprobs     any             `json:"logprobs"`
	FinishReason string          `json:"finish_reason"`
}

// ChatCompletion is the non-streaming response envelope.
type ChatCompletion struct {
	ID                string       `json:"id"`
	Object            string       `json:"object"`
	Created           int64        `json:"created"`
	Model             string       `json:"model"`
	Choices           []ChatChoice `json:"choices"`
	Usage             Usage        `json:"usage"`
	SystemFingerprint string       `json:"system_fingerprint"`
}

// Delta is the incremental payload in a streaming chunk.
type Delta struct {
	Role      string          `json:"role,omitempty"`
	Content   *string         `json:"content,omitempty"`
	ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`
	Refusal   *string         `json:"refusal,omitempty"`
}

// ToolCallDelta is a streamed tool call fragment.
type ToolCallDelta struct {
	Index    int                `json:"index"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function *FunctionCallDelta `json:"function,omitempty"`
}

// FunctionCallDelta is a streamed function fragment.
type FunctionCallDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

// ChunkChoice is one choice in a streaming chunk.
type ChunkChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	Logprobs     any     `json:"logprobs"`
	FinishReason *string `json:"finish_reason"`
}

// ChatChunk is a streaming response chunk.
type ChatChunk struct {
	ID                string        `json:"id"`
	Object            string        `json:"object"`
	Created           int64         `json:"created"`
	Model             string        `json:"model"`
	Choices           []ChunkChoice `json:"choices"`
	Usage             *Usage        `json:"usage,omitempty"`
	SystemFingerprint string        `json:"system_fingerprint"`
}

// TextChoice is a legacy completion choice.
type TextChoice struct {
	Text         string `json:"text"`
	Index        int    `json:"index"`
	Logprobs     any    `json:"logprobs"`
	FinishReason string `json:"finish_reason"`
}

// TextCompletion is the legacy /v1/completions envelope.
type TextCompletion struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []TextChoice `json:"choices"`
	Usage   Usage        `json:"usage"`
}

// Model is one entry in GET /v1/models.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelList is GET /v1/models.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

const SystemFingerprint = "fp_switchboard"

// CompletionID derives the public chatcmpl id from an internal request id.
func CompletionID(requestID string) string {
	return "chatcmpl-" + strings.TrimPrefix(requestID, "req_")
}

// BuildChatCompletion wraps an answer in the non-streaming envelope.
func BuildChatCompletion(requestID, model string, created time.Time, a Answer, usage Usage) ChatCompletion {
	content := a.Content
	msg := ResponseMessage{Role: "assistant", Content: &content, ToolCalls: a.ToolCalls}
	if a.Content == "" && len(a.ToolCalls) > 0 {
		msg.Content = nil
	}
	if a.Refusal != "" {
		r := a.Refusal
		msg.Refusal = &r
	}
	return ChatCompletion{
		ID:                CompletionID(requestID),
		Object:            "chat.completion",
		Created:           created.Unix(),
		Model:             model,
		Choices:           []ChatChoice{{Index: 0, Message: msg, FinishReason: a.FinishReason}},
		Usage:             usage,
		SystemFingerprint: SystemFingerprint,
	}
}

// BuildChunk wraps a delta in the streaming envelope.
func BuildChunk(requestID, model string, created time.Time, delta Delta, finish *string, usage *Usage) ChatChunk {
	ch := ChatChunk{
		ID:                CompletionID(requestID),
		Object:            "chat.completion.chunk",
		Created:           created.Unix(),
		Model:             model,
		Choices:           []ChunkChoice{{Index: 0, Delta: delta, FinishReason: finish}},
		SystemFingerprint: SystemFingerprint,
		Usage:             usage,
	}
	return ch
}

// UsageOnlyChunk is the trailing chunk sent when stream_options.include_usage
// is set: no choices, just usage.
func UsageOnlyChunk(requestID, model string, created time.Time, usage Usage) ChatChunk {
	return ChatChunk{
		ID:                CompletionID(requestID),
		Object:            "chat.completion.chunk",
		Created:           created.Unix(),
		Model:             model,
		Choices:           []ChunkChoice{},
		Usage:             &usage,
		SystemFingerprint: SystemFingerprint,
	}
}

// BuildTextCompletion wraps an answer in the legacy envelope.
func BuildTextCompletion(requestID, model string, created time.Time, a Answer, usage Usage) TextCompletion {
	return TextCompletion{
		ID:      "cmpl-" + strings.TrimPrefix(requestID, "req_"),
		Object:  "text_completion",
		Created: created.Unix(),
		Model:   model,
		Choices: []TextChoice{{Text: a.Content, Index: 0, FinishReason: a.FinishReason}},
		Usage:   usage,
	}
}

// ToolCallsAsDeltas converts complete tool calls into streaming fragments.
func ToolCallsAsDeltas(calls []ToolCall, startIndex int) []ToolCallDelta {
	out := make([]ToolCallDelta, 0, len(calls))
	for i, tc := range calls {
		out = append(out, ToolCallDelta{
			Index:    startIndex + i,
			ID:       tc.ID,
			Type:     tc.Type,
			Function: &FunctionCallDelta{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
		})
	}
	return out
}

// Errors

// Error type strings used by OpenAI clients to classify failures.
const (
	ErrTypeInvalidRequest = "invalid_request_error"
	ErrTypeAuthentication = "authentication_error"
	ErrTypePermission     = "permission_error"
	ErrTypeRateLimit      = "rate_limit_error"
	ErrTypeServer         = "server_error"
	ErrTypeNotFound       = "not_found_error"
)

// ErrorBody is the inner error object.
type ErrorBody struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

// ErrorResponse is the OpenAI error envelope.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// WriteError writes an OpenAI-format error with the given HTTP status.
func WriteError(w http.ResponseWriter, status int, typ, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var codePtr *string
	if code != "" {
		codePtr = &code
	}
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: ErrorBody{Message: msg, Type: typ, Code: codePtr}})
}

// WriteJSON writes v as JSON with status 200.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
