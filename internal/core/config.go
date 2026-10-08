package core

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/events"
)

// Setting keys stored in the settings table.
const (
	KeyInstanceName     = "instance_name"
	KeyPublicURL        = "public_url"
	KeyRequestTimeout   = "request_timeout_seconds"
	KeyLease            = "lease_seconds"
	KeyChannelStale     = "channel_stale_seconds"
	KeyMaxAttempts      = "max_attempts"
	KeyRetentionDays    = "retention_days"
	KeyModels           = "models"
	KeyAcceptAnyModel   = "accept_any_model"
	KeyOAuthAccessTTL   = "oauth_access_ttl_seconds"
	KeyOAuthRefreshTTL  = "oauth_refresh_ttl_seconds"
	KeySessionTTLDays   = "session_ttl_days"
	KeyStreamKeepalive  = "stream_keepalive_seconds"
	KeyPasswordHash     = "admin.password_hash"
	KeyMaxQueueDepth    = "max_queue_depth"
	KeyDefaultRateLimit = "default_rate_limit_rpm"
)

// SettingDef describes one tunable for the dashboard and CLI.
type SettingDef struct {
	Key         string
	Label       string
	Description string
	Type        string // string | int | bool | list
	Default     string
	Secret      bool
}

// SettingDefs lists every user-editable setting in display order.
var SettingDefs = []SettingDef{
	{KeyInstanceName, "Instance name", "Shown in the dashboard header and MCP server info.", "string", "Switchboard", false},
	{KeyPublicURL, "Public URL", "External base URL when reachable through a tunnel or reverse proxy (used for OAuth issuer and MCP resource identifiers). Leave empty for localhost.", "string", "", false},
	{KeyModels, "Advertised models", "Models listed by GET /v1/models when no channel declares any. Comma separated.", "list", "switchboard/auto", false},
	{KeyAcceptAnyModel, "Accept any model", "Queue requests for models no channel has declared (they wait until a wildcard channel claims them).", "bool", "true", false},
	{KeyRequestTimeout, "Request timeout (s)", "Maximum time a caller waits for an answer before receiving 504.", "int", "600", false},
	{KeyLease, "Claim lease (s)", "How long an agent may hold a claimed request without streaming or completing before it is requeued.", "int", "120", false},
	{KeyChannelStale, "Channel stale after (s)", "A channel with no MCP activity for this long is marked offline and its in-flight requests requeued.", "int", "90", false},
	{KeyMaxAttempts, "Max attempts", "Times a request may be claimed before it fails permanently.", "int", "3", false},
	{KeyMaxQueueDepth, "Max queue depth", "Reject new requests with 429 when this many are queued (0 = unlimited).", "int", "0", false},
	{KeyDefaultRateLimit, "Default rate limit (rpm)", "Requests per minute for keys without their own limit (0 = unlimited).", "int", "0", false},
	{KeyStreamKeepalive, "Stream keepalive (s)", "Interval of SSE comments sent to streaming callers while waiting.", "int", "15", false},
	{KeyRetentionDays, "Retention (days)", "Finished requests and events older than this are pruned.", "int", "30", false},
	{KeyOAuthAccessTTL, "OAuth access token TTL (s)", "Lifetime of MCP OAuth access tokens.", "int", "86400", false},
	{KeyOAuthRefreshTTL, "OAuth refresh token TTL (s)", "Lifetime of MCP OAuth refresh tokens.", "int", "2592000", false},
	{KeySessionTTLDays, "Dashboard session TTL (days)", "How long a dashboard login stays valid.", "int", "30", false},
}

// Settings is a typed snapshot of the tunables.
type Settings struct {
	InstanceName     string
	PublicURL        string
	Models           []string
	AcceptAnyModel   bool
	RequestTimeout   time.Duration
	Lease            time.Duration
	ChannelStale     time.Duration
	MaxAttempts      int64
	MaxQueueDepth    int64
	DefaultRateLimit int64
	StreamKeepalive  time.Duration
	RetentionDays    int64
	OAuthAccessTTL   time.Duration
	OAuthRefreshTTL  time.Duration
	SessionTTL       time.Duration
}

func defaultFor(key string) string {
	for _, d := range SettingDefs {
		if d.Key == key {
			return d.Default
		}
	}
	return ""
}

func settingsFromMap(m map[string]string) Settings {
	get := func(k string) string {
		if v, ok := m[k]; ok && strings.TrimSpace(v) != "" {
			return v
		}
		return defaultFor(k)
	}
	intv := func(k string) int64 {
		n, err := strconv.ParseInt(strings.TrimSpace(get(k)), 10, 64)
		if err != nil {
			n, _ = strconv.ParseInt(defaultFor(k), 10, 64)
		}
		return n
	}
	boolv := func(k string) bool {
		v := strings.ToLower(strings.TrimSpace(get(k)))
		return v == "true" || v == "1" || v == "yes" || v == "on"
	}
	secs := func(k string) time.Duration { return time.Duration(intv(k)) * time.Second }
	return Settings{
		InstanceName:     get(KeyInstanceName),
		PublicURL:        strings.TrimRight(strings.TrimSpace(get(KeyPublicURL)), "/"),
		Models:           SplitList(get(KeyModels)),
		AcceptAnyModel:   boolv(KeyAcceptAnyModel),
		RequestTimeout:   secs(KeyRequestTimeout),
		Lease:            secs(KeyLease),
		ChannelStale:     secs(KeyChannelStale),
		MaxAttempts:      max(intv(KeyMaxAttempts), 1),
		MaxQueueDepth:    intv(KeyMaxQueueDepth),
		DefaultRateLimit: intv(KeyDefaultRateLimit),
		StreamKeepalive:  secs(KeyStreamKeepalive),
		RetentionDays:    intv(KeyRetentionDays),
		OAuthAccessTTL:   secs(KeyOAuthAccessTTL),
		OAuthRefreshTTL:  secs(KeyOAuthRefreshTTL),
		SessionTTL:       time.Duration(intv(KeySessionTTLDays)) * 24 * time.Hour,
	}
}

type settingsCache struct {
	mu   sync.RWMutex
	raw  map[string]string
	snap Settings
}

func (s *Service) reloadSettings(ctx context.Context) error {
	rows, err := s.Q.ListSettings(ctx)
	if err != nil {
		return err
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	for _, def := range SettingDefs {
		if value := os.Getenv("SWITCHBOARD_" + strings.ToUpper(def.Key)); value != "" {
			if err := ValidateSetting(def.Key, value); err != nil {
				return fmt.Errorf("environment setting %s: %w", def.Key, err)
			}
			m[def.Key] = value
		}
	}
	s.settings.mu.Lock()
	s.settings.raw = m
	s.settings.snap = settingsFromMap(m)
	s.settings.mu.Unlock()
	return nil
}

// Settings returns the current typed snapshot.
func (s *Service) Settings() Settings {
	s.settings.mu.RLock()
	defer s.settings.mu.RUnlock()
	return s.settings.snap
}

// SettingValues returns raw values with defaults filled in, for display.
func (s *Service) SettingValues() map[string]string {
	s.settings.mu.RLock()
	defer s.settings.mu.RUnlock()
	out := make(map[string]string, len(SettingDefs))
	for _, d := range SettingDefs {
		if v, ok := s.settings.raw[d.Key]; ok {
			out[d.Key] = v
		} else {
			out[d.Key] = d.Default
		}
	}
	return out
}

// RawSetting returns a stored value (including non-user-facing keys).
func (s *Service) RawSetting(key string) (string, bool) {
	s.settings.mu.RLock()
	defer s.settings.mu.RUnlock()
	v, ok := s.settings.raw[key]
	return v, ok
}

// ValidateSetting checks a value against its definition.
func ValidateSetting(key, value string) error {
	var def *SettingDef
	for i := range SettingDefs {
		if SettingDefs[i].Key == key {
			def = &SettingDefs[i]
		}
	}
	if def == nil {
		return fmt.Errorf("unknown setting %q", key)
	}
	value = strings.TrimSpace(value)
	switch def.Type {
	case "int":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("%s must be an integer", key)
		}
		if n < 0 {
			return fmt.Errorf("%s must be >= 0", key)
		}
	case "bool":
		switch strings.ToLower(value) {
		case "true", "false", "1", "0", "yes", "no", "on", "off":
		default:
			return fmt.Errorf("%s must be true or false", key)
		}
	}
	if key == KeyPublicURL && value != "" && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		return fmt.Errorf("public_url must start with http:// or https://")
	}
	return nil
}

// SetSetting validates, stores and reloads one setting.
func (s *Service) SetSetting(ctx context.Context, key, value string) error {
	if err := ValidateSetting(key, value); err != nil {
		return err
	}
	if err := s.Q.SetSetting(ctx, sqlcgen.SetSettingParams{Key: key, Value: strings.TrimSpace(value), UpdatedAt: nowMs()}); err != nil {
		return err
	}
	if err := s.reloadSettings(ctx); err != nil {
		return err
	}
	s.publish(events.Event{Kind: events.SettingChanged, Message: "Setting " + key + " updated", Data: map[string]any{"key": key, "value": value}})
	return nil
}

// setRawSetting stores an internal (non-validated) key such as the password hash.
func (s *Service) setRawSetting(ctx context.Context, key, value string) error {
	if err := s.Q.SetSetting(ctx, sqlcgen.SetSettingParams{Key: key, Value: value, UpdatedAt: nowMs()}); err != nil {
		return err
	}
	return s.reloadSettings(ctx)
}
