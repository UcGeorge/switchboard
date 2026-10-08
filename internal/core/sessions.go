package core

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/secret"
)

// loginAttemptsPerMinute bounds password guesses per client IP.
const loginAttemptsPerMinute = 10

// HasPassword reports whether a dashboard password is configured.
func (s *Service) HasPassword() bool {
	v, ok := s.RawSetting(KeyPasswordHash)
	return ok && v != ""
}

// SetPassword stores a new dashboard password.
func (s *Service) SetPassword(ctx context.Context, password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	return s.setRawSetting(ctx, KeyPasswordHash, secret.HashPassword(password))
}

// EnsurePassword generates and stores a random password when none exists.
// The plaintext is returned only in that case so it can be shown once.
func (s *Service) EnsurePassword(ctx context.Context) (string, error) {
	if pw := os.Getenv("SWITCHBOARD_ADMIN_PASSWORD"); pw != "" {
		return "", s.SetPassword(ctx, pw)
	}
	if s.HasPassword() {
		return "", nil
	}
	pw := secret.Random(20)
	if err := s.SetPassword(ctx, pw); err != nil {
		return "", err
	}
	return pw, nil
}

// VerifyPassword checks a dashboard password.
func (s *Service) VerifyPassword(password string) bool {
	hash, ok := s.RawSetting(KeyPasswordHash)
	if !ok || hash == "" {
		return false
	}
	return secret.VerifyPassword(hash, password)
}

// LoginAllowed rate-limits login attempts per client address.
func (s *Service) LoginAllowed(clientIP string) (bool, time.Duration) {
	ok, _, retry := s.loginLimiter.Allow("login:"+clientIP, loginAttemptsPerMinute)
	return ok, retry
}

// CreateSession issues a dashboard session token (stored hashed).
func (s *Service) CreateSession(ctx context.Context, userAgent string) (string, error) {
	token := secret.RandomURLSafe(32)
	now := nowMs()
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	err := s.Q.CreateSession(ctx, sqlcgen.CreateSessionParams{
		ID:        secret.Hash(token),
		CreatedAt: now,
		ExpiresAt: now + s.Settings().SessionTTL.Milliseconds(),
		UserAgent: userAgent,
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// ValidateSession reports whether a session token is live.
func (s *Service) ValidateSession(ctx context.Context, token string) bool {
	if token == "" {
		return false
	}
	_, err := s.Q.GetSession(ctx, sqlcgen.GetSessionParams{ID: secret.Hash(token), ExpiresAt: nowMs()})
	return err == nil
}

// DeleteSession logs a session out.
func (s *Service) DeleteSession(ctx context.Context, token string) error {
	return s.Q.DeleteSession(ctx, secret.Hash(token))
}

// RevokeAllSessions logs every dashboard session out.
func (s *Service) RevokeAllSessions(ctx context.Context) (int64, error) {
	return s.Q.DeleteAllSessions(ctx)
}

// CreateLoginToken mints a one-time magic-link token.
func (s *Service) CreateLoginToken(ctx context.Context, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	token := secret.RandomURLSafe(32)
	if err := s.Q.CreateLoginToken(ctx, sqlcgen.CreateLoginTokenParams{TokenHash: secret.Hash(token), ExpiresAt: nowMs() + ttl.Milliseconds()}); err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeLoginToken redeems a magic-link token exactly once.
func (s *Service) ConsumeLoginToken(ctx context.Context, token string) bool {
	if token == "" {
		return false
	}
	now := nowMs()
	n, err := s.Q.ConsumeLoginToken(ctx, sqlcgen.ConsumeLoginTokenParams{UsedAt: ptr(now), TokenHash: secret.Hash(token), ExpiresAt: now})
	return err == nil && n == 1
}

// LoginLinkURL renders a magic-link URL for a token.
func (s *Service) LoginLinkURL(token string) string {
	return s.LocalURL() + "/login?token=" + token
}
