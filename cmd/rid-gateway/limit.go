package main

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// limiter is a per-IP token bucket for the bundled geocoder. It exists because
// /place is a linear scan of the whole index: its measured ceiling is ~20 RPS,
// and a single client would otherwise spend the budget of everyone else. The
// routing upstreams enforce their own limit; this one covers the feature the
// gateway proxies without a limiter of its own.
type limiter struct {
	rate  float64 // tokens per second
	burst float64

	mu      sync.Mutex
	buckets map[string]*bucket
	calls   int
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perMinute, burst float64) *limiter {
	if perMinute <= 0 {
		return nil
	}
	if burst < 1 {
		burst = 1
	}
	return &limiter{
		rate:    perMinute / 60,
		burst:   burst,
		buckets: make(map[string]*bucket, 64),
		now:     time.Now,
	}
}

func (l *limiter) allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls++
	if l.calls%1024 == 0 {
		l.sweep(now)
	}
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	}
	b.tokens--
	return true, 0
}

// sweep drops buckets that have refilled completely: they are identical to a
// fresh bucket, so keeping them only grows the map.
func (l *limiter) sweep(now time.Time) {
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) > 2*full {
			delete(l.buckets, k)
		}
	}
}

// allowRequest answers 429 with Retry-After when the caller is over the limit.
// The key is the address a trusted proxy reported, so one Cloudflare client
// cannot exhaust the buckets of every other client.
func (l *limiter) allowRequest(w http.ResponseWriter, r *http.Request, key string) bool {
	if l == nil {
		return true
	}
	ok, wait := l.allow(key)
	if ok {
		return true
	}
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
	return false
}
