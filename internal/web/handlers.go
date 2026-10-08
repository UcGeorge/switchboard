package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/openai"
)

const perPage = 50

// overview

type windowOpt struct {
	Value, Label string
	Active       bool
}

var windows = []struct {
	key string
	d   time.Duration
}{{"15m", 15 * time.Minute}, {"1h", time.Hour}, {"6h", 6 * time.Hour}, {"24h", 24 * time.Hour}, {"7d", 7 * 24 * time.Hour}}

func windowFrom(r *http.Request) (string, time.Duration) {
	key := r.URL.Query().Get("w")
	for _, w := range windows {
		if w.key == key {
			return w.key, w.d
		}
	}
	return "1h", time.Hour
}

type usageRow struct {
	ID, Name                        string
	Total, Completed, Prompt, Compl int64
	Share                           float64
}

type overviewPage struct {
	Page
	O          core.Overview
	WindowKey  string
	Windows    []windowOpt
	Chart      ChartView
	Recent     RequestTable
	Channels   []ChannelView
	Events     []EventView
	Models     []usageRow
	Keys       []usageRow
	Uptime     string
	NoKeys     bool
	NoChannels bool
	BaseURL    string
}

func (s *Server) channelViews(ctx context.Context, chans []sqlcgen.Channel, names nameMaps) []ChannelView {
	out := make([]ChannelView, 0, len(chans))
	for _, c := range chans {
		v := ChannelView{Channel: c, ModelList: core.ChannelModels(c), AvgLatencyMs: core.AvgLatencyMs(c), Closed: c.ClosedAt != nil}
		v.InFlight, _ = s.svc.ChannelInFlight(ctx, c.ID)
		if c.AgentTokenID != nil {
			v.TokenName = names.tokens[*c.AgentTokenID]
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key, window := windowFrom(r)
	o, err := s.svc.Overview(ctx, window)
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	names := s.names(ctx)
	p := overviewPage{Page: s.page(r, "Overview", "home"), O: o, WindowKey: key, Chart: chartView(o.Buckets, window), Uptime: s.svc.Uptime().String(), BaseURL: s.baseURL(r)}
	for _, wd := range windows {
		p.Windows = append(p.Windows, windowOpt{Value: wd.key, Label: wd.key, Active: wd.key == key})
	}
	recent, _ := s.svc.RecentRequests(ctx, 10)
	rows := make([]RequestView, 0, len(recent))
	for _, rq := range recent {
		rows = append(rows, requestView(rq, names))
	}
	p.Recent = RequestTable{Rows: rows, ID: "recent-requests", ShowKey: true, ShowChannel: true, EmptyText: "No requests yet"}
	chans, _ := s.svc.ListOpenChannels(ctx)
	p.Channels = s.channelViews(ctx, chans, names)
	evs, _ := s.svc.RecentEvents(ctx, 12)
	for _, e := range evs {
		p.Events = append(p.Events, eventView(e))
	}
	for _, m := range o.Models {
		row := usageRow{Name: m.Model, Total: m.Total, Prompt: m.PromptTokens, Compl: m.CompletionTokens}
		if o.Total > 0 {
			row.Share = float64(m.Total) / float64(o.Total) * 100
		}
		p.Models = append(p.Models, row)
	}
	for _, k := range o.Keys {
		row := usageRow{ID: k.ApiKeyID, Name: names.keys[k.ApiKeyID], Total: k.Total, Completed: k.Completed, Prompt: k.PromptTokens, Compl: k.CompletionTokens}
		if row.Name == "" {
			row.Name = "(deleted key)"
		}
		if o.Total > 0 {
			row.Share = float64(k.Total) / float64(o.Total) * 100
		}
		p.Keys = append(p.Keys, row)
	}
	p.NoKeys = o.KeysActive == 0
	p.NoChannels = o.ChannelsOpen == 0
	s.render(w, r, "overview", http.StatusOK, p)
}

// requests

func requestFilter(r *http.Request) core.RequestFilter {
	q := r.URL.Query()
	return core.RequestFilter{Status: q.Get("status"), APIKeyID: q.Get("api_key_id"), ChannelID: q.Get("channel_id"), ConversationID: q.Get("conversation_id"), Model: q.Get("model"), Search: q.Get("q")}
}

func (s *Server) requestTable(ctx context.Context, r *http.Request, f core.RequestFilter, id string) (RequestTable, error) {
	page := pageParam(r)
	f.Limit = perPage
	f.Offset = int64((page - 1) * perPage)
	rows, total, err := s.svc.ListRequests(ctx, f)
	if err != nil {
		return RequestTable{}, err
	}
	names := s.names(ctx)
	views := make([]RequestView, 0, len(rows))
	for _, rq := range rows {
		views = append(views, requestView(rq, names))
	}
	q := cloneValues(r.URL.Query())
	return RequestTable{
		Rows: views, Pager: newPager("/requests", q, page, perPage, total), ID: id,
		URL:     "/requests/table?" + q.Encode(),
		ShowKey: f.APIKeyID == "", ShowChannel: f.ChannelID == "",
	}, nil
}

type requestsPage struct {
	Page
	Table    RequestTable
	Filter   core.RequestFilter
	Statuses []string
	Keys     []sqlcgen.ApiKey
	Channels []sqlcgen.Channel
	Models   []string
}

func (s *Server) requests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f := requestFilter(r)
	table, err := s.requestTable(ctx, r, f, "requests-table")
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	p := requestsPage{Page: s.page(r, "Requests", "requests"), Table: table, Filter: f, Statuses: core.AllStatuses}
	p.Keys, _ = s.svc.ListAPIKeys(ctx)
	p.Channels, _ = s.svc.ListChannels(ctx)
	p.Models, _ = s.svc.DistinctModels(ctx)
	s.render(w, r, "requests", http.StatusOK, p)
}

func (s *Server) requestsTable(w http.ResponseWriter, r *http.Request) {
	table, err := s.requestTable(r.Context(), r, requestFilter(r), "requests-table")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.partial(w, http.StatusOK, "request-table", table)
}

type requestPage struct {
	Page
	R            RequestView
	Messages     []MessageView
	Answer       *openai.Answer
	HasAnswer    bool
	Events       []EventView
	Params       string
	RequestJSON  string
	ResponseJSON string
	Conversation *sqlcgen.Conversation
	Channel      *ChannelView
	Live         bool
	LiveText     string
	Timeout      string
	Remaining    string
	CompletionID string
}

func (s *Server) loadRequest(w http.ResponseWriter, r *http.Request) (sqlcgen.Request, bool) {
	rec, err := s.svc.GetRequest(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, core.ErrRequestNotFound) {
			s.errorPage(w, r, http.StatusNotFound, "No request with id "+r.PathValue("id"))
		} else {
			s.errorPage(w, r, http.StatusInternalServerError, err.Error())
		}
		return rec, false
	}
	return rec, true
}

func (s *Server) request(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rec, ok := s.loadRequest(w, r)
	if !ok {
		return
	}
	names := s.names(ctx)
	p := requestPage{Page: s.page(r, "Request "+shortID(rec.ID, "req_"), "requests"), R: requestView(rec, names), CompletionID: openai.CompletionID(rec.ID)}
	if req, err := core.ParseRequestBody(rec); err == nil {
		p.Messages = messageViews(req.Messages)
		p.Params = prettyJSON(req.Params())
	}
	p.RequestJSON = prettyJSON(rec.RequestBody)
	if rec.ResponseBody != nil {
		p.ResponseJSON = prettyJSON(*rec.ResponseBody)
	}
	if a, ok := core.ParseAnswer(rec); ok {
		p.Answer = &a
		p.HasAnswer = true
	}
	evs, _ := s.svc.RequestEvents(ctx, rec.ID)
	for _, e := range evs {
		p.Events = append(p.Events, eventView(e))
	}
	if rec.ConversationID != nil {
		if c, err := s.svc.GetConversation(ctx, *rec.ConversationID); err == nil {
			p.Conversation = &c
		}
	}
	if rec.ChannelID != nil {
		if c, err := s.svc.GetChannel(ctx, *rec.ChannelID); err == nil {
			cv := s.channelViews(ctx, []sqlcgen.Channel{c}, names)[0]
			p.Channel = &cv
		}
	}
	p.Live = rec.Status == core.StatusClaimed || rec.Status == core.StatusStreaming
	if p.Live {
		_, replay, _, unsub := s.svc.SubscribeRequest(rec.ID)
		unsub()
		var sb strings.Builder
		for _, d := range replay {
			if d.Content != nil {
				sb.WriteString(*d.Content)
			}
		}
		p.LiveText = sb.String()
	}
	p.Timeout = durStr(rec.TimeoutAt - rec.CreatedAt)
	if core.IsActive(rec.Status) {
		p.Remaining = time.Until(time.UnixMilli(rec.TimeoutAt)).Round(time.Second).String()
	}
	s.render(w, r, "request", http.StatusOK, p)
}

func (s *Server) requestCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.svc.CancelRequest(r.Context(), id, "cancelled by operator"); err != nil {
		s.flash(w, "err", err.Error())
	} else {
		s.flash(w, "ok", "Request cancelled.")
	}
	s.redirect(w, r, "/requests/"+id)
}

func (s *Server) requestRequeue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.svc.RequeueRequest(r.Context(), id, "requeued by operator"); err != nil {
		s.flash(w, "err", err.Error())
	} else {
		s.flash(w, "ok", "Request returned to the queue (or failed if it could not be retried).")
	}
	s.redirect(w, r, "/requests/"+id)
}

type answerForm struct {
	RequestID, Model, Prompt, Content, Error, HeldBy string
	Held                                             bool
}

func (s *Server) answerFormFor(ctx context.Context, rec sqlcgen.Request) answerForm {
	f := answerForm{RequestID: rec.ID, Model: rec.Model}
	if req, err := core.ParseRequestBody(rec); err == nil {
		f.Prompt = openai.LastMessage(req.Messages).Text()
	}
	if rec.ChannelID != nil && (rec.Status == core.StatusClaimed || rec.Status == core.StatusStreaming) {
		if ch, err := s.svc.GetChannel(ctx, *rec.ChannelID); err == nil && ch.Name != core.ManualChannelName {
			f.Held = true
			f.HeldBy = ch.Name
		}
	}
	return f
}

func (s *Server) requestAnswerForm(w http.ResponseWriter, r *http.Request) {
	rec, err := s.svc.GetRequest(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}
	if core.IsTerminal(rec.Status) {
		http.Error(w, "request is already "+rec.Status, http.StatusConflict)
		return
	}
	s.partial(w, http.StatusOK, "modal-answer-form", s.answerFormFor(r.Context(), rec))
}

func (s *Server) requestAnswer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rec, err := s.svc.GetRequest(ctx, r.PathValue("id"))
	if err != nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}
	_ = r.ParseForm()
	f := s.answerFormFor(ctx, rec)
	f.Content = r.PostFormValue("content")
	if strings.TrimSpace(f.Content) == "" {
		f.Error = "Reply cannot be empty."
		s.partial(w, http.StatusUnprocessableEntity, "modal-answer-form", f)
		return
	}
	a := openai.Answer{Content: f.Content, FinishReason: r.PostFormValue("finish_reason")}
	if _, err := s.svc.AnswerRequest(ctx, rec.ID, a, r.PostFormValue("force") == "1"); err != nil {
		f.Error = err.Error()
		s.partial(w, http.StatusUnprocessableEntity, "modal-answer-form", f)
		return
	}
	s.flash(w, "ok", "Answer delivered to the caller.")
	s.redirect(w, r, "/requests/"+rec.ID)
}

// conversations

type convRow struct {
	sqlcgen.Conversation
	KeyName string
}

type conversationsPage struct {
	Page
	Rows   []convRow
	Pager  Pager
	Filter core.ConversationFilter
	Keys   []sqlcgen.ApiKey
}

func (s *Server) conversations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := pageParam(r)
	f := core.ConversationFilter{APIKeyID: r.URL.Query().Get("api_key_id"), Search: r.URL.Query().Get("q"), Limit: perPage, Offset: int64((page - 1) * perPage)}
	rows, total, err := s.svc.ListConversations(ctx, f)
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	names := s.names(ctx)
	p := conversationsPage{Page: s.page(r, "Conversations", "conversations"), Filter: f, Pager: newPager("/conversations", r.URL.Query(), page, perPage, total)}
	for _, c := range rows {
		p.Rows = append(p.Rows, convRow{Conversation: c, KeyName: names.keys[c.ApiKeyID]})
	}
	p.Keys, _ = s.svc.ListAPIKeys(ctx)
	s.render(w, r, "conversations", http.StatusOK, p)
}

type conversationPage struct {
	Page
	C       sqlcgen.Conversation
	KeyName string
	Turns   []TurnView
	Stats   struct {
		Requests, Completed, PromptTokens, CompletionTokens, TotalLatencyMs, AvgLatencyMs int64
		Channels                                                                          []string
	}
}

func (s *Server) conversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.svc.GetConversation(ctx, r.PathValue("id"))
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "No conversation with id "+r.PathValue("id"))
		return
	}
	names := s.names(ctx)
	p := conversationPage{Page: s.page(r, "Conversation", "conversations"), C: c, KeyName: names.keys[c.ApiKeyID]}
	reqs, _ := s.svc.ConversationRequests(ctx, c.ID)
	prevCount := 0
	seen := map[string]bool{}
	for i, rq := range reqs {
		req, err := core.ParseRequestBody(rq)
		if err != nil {
			continue
		}
		msgs := req.Messages
		var shown []openai.Message
		switch {
		case i == 0:
			shown = msgs
		case len(msgs) > prevCount+1:
			shown = msgs[prevCount+1:]
		case len(msgs) > 0:
			shown = msgs[len(msgs)-1:]
		}
		turn := TurnView{Request: requestView(rq, names), Messages: messageViews(shown)}
		if a, ok := core.ParseAnswer(rq); ok {
			turn.Answer = &a
			turn.HasAnswer = true
		}
		p.Turns = append(p.Turns, turn)
		prevCount = len(msgs)
		p.Stats.Requests++
		if rq.Status == core.StatusCompleted {
			p.Stats.Completed++
			p.Stats.TotalLatencyMs += turn.Request.LatencyMs
		}
		p.Stats.PromptTokens += rq.PromptTokens
		p.Stats.CompletionTokens += rq.CompletionTokens
		if rq.ChannelID != nil && !seen[*rq.ChannelID] {
			seen[*rq.ChannelID] = true
			if n := names.channels[*rq.ChannelID]; n != "" {
				p.Stats.Channels = append(p.Stats.Channels, n)
			}
		}
	}
	if p.Stats.Completed > 0 {
		p.Stats.AvgLatencyMs = p.Stats.TotalLatencyMs / p.Stats.Completed
	}
	s.render(w, r, "conversation", http.StatusOK, p)
}

func (s *Server) conversationDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteConversation(r.Context(), r.PathValue("id")); err != nil {
		s.flash(w, "err", err.Error())
		s.redirect(w, r, "/conversations/"+r.PathValue("id"))
		return
	}
	s.flash(w, "ok", "Conversation removed (its requests are kept).")
	s.redirect(w, r, "/conversations")
}

// channels

type channelsPage struct {
	Page
	Open   []ChannelView
	Closed []ChannelView
	MCPURL string
}

func (s *Server) channels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, err := s.svc.ListChannels(ctx)
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	names := s.names(ctx)
	p := channelsPage{Page: s.page(r, "Channels", "channels"), MCPURL: s.baseURL(r) + "/mcp"}
	for _, v := range s.channelViews(ctx, all, names) {
		if v.Closed {
			p.Closed = append(p.Closed, v)
		} else {
			p.Open = append(p.Open, v)
		}
	}
	s.render(w, r, "channels", http.StatusOK, p)
}

type channelPage struct {
	Page
	C           ChannelView
	Requests    RequestTable
	Events      []EventView
	Total       int64
	Completed   int64
	Failed      int64
	SuccessRate float64
}

func (s *Server) channel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.svc.GetChannel(ctx, r.PathValue("id"))
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "No channel with id "+r.PathValue("id"))
		return
	}
	names := s.names(ctx)
	p := channelPage{Page: s.page(r, "Channel "+c.Name, "channels"), C: s.channelViews(ctx, []sqlcgen.Channel{c}, names)[0]}
	q := url.Values{"channel_id": {c.ID}}
	rr := r.Clone(ctx)
	rr.URL.RawQuery = q.Encode()
	p.Requests, _ = s.requestTable(ctx, rr, core.RequestFilter{ChannelID: c.ID}, "channel-requests")
	evs, _, _ := s.svc.ListEvents(ctx, core.EventFilter{ChannelID: c.ID, Limit: 30})
	for _, e := range evs {
		p.Events = append(p.Events, eventView(e))
	}
	usage, _ := s.svc.Q.ChannelUsageSince(ctx, time.Now().Add(-24*time.Hour).UnixMilli())
	for _, u := range usage {
		if u.ChannelID != nil && *u.ChannelID == c.ID {
			p.Total, p.Completed, p.Failed = u.Total, u.Completed, u.Failed
		}
	}
	if fin := p.Completed + p.Failed; fin > 0 {
		p.SuccessRate = float64(p.Completed) / float64(fin) * 100
	}
	s.render(w, r, "channel", http.StatusOK, p)
}

type channelForm struct {
	ID, Name, Description, Models, Error string
	Concurrency                          int64
}

func (s *Server) channelEditForm(w http.ResponseWriter, r *http.Request) {
	c, err := s.svc.GetChannel(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	s.partial(w, http.StatusOK, "modal-channel-form", channelForm{ID: c.ID, Name: c.Name, Description: c.Description, Models: c.Models, Concurrency: c.Concurrency})
}

func (s *Server) channelEdit(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f := channelForm{ID: r.PathValue("id"), Name: r.PostFormValue("name"), Description: r.PostFormValue("description"), Models: r.PostFormValue("models"), Concurrency: formInt(r, "concurrency")}
	err := s.svc.UpdateChannel(r.Context(), f.ID, core.ChannelInput{Name: f.Name, Description: f.Description, Models: core.SplitList(f.Models), Concurrency: f.Concurrency})
	if err != nil {
		f.Error = err.Error()
		s.partial(w, http.StatusUnprocessableEntity, "modal-channel-form", f)
		return
	}
	s.flash(w, "ok", "Channel updated.")
	s.redirect(w, r, "/channels/"+f.ID)
}

func (s *Server) channelAction(w http.ResponseWriter, r *http.Request, fn func(context.Context, string) error, okMsg string) {
	id := r.PathValue("id")
	if err := fn(r.Context(), id); err != nil {
		s.flash(w, "err", err.Error())
	} else {
		s.flash(w, "ok", okMsg)
	}
	s.redirect(w, r, "/channels/"+id)
}

func (s *Server) channelDrain(w http.ResponseWriter, r *http.Request) {
	s.channelAction(w, r, func(ctx context.Context, id string) error { return s.svc.SetChannelDraining(ctx, id, true) }, "Channel is draining: no new claims.")
}

func (s *Server) channelResume(w http.ResponseWriter, r *http.Request) {
	s.channelAction(w, r, func(ctx context.Context, id string) error { return s.svc.SetChannelDraining(ctx, id, false) }, "Channel resumed.")
}

func (s *Server) channelClose(w http.ResponseWriter, r *http.Request) {
	s.channelAction(w, r, func(ctx context.Context, id string) error {
		return s.svc.CloseChannel(ctx, id, "", "closed by operator")
	}, "Channel closed; its in-flight requests were requeued.")
}

func (s *Server) channelDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteChannel(r.Context(), r.PathValue("id")); err != nil {
		s.flash(w, "err", err.Error())
		s.redirect(w, r, "/channels/"+r.PathValue("id"))
		return
	}
	s.flash(w, "ok", "Channel deleted.")
	s.redirect(w, r, "/channels")
}

// API keys

type keyView struct {
	sqlcgen.ApiKey
	PinnedChannelName string
	Allowed           []string
	Revoked           bool
	Total24h          int64
	Completed24h      int64
	Tokens24h         int64
}

type keysPage struct {
	Page
	Keys    []keyView
	BaseURL string
}

func (s *Server) keys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keys, err := s.svc.ListAPIKeys(ctx)
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	names := s.names(ctx)
	usage := map[string]sqlcgen.KeyUsageSinceRow{}
	if rows, err := s.svc.Q.KeyUsageSince(ctx, time.Now().Add(-24*time.Hour).UnixMilli()); err == nil {
		for _, u := range rows {
			usage[u.ApiKeyID] = u
		}
	}
	p := keysPage{Page: s.page(r, "API keys", "keys"), BaseURL: s.baseURL(r)}
	for _, k := range keys {
		v := keyView{ApiKey: k, Allowed: core.SplitList(k.AllowedModels), Revoked: k.RevokedAt != nil}
		if k.PinnedChannelID != nil {
			v.PinnedChannelName = names.channels[*k.PinnedChannelID]
		}
		if u, ok := usage[k.ID]; ok {
			v.Total24h, v.Completed24h, v.Tokens24h = u.Total, u.Completed, u.PromptTokens+u.CompletionTokens
		}
		p.Keys = append(p.Keys, v)
	}
	s.render(w, r, "keys", http.StatusOK, p)
}

type keyForm struct {
	ID, Name, AllowedModels, PinnedChannelID, Error, Action string
	RateLimitRPM, TimeoutSeconds, Priority                  int64
	Edit                                                    bool
	Channels                                                []ChannelView
}

func (s *Server) keyFormChannels(ctx context.Context) []ChannelView {
	chans, _ := s.svc.ListChannels(ctx)
	return s.channelViews(ctx, chans, s.names(ctx))
}

func (s *Server) keyNewForm(w http.ResponseWriter, r *http.Request) {
	s.partial(w, http.StatusOK, "modal-key-form", keyForm{Action: "/keys", Channels: s.keyFormChannels(r.Context())})
}

func keyInputFrom(r *http.Request) core.APIKeyInput {
	return core.APIKeyInput{
		Name: r.PostFormValue("name"), RateLimitRPM: formInt(r, "rate_limit_rpm"), AllowedModels: core.SplitList(r.PostFormValue("allowed_models")),
		PinnedChannelID: r.PostFormValue("pinned_channel_id"), TimeoutSeconds: formInt(r, "timeout_seconds"), Priority: formInt(r, "priority"),
	}
}

func keyFormFrom(r *http.Request, in core.APIKeyInput) keyForm {
	return keyForm{Name: in.Name, AllowedModels: core.JoinList(in.AllowedModels), PinnedChannelID: in.PinnedChannelID, RateLimitRPM: in.RateLimitRPM, TimeoutSeconds: in.TimeoutSeconds, Priority: in.Priority}
}

func (s *Server) keyCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	in := keyInputFrom(r)
	k, plain, err := s.svc.CreateAPIKey(r.Context(), in)
	if err != nil {
		f := keyFormFrom(r, in)
		f.Action, f.Error, f.Channels = "/keys", err.Error(), s.keyFormChannels(r.Context())
		s.partial(w, http.StatusUnprocessableEntity, "modal-key-form", f)
		return
	}
	s.partial(w, http.StatusOK, "modal-key-created", map[string]string{"Name": k.Name, "Key": plain, "BaseURL": s.baseURL(r)})
}

func (s *Server) keyEditForm(w http.ResponseWriter, r *http.Request) {
	k, err := s.svc.GetAPIKey(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}
	f := keyForm{ID: k.ID, Name: k.Name, AllowedModels: k.AllowedModels, RateLimitRPM: k.RateLimitRpm, TimeoutSeconds: k.TimeoutSeconds, Priority: k.Priority, Edit: true, Action: "/keys/" + k.ID + "/edit", Channels: s.keyFormChannels(r.Context())}
	if k.PinnedChannelID != nil {
		f.PinnedChannelID = *k.PinnedChannelID
	}
	s.partial(w, http.StatusOK, "modal-key-form", f)
}

func (s *Server) keyEdit(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := r.PathValue("id")
	in := keyInputFrom(r)
	if err := s.svc.UpdateAPIKey(r.Context(), id, in); err != nil {
		f := keyFormFrom(r, in)
		f.ID, f.Edit, f.Action, f.Error, f.Channels = id, true, "/keys/"+id+"/edit", err.Error(), s.keyFormChannels(r.Context())
		s.partial(w, http.StatusUnprocessableEntity, "modal-key-form", f)
		return
	}
	s.flash(w, "ok", "API key updated.")
	s.redirect(w, r, "/keys")
}

func (s *Server) keyRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RevokeAPIKey(r.Context(), r.PathValue("id")); err != nil {
		s.flash(w, "err", err.Error())
	} else {
		s.flash(w, "ok", "API key revoked. Callers using it now receive 401.")
	}
	s.redirect(w, r, "/keys")
}

func (s *Server) keyDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteAPIKey(r.Context(), r.PathValue("id")); err != nil {
		s.flash(w, "err", err.Error())
	} else {
		s.flash(w, "ok", "API key deleted.")
	}
	s.redirect(w, r, "/keys")
}

// agents (tokens + OAuth clients)

type tokenView struct {
	sqlcgen.AgentToken
	Revoked, Expired bool
	ClientName       string
	Channels         []string
}

type clientView struct {
	sqlcgen.OauthClient
	RedirectURIs []string
	TokenCount   int
}

type agentsPage struct {
	Page
	Tokens    []tokenView
	Clients   []clientView
	MCPURL    string
	BaseURL   string
	HasTokens bool
}

func (s *Server) agents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	toks, err := s.svc.ListAgentTokens(ctx)
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	clients, _ := s.svc.ListOAuthClients(ctx)
	clientNames := map[string]string{}
	tokenCounts := map[string]int{}
	for _, c := range clients {
		clientNames[c.ID] = c.Name
	}
	chans, _ := s.svc.ListOpenChannels(ctx)
	byToken := map[string][]string{}
	for _, c := range chans {
		if c.AgentTokenID != nil {
			byToken[*c.AgentTokenID] = append(byToken[*c.AgentTokenID], c.Name)
		}
	}
	now := time.Now().UnixMilli()
	p := agentsPage{Page: s.page(r, "Agents", "agents"), MCPURL: s.baseURL(r) + "/mcp", BaseURL: s.baseURL(r)}
	for _, t := range toks {
		v := tokenView{AgentToken: t, Revoked: t.RevokedAt != nil, Expired: t.ExpiresAt != nil && *t.ExpiresAt < now, Channels: byToken[t.ID]}
		if t.OauthClientID != nil {
			v.ClientName = clientNames[*t.OauthClientID]
			tokenCounts[*t.OauthClientID]++
		}
		if !v.Revoked && !v.Expired {
			p.HasTokens = true
		}
		p.Tokens = append(p.Tokens, v)
	}
	for _, c := range clients {
		p.Clients = append(p.Clients, clientView{OauthClient: c, RedirectURIs: core.ClientRedirectURIs(c), TokenCount: tokenCounts[c.ID]})
	}
	s.render(w, r, "agents", http.StatusOK, p)
}

func (s *Server) tokenNewForm(w http.ResponseWriter, r *http.Request) {
	s.partial(w, http.StatusOK, "modal-token-form", map[string]string{})
}

func (s *Server) tokenCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	name := r.PostFormValue("name")
	_, plain, err := s.svc.CreateAgentToken(r.Context(), name)
	if err != nil {
		s.partial(w, http.StatusUnprocessableEntity, "modal-token-form", map[string]string{"Name": name, "Error": err.Error()})
		return
	}
	s.partial(w, http.StatusOK, "modal-token-created", map[string]string{"Token": plain, "MCPURL": s.baseURL(r) + "/mcp"})
}

func (s *Server) tokenRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RevokeAgentToken(r.Context(), r.PathValue("id")); err != nil {
		s.flash(w, "err", err.Error())
	} else {
		s.flash(w, "ok", "Token revoked.")
	}
	s.redirect(w, r, "/agents")
}

func (s *Server) tokenDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteAgentToken(r.Context(), r.PathValue("id")); err != nil {
		s.flash(w, "err", err.Error())
	} else {
		s.flash(w, "ok", "Token deleted.")
	}
	s.redirect(w, r, "/agents")
}

func (s *Server) clientDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteOAuthClient(r.Context(), r.PathValue("id")); err != nil {
		s.flash(w, "err", err.Error())
	} else {
		s.flash(w, "ok", "OAuth client removed. Its tokens stay valid until revoked or expired.")
	}
	s.redirect(w, r, "/agents")
}

// events

func eventFilter(r *http.Request) core.EventFilter {
	q := r.URL.Query()
	return core.EventFilter{Kind: q.Get("kind"), Level: q.Get("level"), RequestID: q.Get("request_id"), ChannelID: q.Get("channel_id"), APIKeyID: q.Get("api_key_id"), Search: q.Get("q")}
}

func (s *Server) eventTable(ctx context.Context, r *http.Request, f core.EventFilter) (EventTable, error) {
	page := pageParam(r)
	f.Limit = 100
	f.Offset = int64((page - 1) * 100)
	rows, total, err := s.svc.ListEvents(ctx, f)
	if err != nil {
		return EventTable{}, err
	}
	views := make([]EventView, 0, len(rows))
	for _, e := range rows {
		views = append(views, eventView(e))
	}
	q := cloneValues(r.URL.Query())
	return EventTable{Rows: views, Pager: newPager("/events", q, page, 100, total), ID: "events-table", URL: "/events/table?" + q.Encode()}, nil
}

type eventsPage struct {
	Page
	Table  EventTable
	Filter core.EventFilter
	Kinds  []string
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	f := eventFilter(r)
	table, err := s.eventTable(r.Context(), r, f)
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	p := eventsPage{Page: s.page(r, "Events", "events"), Table: table, Filter: f}
	p.Kinds, _ = s.svc.EventKinds(r.Context())
	s.render(w, r, "events", http.StatusOK, p)
}

func (s *Server) eventsTable(w http.ResponseWriter, r *http.Request) {
	table, err := s.eventTable(r.Context(), r, eventFilter(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.partial(w, http.StatusOK, "event-table", table)
}

// settings

type settingsPage struct {
	Page
	Defs        []core.SettingDef
	Values      map[string]string
	HasPassword bool
	DBPath      string
	Uptime      string
	GoVersion   string
	LiveCount   int
	BaseURL     string
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	p := settingsPage{Page: s.page(r, "Settings", "settings"), Defs: core.SettingDefs, Values: s.svc.SettingValues(), HasPassword: s.svc.HasPassword(), DBPath: s.DBPath, Uptime: s.svc.Uptime().String(), GoVersion: runtime.Version(), LiveCount: s.svc.LiveCount(), BaseURL: s.baseURL(r)}
	s.render(w, r, "settings", http.StatusOK, p)
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	var errs []string
	for _, d := range core.SettingDefs {
		if _, ok := r.PostForm[d.Key]; !ok && d.Type != "bool" {
			continue
		}
		val := strings.TrimSpace(r.PostFormValue(d.Key))
		if d.Type == "bool" {
			if val == "" {
				val = "false"
			} else {
				val = "true"
			}
		}
		if val == s.svc.SettingValues()[d.Key] {
			continue
		}
		if err := s.svc.SetSetting(r.Context(), d.Key, val); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		s.flash(w, "err", strings.Join(errs, "; "))
	} else {
		s.flash(w, "ok", "Settings saved.")
	}
	s.redirect(w, r, "/settings")
}

func (s *Server) settingsPassword(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	pw, confirm := r.PostFormValue("password"), r.PostFormValue("confirm")
	if pw != confirm {
		s.flash(w, "err", "Passwords do not match.")
		s.redirect(w, r, "/settings")
		return
	}
	if err := s.svc.SetPassword(r.Context(), pw); err != nil {
		s.flash(w, "err", err.Error())
	} else {
		s.flash(w, "ok", "Password updated.")
	}
	s.redirect(w, r, "/settings")
}

func (s *Server) settingsRevokeSessions(w http.ResponseWriter, r *http.Request) {
	n, _ := s.svc.RevokeAllSessions(r.Context())
	s.clearSession(w)
	s.flash(w, "ok", fmt.Sprintf("Signed out %d session(s).", n))
	s.redirect(w, r, "/login")
}

func (s *Server) settingsPrune(w http.ResponseWriter, r *http.Request) {
	s.svc.Prune(r.Context())
	s.flash(w, "ok", "Retention cleanup ran.")
	s.redirect(w, r, "/settings")
}

// docs

type docsPage struct {
	Page
	BaseURL, OpenAIURL, MCPURL string
	HasKeys, HasTokens         bool
}

func (s *Server) docs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	base := s.baseURL(r)
	p := docsPage{Page: s.page(r, "Connect & docs", "docs"), BaseURL: base, OpenAIURL: base + "/v1", MCPURL: base + "/mcp"}
	if n, err := s.svc.Q.CountActiveAPIKeys(ctx); err == nil && n > 0 {
		p.HasKeys = true
	}
	if toks, err := s.svc.ListAgentTokens(ctx); err == nil {
		for _, t := range toks {
			if t.RevokedAt == nil {
				p.HasTokens = true
				break
			}
		}
	}
	s.render(w, r, "docs", http.StatusOK, p)
}
