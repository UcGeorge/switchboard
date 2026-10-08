package core

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/events"
	"github.com/ucgeorge/switchboard/internal/ids"
	"github.com/ucgeorge/switchboard/internal/secret"
)

// APIKeyInput holds the editable fields of an API key.
type APIKeyInput struct {
	Name            string
	RateLimitRPM    int64
	AllowedModels   []string
	PinnedChannelID string
	TimeoutSeconds  int64
	Priority        int64
}

// Key errors.
var (
	ErrKeyRevoked = errors.New("api key revoked")
	ErrKeyInvalid = errors.New("invalid api key")
)

// CreateAPIKey mints a key. The plaintext is returned exactly once.
func (s *Service) CreateAPIKey(ctx context.Context, in APIKeyInput) (sqlcgen.ApiKey, string, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return sqlcgen.ApiKey{}, "", errors.New("name is required")
	}
	plain, hash, display := secret.NewAPIKey()
	k, err := s.Q.CreateAPIKey(ctx, sqlcgen.CreateAPIKeyParams{
		ID:              ids.New("key"),
		Name:            in.Name,
		KeyHash:         hash,
		KeyPrefix:       display,
		RateLimitRpm:    max(in.RateLimitRPM, 0),
		AllowedModels:   JoinList(in.AllowedModels),
		PinnedChannelID: nilIfEmpty(in.PinnedChannelID),
		TimeoutSeconds:  max(in.TimeoutSeconds, 0),
		Priority:        in.Priority,
		CreatedAt:       nowMs(),
	})
	if err != nil {
		return sqlcgen.ApiKey{}, "", err
	}
	s.publish(events.Event{Kind: events.KeyCreated, APIKeyID: k.ID, Message: fmt.Sprintf("API key %q created", k.Name), Data: map[string]any{"name": k.Name, "prefix": k.KeyPrefix}})
	return k, plain, nil
}

// GetAPIKey fetches a key by id.
func (s *Service) GetAPIKey(ctx context.Context, id string) (sqlcgen.ApiKey, error) {
	k, err := s.Q.GetAPIKey(ctx, id)
	if isNoRows(err) {
		return k, ErrNotFound
	}
	return k, err
}

// ListAPIKeys returns all keys, newest first.
func (s *Service) ListAPIKeys(ctx context.Context) ([]sqlcgen.ApiKey, error) {
	return s.Q.ListAPIKeys(ctx)
}

// UpdateAPIKey edits a key's settings.
func (s *Service) UpdateAPIKey(ctx context.Context, id string, in APIKeyInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return errors.New("name is required")
	}
	if err := s.Q.UpdateAPIKey(ctx, sqlcgen.UpdateAPIKeyParams{
		Name:            in.Name,
		RateLimitRpm:    max(in.RateLimitRPM, 0),
		AllowedModels:   JoinList(in.AllowedModels),
		PinnedChannelID: nilIfEmpty(in.PinnedChannelID),
		TimeoutSeconds:  max(in.TimeoutSeconds, 0),
		Priority:        in.Priority,
		ID:              id,
	}); err != nil {
		return err
	}
	s.publish(events.Event{Kind: events.KeyUpdated, APIKeyID: id, Message: fmt.Sprintf("API key %q updated", in.Name)})
	return nil
}

// RevokeAPIKey disables a key; existing requests are unaffected.
func (s *Service) RevokeAPIKey(ctx context.Context, id string) error {
	k, err := s.GetAPIKey(ctx, id)
	if err != nil {
		return err
	}
	n, err := s.Q.RevokeAPIKey(ctx, sqlcgen.RevokeAPIKeyParams{RevokedAt: ptr(nowMs()), ID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrKeyRevoked
	}
	s.publish(events.Event{Kind: events.KeyRevoked, Level: events.LevelWarn, APIKeyID: id, Message: fmt.Sprintf("API key %q revoked", k.Name)})
	return nil
}

// DeleteAPIKey removes a key record entirely (history keeps the id).
func (s *Service) DeleteAPIKey(ctx context.Context, id string) error {
	n, err := s.Q.DeleteAPIKey(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthenticateAPIKey resolves a plaintext key to its record.
func (s *Service) AuthenticateAPIKey(ctx context.Context, plaintext string) (sqlcgen.ApiKey, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return sqlcgen.ApiKey{}, ErrKeyInvalid
	}
	k, err := s.Q.GetAPIKeyByHash(ctx, secret.Hash(plaintext))
	if err != nil {
		if isNoRows(err) {
			return sqlcgen.ApiKey{}, ErrKeyInvalid
		}
		return sqlcgen.ApiKey{}, err
	}
	if k.RevokedAt != nil {
		return k, ErrKeyRevoked
	}
	return k, nil
}

// KeyAllowsModel reports whether a key may request the model (empty
// allowlist means any model). Patterns support '*' globs.
func KeyAllowsModel(k sqlcgen.ApiKey, model string) bool {
	allowed := SplitList(k.AllowedModels)
	if len(allowed) == 0 {
		return true
	}
	return MatchModel(allowed, model)
}

// MatchModel reports whether model matches any pattern ('*' wildcard,
// case-insensitive, glob via path.Match).
func MatchModel(patterns []string, model string) bool {
	m := strings.ToLower(model)
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if p == "*" || p == m {
			return true
		}
		if strings.ContainsAny(p, "*?[") {
			if ok, _ := path.Match(p, m); ok {
				return true
			}
		}
	}
	return false
}
