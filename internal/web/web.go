// Package web is the operator dashboard: server-rendered HTML with HTMX for
// partial updates and an SSE feed for live refresh.
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/events"
	"github.com/ucgeorge/switchboard/internal/version"
)

//go:embed templates static
var assets embed.FS

const (
	sessionCookie = "sb_session"
	flashCookie   = "sb_flash"
)

// Server serves the dashboard.
type Server struct {
	svc      *core.Service
	log      *slog.Logger
	pages    map[string]*template.Template
	partials *template.Template
	login    *template.Template
	DBPath   string
}

var pageNames = []string{"overview", "requests", "request", "conversations", "conversation", "channels", "channel", "keys", "agents", "events", "settings", "docs", "error"}

// New parses templates and builds the server.
func New(svc *core.Service, log *slog.Logger, dbPath string) (*Server, error) {
	s := &Server{svc: svc, log: log, pages: map[string]*template.Template{}, DBPath: dbPath}
	funcs := funcMap()
	partials, err := template.New("partials").Funcs(funcs).ParseFS(assets, "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse partials: %w", err)
	}
	s.partials = partials
	for _, name := range pageNames {
		t, err := template.New(name).Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/partials/*.html", "templates/pages/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse page %s: %w", name, err)
		}
		s.pages[name] = t
	}
	login, err := template.New("login").Funcs(funcs).ParseFS(assets, "templates/partials/icons.html", "templates/pages/login.html")
	if err != nil {
		return nil, fmt.Errorf("parse login: %w", err)
	}
	s.login = login
	return s, nil
}

// Register mounts the dashboard routes.
func (s *Server) Register(mux *http.ServeMux) {
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheControl(http.FileServerFS(static))))
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /login", s.loginGet)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.Handle("POST /logout", s.RequireSession(s.csrf(s.logout)))

	get := func(pattern string, h http.HandlerFunc) { mux.Handle("GET "+pattern, s.RequireSession(h)) }
	post := func(pattern string, h http.HandlerFunc) { mux.Handle("POST "+pattern, s.RequireSession(s.csrf(h))) }

	get("/{$}", s.overview)
	get("/live", s.live)
	get("/partials/stats", s.statsPartial)

	get("/requests", s.requests)
	get("/requests/table", s.requestsTable)
	get("/requests/{id}", s.request)
	post("/requests/{id}/cancel", s.requestCancel)
	post("/requests/{id}/requeue", s.requestRequeue)
	get("/requests/{id}/answer", s.requestAnswerForm)
	post("/requests/{id}/answer", s.requestAnswer)

	get("/conversations", s.conversations)
	get("/conversations/{id}", s.conversation)
	post("/conversations/{id}/delete", s.conversationDelete)

	get("/channels", s.channels)
	get("/channels/{id}", s.channel)
	get("/channels/{id}/edit", s.channelEditForm)
	post("/channels/{id}/edit", s.channelEdit)
	post("/channels/{id}/drain", s.channelDrain)
	post("/channels/{id}/resume", s.channelResume)
	post("/channels/{id}/close", s.channelClose)
	post("/channels/{id}/delete", s.channelDelete)

	get("/keys", s.keys)
	get("/keys/new", s.keyNewForm)
	post("/keys", s.keyCreate)
	get("/keys/{id}/edit", s.keyEditForm)
	post("/keys/{id}/edit", s.keyEdit)
	post("/keys/{id}/revoke", s.keyRevoke)
	post("/keys/{id}/delete", s.keyDelete)

	get("/agents", s.agents)
	get("/agents/tokens/new", s.tokenNewForm)
	post("/agents/tokens", s.tokenCreate)
	post("/agents/tokens/{id}/revoke", s.tokenRevoke)
	post("/agents/tokens/{id}/delete", s.tokenDelete)
	post("/agents/clients/{id}/delete", s.clientDelete)

	get("/events", s.events)
	get("/events/table", s.eventsTable)

	get("/settings", s.settings)
	post("/settings", s.settingsSave)
	post("/settings/password", s.settingsPassword)
	post("/settings/sessions/revoke", s.settingsRevokeSessions)
	post("/settings/prune", s.settingsPrune)

	get("/docs", s.docs)
}

func cacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		next.ServeHTTP(w, r)
	})
}

// auth

func (s *Server) sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func isSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteLaxMode,
		MaxAge: int(s.svc.Settings().SessionTTL.Seconds()),
	})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

// RequireSession guards handlers with the dashboard login. It is exported so
// the OAuth consent screen can reuse it.
func (s *Server) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.svc.ValidateSession(r.Context(), s.sessionToken(r)) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/login?next="+url.QueryEscape(r.URL.RequestURI()))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next_ := r.URL.RequestURI()
		if r.Method != http.MethodGet {
			next_ = "/"
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next_), http.StatusSeeOther)
	})
}

func csrfFor(sessionToken string) string {
	sum := sha256.Sum256([]byte("csrf:" + sessionToken))
	return hex.EncodeToString(sum[:16])
}

func (s *Server) csrf(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := csrfFor(s.sessionToken(r))
		got := r.Header.Get("X-CSRF-Token")
		if got == "" {
			_ = r.ParseForm()
			got = r.PostFormValue("_csrf")
		}
		if got == "" || got != want {
			http.Error(w, "invalid CSRF token; reload the page and try again", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
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

func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}

type loginPage struct {
	Title, Instance, Version, Error, Next, CSRF string
	HasPassword                                 bool
}

func (s *Server) loginGet(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if tok := r.URL.Query().Get("token"); tok != "" {
		if s.svc.ConsumeLoginToken(r.Context(), tok) {
			s.startSession(w, r, "magic link")
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		s.renderLogin(w, http.StatusUnauthorized, loginPage{Error: "That login link is invalid or has expired. Generate a new one from the terminal (press l) or run `switchboard auth login-link`.", Next: next})
		return
	}
	if s.svc.ValidateSession(r.Context(), s.sessionToken(r)) {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	s.renderLogin(w, http.StatusOK, loginPage{Next: next})
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	next := safeNext(r.PostFormValue("next"))
	ip := clientIP(r)
	if ok, retry := s.svc.LoginAllowed(ip); !ok {
		s.svc.Bus.Publish(events.Event{Kind: events.AuthRateLimited, Level: events.LevelWarn, Message: "Login rate limit hit from " + ip})
		s.renderLogin(w, http.StatusTooManyRequests, loginPage{Error: fmt.Sprintf("Too many attempts. Try again in %s.", retry.Round(time.Second)), Next: next})
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
			http.Error(w, "cross-origin login rejected", http.StatusForbidden)
			return
		}
	}
	if !s.svc.VerifyPassword(r.PostFormValue("password")) {
		s.svc.Bus.Publish(events.Event{Kind: events.AuthLoginFailed, Level: events.LevelWarn, Message: "Failed dashboard login from " + ip})
		s.renderLogin(w, http.StatusUnauthorized, loginPage{Error: "Incorrect password.", Next: next})
		return
	}
	s.startSession(w, r, "password")
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, method string) {
	tok, err := s.svc.CreateSession(r.Context(), r.UserAgent())
	if err != nil {
		http.Error(w, "could not create session", http.StatusInternalServerError)
		return
	}
	s.setSession(w, r, tok)
	s.svc.Bus.Publish(events.Event{Kind: events.AuthLogin, Message: "Dashboard login via " + method + " from " + clientIP(r)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	_ = s.svc.DeleteSession(r.Context(), s.sessionToken(r))
	s.clearSession(w)
	s.svc.Bus.Publish(events.Event{Kind: events.AuthLogout, Message: "Dashboard logout"})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) renderLogin(w http.ResponseWriter, status int, p loginPage) {
	p.Title = "Sign in"
	p.Instance = s.svc.Settings().InstanceName
	p.Version = version.Version
	p.HasPassword = s.svc.HasPassword()
	var buf bytes.Buffer
	if err := s.login.ExecuteTemplate(&buf, "login", p); err != nil {
		s.log.Error("render login", "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// rendering

func (s *Server) page(r *http.Request, title, nav string) Page {
	p := Page{Title: title, Nav: nav, Instance: s.svc.Settings().InstanceName, Version: version.Version, CSRF: csrfFor(s.sessionToken(r)), Path: r.URL.Path, Now: time.Now()}
	q, i, o, err := s.svc.QuickCounts(r.Context())
	if err == nil {
		p.Counts = Counts{Queued: q, InFlight: i, Online: o}
	}
	if c, err := r.Cookie(flashCookie); err == nil && c.Value != "" {
		if v, err := url.QueryUnescape(c.Value); err == nil {
			kind, msg, _ := strings.Cut(v, ":")
			p.Flash = &Flash{Kind: kind, Message: msg}
		}
	}
	return p
}

func (s *Server) flash(w http.ResponseWriter, kind, msg string) {
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: url.QueryEscape(kind + ":" + msg), Path: "/", MaxAge: 15, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func clearFlash(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1})
}

// render writes a page. HTMX requests for pages that define a "body" block
// get only that block (used for live refresh of the current page).
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, status int, data any) {
	t, ok := s.pages[name]
	if !ok {
		http.Error(w, "unknown page "+name, http.StatusInternalServerError)
		return
	}
	tmpl := "layout"
	if r.Header.Get("HX-Request") == "true" && t.Lookup("body") != nil {
		tmpl = "body"
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, tmpl, data); err != nil {
		s.log.Error("render page", "page", name, "err", err)
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if tmpl == "layout" {
		clearFlash(w)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) partial(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.partials.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("render partial", "partial", name, "err", err)
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// redirect sends the browser elsewhere after an action, HTMX-aware.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, status int, msg string) {
	type errPage struct {
		Page
		Status  int
		Message string
	}
	s.render(w, r, "error", status, errPage{Page: s.page(r, http.StatusText(status), ""), Status: status, Message: msg})
}

// live feed

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	sub, unsub := s.svc.Bus.Subscribe(512)
	defer unsub()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case e, ok := <-sub:
			if !ok {
				return
			}
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: event\ndata: %s\n\n", b)
			flusher.Flush()
		}
	}
}

func (s *Server) statsPartial(w http.ResponseWriter, r *http.Request) {
	q, i, o, _ := s.svc.QuickCounts(r.Context())
	s.partial(w, http.StatusOK, "stats-strip", Counts{Queued: q, InFlight: i, Online: o})
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	q, i, o, err := s.svc.QuickCounts(ctx)
	status := "ok"
	code := http.StatusOK
	if err != nil {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": status, "version": version.Version, "uptime_s": int(s.svc.Uptime().Seconds()),
		"queued": q, "in_flight": i, "channels_online": o,
	})
}

// small helpers

func (s *Server) names(ctx context.Context) nameMaps {
	n := nameMaps{keys: map[string]string{}, channels: map[string]string{}, tokens: map[string]string{}}
	if keys, err := s.svc.ListAPIKeys(ctx); err == nil {
		for _, k := range keys {
			n.keys[k.ID] = k.Name
		}
	}
	if chans, err := s.svc.ListChannels(ctx); err == nil {
		for _, c := range chans {
			n.channels[c.ID] = c.Name
		}
	}
	if toks, err := s.svc.ListAgentTokens(ctx); err == nil {
		for _, t := range toks {
			n.tokens[t.ID] = t.Name
		}
	}
	return n
}

func pageParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if n < 1 {
		n = 1
	}
	return n
}

func formInt(r *http.Request, key string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(r.PostFormValue(key)), 10, 64)
	return n
}

func (s *Server) baseURL(r *http.Request) string {
	return s.svc.BaseURL(r.Host, isSecure(r))
}
