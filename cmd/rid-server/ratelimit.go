package main

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// limiter is a per-IP token bucket. It exists so a single app (or a single
// abusive client) cannot occupy every routing workspace: the cheap routes are
// served by a machine with two cores, and a stuck client would otherwise turn
// p95 for everybody else into the timeout.
type limiter struct {
	rate       float64 // tokens per second
	burst      float64
	trustProxy bool

	mu      sync.Mutex
	buckets map[string]*bucket
	calls   int
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perMinute, burst float64, trustProxy bool) *limiter {
	if perMinute <= 0 {
		return nil
	}
	if burst < 1 {
		burst = 1
	}
	return &limiter{
		rate:       perMinute / 60,
		burst:      burst,
		trustProxy: trustProxy,
		buckets:    make(map[string]*bucket, 64),
		now:        time.Now,
	}
}

// allow spends one token for key. When the bucket is empty it returns the wait
// until the next token, so the caller can send Retry-After.
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
		need := (1 - b.tokens) / l.rate
		return false, time.Duration(need * float64(time.Second))
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

// clientKey identifies the caller. Behind Caddy the real address is in
// X-Forwarded-For; trusting it is opt-in, because on a public listener anyone
// can forge it.
func clientKey(req *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
			for i := 0; i < len(xff); i++ {
				if xff[i] == ',' {
					return trimHost(xff[:i])
				}
			}
			return trimHost(xff)
		}
		if rip := req.Header.Get("X-Real-IP"); rip != "" {
			return rip
		}
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

func trimHost(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// wrap applies the limiter to a handler, answering 429 with Retry-After.
func (l *limiter) wrap(next http.HandlerFunc, m *metrics) http.HandlerFunc {
	if l == nil {
		return next
	}
	return func(w http.ResponseWriter, req *http.Request) {
		ok, wait := l.allow(clientKey(req, l.trustProxy))
		if !ok {
			secs := int(math.Ceil(wait.Seconds()))
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			if m != nil {
				m.rateLimited.Add(1)
			}
			httpErr(w, 429, "rate limit exceeded ("+strconv.Itoa(int(l.rate*60))+"/min per IP)")
			return
		}
		next(w, req)
	}
}
