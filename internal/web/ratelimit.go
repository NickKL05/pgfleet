package web

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// rateLimiter is a per-client token bucket. It exists because the dashboard is
// deployed without authentication: the API endpoints are cheap to request and
// expensive to serve, so an unthrottled client can turn a stream of HTTP
// requests into a stream of multi-schema catalog scans.
//
// Implemented directly rather than pulled in as a dependency: it is a few dozen
// lines, and the binary's zero-dependency-at-runtime story is worth keeping.
type rateLimiter struct {
	rate    float64       // tokens refilled per second
	burst   float64       // bucket capacity, i.e. the tolerated burst
	idleTTL time.Duration // buckets untouched for this long are dropped

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perSecond, burst float64) *rateLimiter {
	return &rateLimiter{
		rate:      perSecond,
		burst:     burst,
		idleTTL:   10 * time.Minute,
		buckets:   make(map[string]*bucket),
		lastSweep: time.Now(),
	}
}

// allow consumes one token for key, reporting whether the request may proceed.
func (rl *rateLimiter) allow(key string) bool {
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.sweep(now)

	b, ok := rl.buckets[key]
	if !ok {
		// A new client starts with a full bucket, so ordinary browsing is never
		// throttled on arrival.
		b = &bucket{tokens: rl.burst, last: now}
		rl.buckets[key] = b
	}

	b.tokens += now.Sub(b.last).Seconds() * rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets that have been idle long enough to have fully refilled,
// so the map does not grow without bound as addresses come and go. It runs at
// most once per idleTTL and is called with the mutex held.
func (rl *rateLimiter) sweep(now time.Time) {
	if now.Sub(rl.lastSweep) < rl.idleTTL {
		return
	}
	rl.lastSweep = now
	for key, b := range rl.buckets {
		if now.Sub(b.last) > rl.idleTTL {
			delete(rl.buckets, key)
		}
	}
}

// clientIP is the rate-limiting key. It uses the transport-level peer address
// and deliberately ignores X-Forwarded-For: this deployment has no trusted
// proxy in front of it, so honoring that header would let any client pick its
// own bucket and opt out of the limit. Put this behind a proxy and the header
// handling has to be revisited.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
