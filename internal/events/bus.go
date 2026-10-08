// Package events is the in-process event bus: every state change in the
// system is published here, persisted to the events table (unless
// ephemeral) and fanned out to live subscribers (dashboard SSE, TUI).
package events

import (
	"sync"
	"sync/atomic"
	"time"
)

// Event kinds. Keep these stable: they appear in the dashboard filters and
// are stored in the database.
const (
	RequestQueued       = "request.queued"
	RequestClaimed      = "request.claimed"
	RequestStreaming    = "request.streaming"
	RequestDelta        = "request.delta" // ephemeral
	RequestCompleted    = "request.completed"
	RequestFailed       = "request.failed"
	RequestRequeued     = "request.requeued"
	RequestCancelled    = "request.cancelled"
	RequestExpired      = "request.expired"
	RequestLeaseExpired = "request.lease_expired"

	ChannelOpened  = "channel.opened"
	ChannelClosed  = "channel.closed"
	ChannelOffline = "channel.offline"
	ChannelOnline  = "channel.online"

	KeyCreated   = "key.created"
	KeyRevoked   = "key.revoked"
	KeyUpdated   = "key.updated"
	TokenCreated = "token.created"
	TokenRevoked = "token.revoked"

	OAuthClientRegistered = "oauth.client_registered"
	OAuthTokenIssued      = "oauth.token_issued"
	OAuthConsentDenied    = "oauth.consent_denied"

	AuthLogin       = "auth.login"
	AuthLoginFailed = "auth.login_failed"
	AuthLogout      = "auth.logout"
	AuthRateLimited = "auth.rate_limited"

	SystemStarted  = "system.started"
	SystemStopped  = "system.stopped"
	SystemPruned   = "system.pruned"
	SettingChanged = "system.setting_changed"
)

// Levels.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Event is a single occurrence in the system.
type Event struct {
	ID             int64          `json:"id,omitempty"`
	Time           time.Time      `json:"time"`
	Kind           string         `json:"kind"`
	Level          string         `json:"level"`
	RequestID      string         `json:"request_id,omitempty"`
	ChannelID      string         `json:"channel_id,omitempty"`
	APIKeyID       string         `json:"api_key_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	Message        string         `json:"message"`
	Data           map[string]any `json:"data,omitempty"`
	// Ephemeral events are fanned out to live subscribers but never persisted.
	Ephemeral bool `json:"-"`
}

// Bus persists events and fans them out to subscribers.
type Bus struct {
	mu      sync.RWMutex
	subs    map[int]*subscriber
	nextID  int
	store   func(Event)
	closed  bool
	dropped atomic.Int64
}

type subscriber struct {
	ch chan Event
}

// New creates a bus. store (may be nil) is invoked for every non-ephemeral
// event before it is delivered to subscribers.
func New(store func(Event)) *Bus {
	return &Bus{subs: map[int]*subscriber{}, store: store}
}

// Publish records e and then delivers it to subscribers.
//
// Persistence happens first and synchronously, so anything a subscriber
// learns about is already readable from the store: a dashboard that
// refreshes a request's timeline in response to an event is guaranteed to
// see that event. Delivery itself never blocks; a subscriber whose buffer is
// full misses the event.
//
// Do not call Publish while holding a database transaction: the store
// writes on its own connection and would wait on the transaction's lock.
func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.Level == "" {
		e.Level = LevelInfo
	}
	b.mu.RLock()
	closed := b.closed
	b.mu.RUnlock()
	if closed {
		return
	}
	if !e.Ephemeral && b.store != nil {
		b.store(e)
	}
	b.mu.RLock()
	for _, s := range b.subs {
		select {
		case s.ch <- e:
		default:
			b.dropped.Add(1)
		}
	}
	b.mu.RUnlock()
}

// Subscribe returns a channel receiving every published event. Call the
// returned function to unsubscribe.
func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 256
	}
	s := &subscriber{ch: make(chan Event, buffer)}
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	if b.closed {
		close(s.ch)
	} else {
		b.subs[id] = s
	}
	b.mu.Unlock()
	return s.ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(s.ch)
		}
		b.mu.Unlock()
	}
}

// Close stops accepting events and closes subscribers.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, s := range b.subs {
		delete(b.subs, id)
		close(s.ch)
	}
}

// Subscribers reports the number of live subscribers.
func (b *Bus) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Dropped reports how many deliveries were skipped because a subscriber's
// buffer was full.
func (b *Bus) Dropped() int64 { return b.dropped.Load() }
