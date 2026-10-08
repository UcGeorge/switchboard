package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/events"
	"github.com/ucgeorge/switchboard/internal/ids"
	"github.com/ucgeorge/switchboard/internal/secret"
)

// Token kinds.
const (
	TokenKindStatic = "static"
	TokenKindOAuth  = "oauth"
)

// Token errors.
var (
	ErrTokenInvalid = errors.New("invalid token")
	ErrTokenExpired = errors.New("token expired")
	ErrTokenRevoked = errors.New("token revoked")
)

// CreateAgentToken mints a static MCP bearer token. Plaintext returned once.
func (s *Service) CreateAgentToken(ctx context.Context, name string) (sqlcgen.AgentToken, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return sqlcgen.AgentToken{}, "", errors.New("name is required")
	}
	plain, hash, display := secret.NewAgentToken()
	t, err := s.Q.CreateAgentToken(ctx, sqlcgen.CreateAgentTokenParams{
		ID:          ids.New("tok"),
		Name:        name,
		Kind:        TokenKindStatic,
		TokenHash:   hash,
		TokenPrefix: display,
		Scope:       "mcp",
		CreatedAt:   nowMs(),
	})
	if err != nil {
		return sqlcgen.AgentToken{}, "", err
	}
	s.publish(events.Event{Kind: events.TokenCreated, Message: fmt.Sprintf("Agent token %q created", name), Data: map[string]any{"token_id": t.ID, "kind": TokenKindStatic}})
	return t, plain, nil
}

// GetAgentToken fetches a token by id.
func (s *Service) GetAgentToken(ctx context.Context, id string) (sqlcgen.AgentToken, error) {
	t, err := s.Q.GetAgentToken(ctx, id)
	if isNoRows(err) {
		return t, ErrNotFound
	}
	return t, err
}

// ListAgentTokens returns every token (static and OAuth-issued).
func (s *Service) ListAgentTokens(ctx context.Context) ([]sqlcgen.AgentToken, error) {
	return s.Q.ListAgentTokens(ctx)
}

// RevokeAgentToken disables a token; channels opened with it stay but can no
// longer be driven.
func (s *Service) RevokeAgentToken(ctx context.Context, id string) error {
	t, err := s.GetAgentToken(ctx, id)
	if err != nil {
		return err
	}
	n, err := s.Q.RevokeAgentToken(ctx, sqlcgen.RevokeAgentTokenParams{RevokedAt: ptr(nowMs()), ID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTokenRevoked
	}
	s.publish(events.Event{Kind: events.TokenRevoked, Level: events.LevelWarn, Message: fmt.Sprintf("Agent token %q revoked", t.Name), Data: map[string]any{"token_id": id}})
	return nil
}

// DeleteAgentToken removes a token record.
func (s *Service) DeleteAgentToken(ctx context.Context, id string) error {
	n, err := s.Q.DeleteAgentToken(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthenticateAgentToken resolves a bearer token presented to the MCP
// endpoint and records its use.
func (s *Service) AuthenticateAgentToken(ctx context.Context, plaintext string) (sqlcgen.AgentToken, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return sqlcgen.AgentToken{}, ErrTokenInvalid
	}
	t, err := s.Q.GetAgentTokenByHash(ctx, secret.Hash(plaintext))
	if err != nil {
		if isNoRows(err) {
			return sqlcgen.AgentToken{}, ErrTokenInvalid
		}
		return sqlcgen.AgentToken{}, err
	}
	if t.RevokedAt != nil {
		return t, ErrTokenRevoked
	}
	if t.ExpiresAt != nil && *t.ExpiresAt < nowMs() {
		return t, ErrTokenExpired
	}
	// Throttle last_used_at writes to once a minute per token.
	if t.LastUsedAt == nil || nowMs()-*t.LastUsedAt > 60_000 {
		_ = s.Q.TouchAgentToken(ctx, sqlcgen.TouchAgentTokenParams{LastUsedAt: ptr(nowMs()), ID: t.ID})
	}
	return t, nil
}

// OAuth 2.1 (authorization server for MCP clients)

// OAuthClientInput is the dynamic client registration payload (RFC 7591).
type OAuthClientInput struct {
	Name                    string
	RedirectURIs            []string
	GrantTypes              []string
	TokenEndpointAuthMethod string
	Metadata                map[string]any
}

// OAuth errors map onto RFC 6749 error codes via Code.
type OAuthError struct {
	Code        string
	Description string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }

func oauthErr(code, desc string) error { return &OAuthError{Code: code, Description: desc} }

// RegisterOAuthClient stores a dynamically registered client.
func (s *Service) RegisterOAuthClient(ctx context.Context, in OAuthClientInput) (sqlcgen.OauthClient, string, error) {
	if len(in.RedirectURIs) == 0 {
		return sqlcgen.OauthClient{}, "", oauthErr("invalid_redirect_uri", "redirect_uris is required")
	}
	for _, u := range in.RedirectURIs {
		parsed, err := url.Parse(u)
		if err != nil || parsed.Scheme == "" || parsed.Fragment != "" || parsed.User != nil || ((parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host == "") || parsed.Scheme == "javascript" || parsed.Scheme == "data" {
			return sqlcgen.OauthClient{}, "", oauthErr("invalid_redirect_uri", "redirect_uri must be an absolute URI: "+u)
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "MCP client"
	}
	method := in.TokenEndpointAuthMethod
	if method == "" {
		method = "none"
	}
	grants := in.GrantTypes
	if len(grants) == 0 {
		grants = []string{"authorization_code", "refresh_token"}
	}
	var secretPlain string
	var secretHash *string
	if method != "none" {
		secretPlain = secret.Random(48)
		secretHash = ptr(secret.Hash(secretPlain))
	}
	uris, _ := json.Marshal(in.RedirectURIs)
	meta, _ := json.Marshal(in.Metadata)
	if in.Metadata == nil {
		meta = []byte("{}")
	}
	c, err := s.Q.CreateOAuthClient(ctx, sqlcgen.CreateOAuthClientParams{
		ID:                      ids.New("cli"),
		SecretHash:              secretHash,
		Name:                    name,
		RedirectUris:            string(uris),
		GrantTypes:              JoinList(grants),
		TokenEndpointAuthMethod: method,
		Metadata:                string(meta),
		CreatedAt:               nowMs(),
	})
	if err != nil {
		return sqlcgen.OauthClient{}, "", err
	}
	s.publish(events.Event{Kind: events.OAuthClientRegistered, Message: fmt.Sprintf("OAuth client %q registered", name), Data: map[string]any{"client_id": c.ID, "redirect_uris": in.RedirectURIs}})
	return c, secretPlain, nil
}

// GetOAuthClient fetches a client.
func (s *Service) GetOAuthClient(ctx context.Context, id string) (sqlcgen.OauthClient, error) {
	c, err := s.Q.GetOAuthClient(ctx, id)
	if isNoRows(err) {
		return c, ErrNotFound
	}
	return c, err
}

// ListOAuthClients returns all registered clients.
func (s *Service) ListOAuthClients(ctx context.Context) ([]sqlcgen.OauthClient, error) {
	return s.Q.ListOAuthClients(ctx)
}

// DeleteOAuthClient removes a client registration.
func (s *Service) DeleteOAuthClient(ctx context.Context, id string) error {
	n, err := s.Q.DeleteOAuthClient(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClientRedirectURIs decodes a client's registered redirect URIs.
func ClientRedirectURIs(c sqlcgen.OauthClient) []string {
	var out []string
	_ = json.Unmarshal([]byte(c.RedirectUris), &out)
	return out
}

// ClientAllowsRedirect checks an exact match against registered URIs.
func ClientAllowsRedirect(c sqlcgen.OauthClient, uri string) bool {
	for _, u := range ClientRedirectURIs(c) {
		if u == uri {
			return true
		}
	}
	return false
}

// VerifyOAuthClientSecret checks a confidential client's secret.
func VerifyOAuthClientSecret(c sqlcgen.OauthClient, presented string) bool {
	if c.TokenEndpointAuthMethod == "none" {
		return true
	}
	if c.SecretHash == nil || presented == "" {
		return false
	}
	return secret.Equal(*c.SecretHash, secret.Hash(presented))
}

// IssueAuthCode creates a short-lived authorization code after user consent.
func (s *Service) IssueAuthCode(ctx context.Context, clientID, redirectURI, challenge, method, scope, resource string) (string, error) {
	if challenge == "" {
		return "", oauthErr("invalid_request", "code_challenge is required (PKCE)")
	}
	if method == "" {
		method = "S256"
	}
	if method != "S256" {
		return "", oauthErr("invalid_request", "only S256 code_challenge_method is supported")
	}
	code := secret.RandomURLSafe(32)
	now := nowMs()
	err := s.Q.CreateOAuthCode(ctx, sqlcgen.CreateOAuthCodeParams{
		CodeHash:            secret.Hash(code),
		ClientID:            clientID,
		RedirectUri:         redirectURI,
		CodeChallenge:       challenge,
		CodeChallengeMethod: method,
		Scope:               scope,
		Resource:            resource,
		ExpiresAt:           now + int64((10 * time.Minute).Milliseconds()),
		CreatedAt:           now,
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// TokenPair is an issued access/refresh token set.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
	Scope        string
	TokenID      string
}

// ExchangeAuthCode implements grant_type=authorization_code with PKCE.
func (s *Service) ExchangeAuthCode(ctx context.Context, code, clientID, redirectURI, verifier string) (TokenPair, error) {
	rec, err := s.Q.GetOAuthCode(ctx, secret.Hash(code))
	if err != nil {
		if isNoRows(err) {
			return TokenPair{}, oauthErr("invalid_grant", "unknown authorization code")
		}
		return TokenPair{}, err
	}
	if rec.ClientID != clientID {
		return TokenPair{}, oauthErr("invalid_grant", "code was issued to a different client")
	}
	if rec.RedirectUri != redirectURI {
		return TokenPair{}, oauthErr("invalid_grant", "redirect_uri mismatch")
	}
	if !secret.PKCEVerify(verifier, rec.CodeChallenge, rec.CodeChallengeMethod) {
		return TokenPair{}, oauthErr("invalid_grant", "PKCE verification failed")
	}
	now := nowMs()
	n, err := s.Q.ConsumeOAuthCode(ctx, sqlcgen.ConsumeOAuthCodeParams{UsedAt: ptr(now), CodeHash: rec.CodeHash, ExpiresAt: now})
	if err != nil {
		return TokenPair{}, err
	}
	if n == 0 {
		return TokenPair{}, oauthErr("invalid_grant", "authorization code expired or already used")
	}
	client, err := s.GetOAuthClient(ctx, clientID)
	if err != nil {
		return TokenPair{}, oauthErr("invalid_client", "unknown client")
	}
	return s.issueOAuthTokens(ctx, client, rec.Scope, "")
}

// RefreshOAuthToken implements grant_type=refresh_token with rotation.
func (s *Service) RefreshOAuthToken(ctx context.Context, refreshToken, clientID string) (TokenPair, error) {
	t, err := s.Q.GetAgentTokenByRefreshHash(ctx, strp(secret.Hash(refreshToken)))
	if err != nil {
		if isNoRows(err) {
			return TokenPair{}, oauthErr("invalid_grant", "unknown refresh token")
		}
		return TokenPair{}, err
	}
	if t.RevokedAt != nil {
		return TokenPair{}, oauthErr("invalid_grant", "token revoked")
	}
	if clientID == "" || deref(t.OauthClientID) != clientID {
		return TokenPair{}, oauthErr("invalid_grant", "refresh token belongs to a different client")
	}
	// Refresh tokens expire relative to issuance.
	if nowMs()-t.CreatedAt > s.Settings().OAuthRefreshTTL.Milliseconds() {
		return TokenPair{}, oauthErr("invalid_grant", "refresh token expired")
	}
	client, err := s.GetOAuthClient(ctx, deref(t.OauthClientID))
	if err != nil {
		return TokenPair{}, oauthErr("invalid_client", "unknown client")
	}
	return s.issueOAuthTokens(ctx, client, t.Scope, t.ID)
}

func (s *Service) issueOAuthTokens(ctx context.Context, client sqlcgen.OauthClient, scope, rotateID string) (TokenPair, error) {
	if scope == "" {
		scope = "mcp"
	}
	access, accessHash, display := secret.NewToken(secret.OAuthTokenPrefix, 40)
	refresh, refreshHash, _ := secret.NewToken(secret.RefreshPrefix, 40)
	ttl := s.Settings().OAuthAccessTTL
	expires := nowMs() + ttl.Milliseconds()
	var tokenID string
	if rotateID != "" {
		if err := s.Q.RotateAgentToken(ctx, sqlcgen.RotateAgentTokenParams{
			TokenHash: accessHash, TokenPrefix: display, RefreshTokenHash: ptr(refreshHash), ExpiresAt: ptr(expires), ID: rotateID,
		}); err != nil {
			return TokenPair{}, err
		}
		tokenID = rotateID
	} else {
		t, err := s.Q.CreateAgentToken(ctx, sqlcgen.CreateAgentTokenParams{
			ID:               ids.New("tok"),
			Name:             client.Name,
			Kind:             TokenKindOAuth,
			TokenHash:        accessHash,
			TokenPrefix:      display,
			RefreshTokenHash: ptr(refreshHash),
			OauthClientID:    ptr(client.ID),
			Scope:            scope,
			ExpiresAt:        ptr(expires),
			CreatedAt:        nowMs(),
		})
		if err != nil {
			return TokenPair{}, err
		}
		tokenID = t.ID
	}
	s.publish(events.Event{Kind: events.OAuthTokenIssued, Message: fmt.Sprintf("OAuth token issued to %q", client.Name), Data: map[string]any{"client_id": client.ID, "token_id": tokenID, "rotated": rotateID != ""}})
	return TokenPair{AccessToken: access, RefreshToken: refresh, ExpiresIn: int64(ttl.Seconds()), Scope: scope, TokenID: tokenID}, nil
}
