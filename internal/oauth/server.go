// Package oauth implements the OAuth 2.1 authorization server that MCP
// clients use to obtain bearer tokens: RFC 8414 / RFC 9728 metadata,
// RFC 7591 dynamic client registration, PKCE authorization code and refresh
// token grants, and RFC 7009 revocation.
package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/events"
	"github.com/ucgeorge/switchboard/internal/secret"
)

// Server serves the OAuth endpoints.
type Server struct {
	svc            *core.Service
	log            *slog.Logger
	requireSession func(http.Handler) http.Handler
	consent        *template.Template
}

// New builds the server. requireSession guards the consent screen with the
// dashboard login (provided by the web package).
func New(svc *core.Service, log *slog.Logger, requireSession func(http.Handler) http.Handler) *Server {
	return &Server{svc: svc, log: log, requireSession: requireSession, consent: template.Must(template.New("consent").Parse(consentHTML))}
}

// Register mounts the endpoints on mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.Handle("GET /.well-known/oauth-protected-resource", cors(http.HandlerFunc(s.protectedResource)))
	mux.Handle("GET /.well-known/oauth-protected-resource/{rest...}", cors(http.HandlerFunc(s.protectedResource)))
	mux.Handle("GET /.well-known/oauth-authorization-server", cors(http.HandlerFunc(s.metadata)))
	mux.Handle("GET /.well-known/oauth-authorization-server/{rest...}", cors(http.HandlerFunc(s.metadata)))
	mux.Handle("GET /.well-known/openid-configuration", cors(http.HandlerFunc(s.metadata)))
	mux.Handle("POST /oauth/register", cors(http.HandlerFunc(s.register)))
	mux.Handle("POST /oauth/token", cors(http.HandlerFunc(s.token)))
	mux.Handle("POST /oauth/revoke", cors(http.HandlerFunc(s.revoke)))
	mux.Handle("OPTIONS /oauth/{rest...}", cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	mux.Handle("OPTIONS /.well-known/{rest...}", cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	mux.Handle("GET /oauth/authorize", s.requireSession(http.HandlerFunc(s.authorizeGet)))
	mux.Handle("POST /oauth/authorize", s.requireSession(http.HandlerFunc(s.authorizePost)))
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Protocol-Version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) base(r *http.Request) string {
	return s.svc.BaseURL(r.Host, r.TLS != nil)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// metadata

func (s *Server) protectedResource(w http.ResponseWriter, r *http.Request) {
	base := s.base(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 base + "/mcp",
		"authorization_servers":    []string{base},
		"scopes_supported":         []string{"mcp"},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            s.svc.Settings().InstanceName + " MCP",
		"resource_documentation":   base + "/docs",
	})
}

func (s *Server) metadata(w http.ResponseWriter, r *http.Request) {
	base := s.base(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                     base,
		"authorization_endpoint":                     base + "/oauth/authorize",
		"token_endpoint":                             base + "/oauth/token",
		"registration_endpoint":                      base + "/oauth/register",
		"revocation_endpoint":                        base + "/oauth/revoke",
		"response_types_supported":                   []string{"code"},
		"response_modes_supported":                   []string{"query"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      []string{"none", "client_secret_post", "client_secret_basic"},
		"revocation_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
		"scopes_supported":                           []string{"mcp"},
		"service_documentation":                      base + "/docs",
	})
}

// dynamic client registration (RFC 7591)

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientName              string         `json:"client_name"`
		RedirectURIs            []string       `json:"redirect_uris"`
		GrantTypes              []string       `json:"grant_types"`
		ResponseTypes           []string       `json:"response_types"`
		TokenEndpointAuthMethod string         `json:"token_endpoint_auth_method"`
		Scope                   string         `json:"scope"`
		ClientURI               string         `json:"client_uri"`
		LogoURI                 string         `json:"logo_uri"`
		SoftwareID              string         `json:"software_id"`
		SoftwareVersion         string         `json:"software_version"`
		Extra                   map[string]any `json:"-"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "body must be a JSON object")
		return
	}
	method := body.TokenEndpointAuthMethod
	switch method {
	case "", "none", "client_secret_post", "client_secret_basic":
	default:
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported token_endpoint_auth_method")
		return
	}
	client, secretPlain, err := s.svc.RegisterOAuthClient(r.Context(), core.OAuthClientInput{
		Name: body.ClientName, RedirectURIs: body.RedirectURIs, GrantTypes: body.GrantTypes, TokenEndpointAuthMethod: method,
		Metadata: map[string]any{"client_uri": body.ClientURI, "logo_uri": body.LogoURI, "software_id": body.SoftwareID, "software_version": body.SoftwareVersion},
	})
	if err != nil {
		var oe *core.OAuthError
		if errors.As(err, &oe) {
			writeOAuthError(w, http.StatusBadRequest, oe.Code, oe.Description)
			return
		}
		s.log.Error("register oauth client", "err", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not register client")
		return
	}
	resp := map[string]any{
		"client_id":                  client.ID,
		"client_id_issued_at":        client.CreatedAt / 1000,
		"client_name":                client.Name,
		"redirect_uris":              body.RedirectURIs,
		"grant_types":                core.SplitList(client.GrantTypes),
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": client.TokenEndpointAuthMethod,
		"scope":                      "mcp",
	}
	if secretPlain != "" {
		resp["client_secret"] = secretPlain
		resp["client_secret_expires_at"] = 0
	}
	writeJSON(w, http.StatusCreated, resp)
}

// authorization (consent)

type consentData struct {
	ClientName   string
	ClientID     string
	RedirectURI  string
	Scope        string
	State        string
	Challenge    string
	Method       string
	Resource     string
	ResponseType string
	Instance     string
	Error        string
}

func (s *Server) authorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	data, redirectErr := s.validateAuthorize(r, q.Get("client_id"), q.Get("redirect_uri"), q.Get("response_type"), q.Get("code_challenge"), q.Get("code_challenge_method"), q.Get("scope"), q.Get("state"), q.Get("resource"))
	if redirectErr != nil {
		s.failAuthorize(w, r, data, redirectErr)
		return
	}
	s.renderConsent(w, data)
}

func (s *Server) authorizePost(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	u, err := url.Parse(origin)
	if err != nil || origin == "" || u.Host != r.Host {
		http.Error(w, "cross-origin consent rejected", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f := r.PostForm
	data, redirectErr := s.validateAuthorize(r, f.Get("client_id"), f.Get("redirect_uri"), f.Get("response_type"), f.Get("code_challenge"), f.Get("code_challenge_method"), f.Get("scope"), f.Get("state"), f.Get("resource"))
	if redirectErr != nil {
		s.failAuthorize(w, r, data, redirectErr)
		return
	}
	if f.Get("decision") != "approve" {
		s.svc.Bus.Publish(events.Event{Kind: events.OAuthConsentDenied, Level: events.LevelWarn, Message: fmt.Sprintf("Consent denied for %q", data.ClientName), Data: map[string]any{"client_id": data.ClientID}})
		s.redirectWith(w, r, data.RedirectURI, url.Values{"error": {"access_denied"}, "error_description": {"The user denied the request."}, "state": {data.State}})
		return
	}
	code, err := s.svc.IssueAuthCode(r.Context(), data.ClientID, data.RedirectURI, data.Challenge, data.Method, data.Scope, data.Resource)
	if err != nil {
		var oe *core.OAuthError
		if errors.As(err, &oe) {
			s.redirectWith(w, r, data.RedirectURI, url.Values{"error": {oe.Code}, "error_description": {oe.Description}, "state": {data.State}})
			return
		}
		s.log.Error("issue auth code", "err", err)
		http.Error(w, "could not issue authorization code", http.StatusInternalServerError)
		return
	}
	s.redirectWith(w, r, data.RedirectURI, url.Values{"code": {code}, "state": {data.State}})
}

// validateAuthorize checks the request. A returned *core.OAuthError with a
// trusted redirect URI is reported to the client; otherwise it is shown to
// the user.
func (s *Server) validateAuthorize(r *http.Request, clientID, redirectURI, responseType, challenge, method, scope, state, resource string) (consentData, error) {
	data := consentData{ClientID: clientID, RedirectURI: redirectURI, Scope: scope, State: state, Challenge: challenge, Method: method, Resource: resource, ResponseType: responseType, Instance: s.svc.Settings().InstanceName}
	if scope == "" {
		data.Scope = "mcp"
	}
	if method == "" {
		data.Method = "S256"
	}
	if clientID == "" {
		data.RedirectURI = ""
		return data, &core.OAuthError{Code: "invalid_request", Description: "client_id is required"}
	}
	client, err := s.svc.GetOAuthClient(r.Context(), clientID)
	if err != nil {
		data.RedirectURI = ""
		return data, &core.OAuthError{Code: "invalid_client", Description: "unknown client_id; register the client first"}
	}
	data.ClientName = client.Name
	uris := core.ClientRedirectURIs(client)
	if redirectURI == "" && len(uris) == 1 {
		redirectURI = uris[0]
		data.RedirectURI = redirectURI
	}
	if !core.ClientAllowsRedirect(client, redirectURI) {
		data.RedirectURI = ""
		return data, &core.OAuthError{Code: "invalid_request", Description: "redirect_uri is not registered for this client"}
	}
	if responseType != "code" {
		return data, &core.OAuthError{Code: "unsupported_response_type", Description: "response_type must be 'code'"}
	}
	if challenge == "" {
		return data, &core.OAuthError{Code: "invalid_request", Description: "code_challenge is required (PKCE S256)"}
	}
	if data.Method != "S256" {
		return data, &core.OAuthError{Code: "invalid_request", Description: "code_challenge_method must be S256"}
	}
	return data, nil
}

func (s *Server) failAuthorize(w http.ResponseWriter, r *http.Request, data consentData, err error) {
	var oe *core.OAuthError
	if !errors.As(err, &oe) {
		oe = &core.OAuthError{Code: "server_error", Description: err.Error()}
	}
	if data.RedirectURI != "" {
		s.redirectWith(w, r, data.RedirectURI, url.Values{"error": {oe.Code}, "error_description": {oe.Description}, "state": {data.State}})
		return
	}
	data.Error = oe.Description
	w.WriteHeader(http.StatusBadRequest)
	s.renderConsent(w, data)
}

func (s *Server) redirectWith(w http.ResponseWriter, r *http.Request, redirectURI string, params url.Values) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	for k, vs := range params {
		for _, v := range vs {
			if v != "" {
				q.Set(k, v)
			}
		}
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (s *Server) renderConsent(w http.ResponseWriter, data consentData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.consent.Execute(w, data); err != nil {
		s.log.Error("render consent", "err", err)
	}
}

// token endpoint

func (s *Server) clientCredentials(r *http.Request) (id, secretPlain string) {
	if u, p, ok := r.BasicAuth(); ok {
		id, _ = url.QueryUnescape(u)
		secretPlain, _ = url.QueryUnescape(p)
		return id, secretPlain
	}
	return r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "body must be application/x-www-form-urlencoded")
		return
	}
	clientID, clientSecret := s.clientCredentials(r)
	if clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id is required")
		return
	}
	if clientID != "" {
		client, err := s.svc.GetOAuthClient(r.Context(), clientID)
		if err != nil {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
			return
		}
		if !core.VerifyOAuthClientSecret(client, clientSecret) {
			w.Header().Set("WWW-Authenticate", `Basic realm="switchboard"`)
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
			return
		}
	}
	var pair core.TokenPair
	var err error
	switch grant := r.PostForm.Get("grant_type"); grant {
	case "authorization_code":
		if clientID == "" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id is required")
			return
		}
		pair, err = s.svc.ExchangeAuthCode(r.Context(), r.PostForm.Get("code"), clientID, r.PostForm.Get("redirect_uri"), r.PostForm.Get("code_verifier"))
	case "refresh_token":
		pair, err = s.svc.RefreshOAuthToken(r.Context(), r.PostForm.Get("refresh_token"), clientID)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		return
	}
	if err != nil {
		var oe *core.OAuthError
		if errors.As(err, &oe) {
			status := http.StatusBadRequest
			if oe.Code == "invalid_client" {
				status = http.StatusUnauthorized
			}
			writeOAuthError(w, status, oe.Code, oe.Description)
			return
		}
		s.log.Error("token grant", "err", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  pair.AccessToken,
		"token_type":    "Bearer",
		"expires_in":    pair.ExpiresIn,
		"refresh_token": pair.RefreshToken,
		"scope":         pair.Scope,
	})
}

// revoke implements RFC 7009 for access tokens issued by this server.
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "bad form")
		return
	}
	token := r.PostForm.Get("token")
	if token == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if strings.HasPrefix(token, secret.OAuthTokenPrefix) {
		if t, err := s.svc.AuthenticateAgentToken(r.Context(), token); err == nil {
			_ = s.svc.RevokeAgentToken(r.Context(), t.ID)
		}
	}
	// RFC 7009: respond 200 even for unknown tokens.
	w.WriteHeader(http.StatusOK)
}

const consentHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Authorize · {{.Instance}}</title>
<style>
:root{color-scheme:light dark;--bg:#0b0e14;--card:#121722;--line:#232b38;--text:#e6edf3;--muted:#8b98a9;--accent:#f5b942;--accent-ink:#1a1304;--danger:#f0616d}
@media (prefers-color-scheme:light){:root{--bg:#f6f7f9;--card:#fff;--line:#e3e7ee;--text:#111827;--muted:#5b6675;--accent:#d99a12;--accent-ink:#fff}}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--text);font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Inter,Roboto,sans-serif}
.card{width:min(440px,92vw);background:var(--card);border:1px solid var(--line);border-radius:14px;padding:28px;box-shadow:0 20px 60px rgba(0,0,0,.25)}
h1{font-size:19px;margin:0 0 6px}p{margin:8px 0;color:var(--muted)}.brand{display:flex;align-items:center;gap:10px;margin-bottom:18px;font-weight:600}
.dot{width:10px;height:10px;border-radius:50%;background:var(--accent);box-shadow:0 0 12px var(--accent)}
dl{display:grid;grid-template-columns:auto 1fr;gap:6px 14px;margin:16px 0;font-size:13px}dt{color:var(--muted)}dd{margin:0;word-break:break-all;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}
.perm{border:1px solid var(--line);border-radius:10px;padding:12px 14px;margin:14px 0;font-size:14px}.perm b{display:block;margin-bottom:4px}
.actions{display:flex;gap:10px;margin-top:20px}button{flex:1;padding:11px 14px;border-radius:9px;border:1px solid var(--line);background:transparent;color:var(--text);font:inherit;font-weight:600;cursor:pointer}
button.primary{background:var(--accent);color:var(--accent-ink);border-color:var(--accent)}.err{border:1px solid var(--danger);color:var(--danger);border-radius:10px;padding:10px 12px;margin:12px 0;font-size:14px}
</style></head><body>
<form class="card" method="post" action="/oauth/authorize">
 <div class="brand"><span class="dot"></span>{{.Instance}}</div>
 {{if .Error}}<h1>Authorization request rejected</h1><div class="err">{{.Error}}</div><p>Fix the client configuration and try again.</p>
 {{else}}
 <h1>Allow <strong>{{.ClientName}}</strong> to serve requests?</h1>
 <p>This MCP client will be able to claim queued requests, stream answers back to callers, and manage its own channel.</p>
 <div class="perm"><b>Scope: {{.Scope}}</b>Claim and answer requests · open/close channels · read queue status</div>
 <dl><dt>Client</dt><dd>{{.ClientID}}</dd><dt>Redirect</dt><dd>{{.RedirectURI}}</dd></dl>
 <input type="hidden" name="client_id" value="{{.ClientID}}"><input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
 <input type="hidden" name="response_type" value="{{.ResponseType}}"><input type="hidden" name="scope" value="{{.Scope}}">
 <input type="hidden" name="state" value="{{.State}}"><input type="hidden" name="code_challenge" value="{{.Challenge}}">
 <input type="hidden" name="code_challenge_method" value="{{.Method}}"><input type="hidden" name="resource" value="{{.Resource}}">
 <div class="actions"><button type="submit" name="decision" value="deny">Deny</button><button type="submit" name="decision" value="approve" class="primary" autofocus>Allow</button></div>
 {{end}}
</form></body></html>`
