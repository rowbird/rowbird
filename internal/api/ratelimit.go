package api

import (
	"sync"
	"time"
)

// rateLimiter is an in-memory token bucket per key (a client IP). Each instance keeps its own
// buckets; per-account lockout in the store is what holds across instances.
type rateLimiter struct {
	mu       sync.Mutex
	capacity float64
	refill   float64 // tokens per second
	buckets  map[string]*bucket
	now      func() time.Time
	lastGC   time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// newRateLimiter allows burst requests per key, refilled evenly over window.
func newRateLimiter(burst int, window time.Duration, now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	return &rateLimiter{
		capacity: float64(burst),
		refill:   float64(burst) / window.Seconds(),
		buckets:  map[string]*bucket{},
		now:      now,
	}
}

// allow takes a token for key. When none is left it returns false and how long until one is.
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.capacity, b.tokens+now.Sub(b.last).Seconds()*l.refill)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.refill * float64(time.Second))
}

// gc drops buckets that have refilled completely, at most once a minute.
func (l *rateLimiter) gc(now time.Time) {
	if now.Sub(l.lastGC) < time.Minute {
		return
	}
	l.lastGC = now
	full := time.Duration(l.capacity / l.refill * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) >= full {
			delete(l.buckets, k)
		}
	}
}
