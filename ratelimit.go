package functions

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter ports the Sinatra app's in-memory RateLimit middleware: a
// sliding window of hit timestamps per client IP, swept periodically so idle
// IPs don't accumulate. Cloud Run instances don't share memory, so the limit
// is per-instance — deploy the POST functions with --max-instances=1 (see
// README.md) to keep it meaningful; that also caps the worst-case bill.
type rateLimiter struct {
	limit  int
	period time.Duration
	now    func() time.Time

	mu        sync.Mutex
	hits      map[string][]time.Time
	lastSweep time.Time
}

// Mirrors MAX_TRACKED in the Ruby middleware: a hard cap on remembered IPs
// so a scan across many addresses can't grow the map without bound.
const maxTrackedIPs = 10_000

func newRateLimiter(limit int, period time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, period: period, now: time.Now, hits: map[string][]time.Time{}}
}

// allow records a hit for ip and reports whether it is within the limit; a
// limited request is not recorded, matching the Ruby middleware.
func (l *rateLimiter) allow(ip string) bool {
	now := l.now()
	cutoff := now.Add(-l.period)

	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) >= l.period {
		for tracked, hits := range l.hits {
			if len(hits) == 0 || !hits[len(hits)-1].After(cutoff) {
				delete(l.hits, tracked)
			}
		}
		l.lastSweep = now
	}

	hits := l.hits[ip]
	for len(hits) > 0 && !hits[0].After(cutoff) {
		hits = hits[1:]
	}

	limited := len(hits) >= l.limit
	if !limited {
		hits = append(hits, now)
	}
	l.hits[ip] = hits

	for tracked := range l.hits {
		if len(l.hits) <= maxTrackedIPs {
			break
		}
		delete(l.hits, tracked)
	}

	return !limited
}

// clientIP: Cloud Run terminates TLS at its proxy and puts the real client
// address first in X-Forwarded-For; fall back to the socket address for
// local runs.
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
