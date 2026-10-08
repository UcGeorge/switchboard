// Package core implements Switchboard's domain: API keys, agent tokens and
// OAuth, channels, the request broker (queue, claims, leases, streaming),
// conversation reconstruction, sessions and statistics. Both the server and
// the CLI use it directly against the same SQLite database.
package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/events"
)

// Service is the façade over the domain.
type Service struct {
	*Store
	Bus *events.Bus
	Log *slog.Logger

	settings settingsCache

	// broker state
	liveMu  sync.Mutex
	live    map[string]*liveRequest
	queueMu sync.Mutex
	queueCh chan struct{}

	limiter      *Limiter
	loginLimiter *Limiter

	// ListenAddr is the address the HTTP server is bound to (set by the app).
	ListenAddr string
	StartedAt  time.Time

	reaperCancel context.CancelFunc
	reaperDone   chan struct{}
}

// New wires a Service over an opened database. The event bus persists to the
// events table.
func New(d *sql.DB, log *slog.Logger) (*Service, error) {
	if log == nil {
		log = slog.Default()
	}
	s := &Service{
		Store:        NewStore(d),
		Log:          log,
		live:         map[string]*liveRequest{},
		queueCh:      make(chan struct{}),
		limiter:      NewLimiter(),
		loginLimiter: NewLimiter(),
		StartedAt:    time.Now(),
	}
	s.Bus = events.New(s.persistEvent)
	if err := s.reloadSettings(context.Background()); err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	return s, nil
}

func (s *Service) persistEvent(e events.Event) {
	data := "{}"
	if len(e.Data) > 0 {
		if b, err := json.Marshal(e.Data); err == nil {
			data = string(b)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.Q.InsertEvent(ctx, sqlcgen.InsertEventParams{
		Ts:             e.Time.UnixMilli(),
		Kind:           e.Kind,
		Level:          e.Level,
		RequestID:      nilIfEmpty(e.RequestID),
		ChannelID:      nilIfEmpty(e.ChannelID),
		ApiKeyID:       nilIfEmpty(e.APIKeyID),
		ConversationID: nilIfEmpty(e.ConversationID),
		Message:        e.Message,
		Data:           data,
	})
	if err != nil {
		s.Log.Warn("persist event", "kind", e.Kind, "err", err)
	}
}

func (s *Service) publish(e events.Event) {
	s.Bus.Publish(e)
}

// Start recovers state from a previous run and launches background upkeep.
// Call it only from the long-running server, never from one-shot CLI use.
func (s *Service) Start(ctx context.Context) error {
	if err := s.recoverOnStart(ctx); err != nil {
		return err
	}
	rctx, cancel := context.WithCancel(context.Background())
	s.reaperCancel = cancel
	s.reaperDone = make(chan struct{})
	go s.reaperLoop(rctx)
	s.publish(events.Event{Kind: events.SystemStarted, Message: "Switchboard started", Data: map[string]any{"addr": s.ListenAddr}})
	return nil
}

// Close stops background work and flushes the event bus.
func (s *Service) Close() {
	if s.reaperCancel != nil {
		s.reaperCancel()
		<-s.reaperDone
		s.publish(events.Event{Kind: events.SystemStopped, Message: "Switchboard stopped"})
	}
	s.Bus.Close()
}

// Uptime reports how long the service has been running.
func (s *Service) Uptime() time.Duration { return time.Since(s.StartedAt).Truncate(time.Second) }

// BaseURL returns the externally visible base URL: the configured public URL
// if set, otherwise one derived from the request host (or the listen address).
func (s *Service) BaseURL(reqHost string, tls bool) string {
	if pub := s.Settings().PublicURL; pub != "" {
		return pub
	}
	scheme := "http"
	if tls {
		scheme = "https"
	}
	host := reqHost
	if host == "" {
		host = s.LocalURLHost()
	}
	return scheme + "://" + host
}

// LocalURLHost renders the listen address as a host suitable for a URL
// (0.0.0.0 and :: become 127.0.0.1 / localhost).
func (s *Service) LocalURLHost() string {
	addr := s.ListenAddr
	if addr == "" {
		return "127.0.0.1:8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host + ":" + port
}

// LocalURL returns http://<host>:<port> for the running instance.
func (s *Service) LocalURL() string {
	if pub := s.Settings().PublicURL; pub != "" {
		return pub
	}
	return "http://" + s.LocalURLHost()
}
