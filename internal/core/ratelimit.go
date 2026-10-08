package core

import (
	"math"
	"sync"
	"time"
)

// Limiter is a per-key token bucket keyed by an arbitrary string (API key
// id, client IP). Buckets refill continuously at rate/minute.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter creates an empty limiter.
func NewLimiter() *Limiter {
	return &Limiter{buckets: map[string]*bucket{}}
}

// Allow consumes one token for key at the given per-minute rate. rpm <= 0
// means unlimited. It returns whether the call is allowed, the remaining
// whole tokens, and how long until a token is available when denied.
func (l *Limiter) Allow(key string, rpm int64) (ok bool, remaining int64, retryAfter time.Duration) {
	if rpm <= 0 {
		return true, math.MaxInt32, 0
	}
	now := time.Now()
	capacity := float64(rpm)
	perSec := capacity / 60

	l.mu.Lock()
	defer l.mu.Unlock()
	b, found := l.buckets[key]
	if !found {
		b = &bucket{tokens: capacity, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(capacity, b.tokens+now.Sub(b.last).Seconds()*perSec)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, int64(b.tokens), 0
	}
	wait := time.Duration((1-b.tokens)/perSec*float64(time.Second)) + time.Millisecond
	return false, 0, wait
}

// Sweep drops buckets idle for longer than age; call periodically.
func (l *Limiter) Sweep(age time.Duration) {
	cutoff := time.Now().Add(-age)
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}
