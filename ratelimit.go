package functions

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is a sliding window of hit timestamps per client IP, swept
// periodically so idle IPs don't accumulate. Cloud Run instances don't share
// memory, so the limit is per-instance — deploy the POST functions with
// --max-instances=1 (see README.md) to keep it meaningful; that also caps
// the worst-case bill.
type rateLimiter struct {
	limit  int
	period time.Duration
	now    func() time.Time

	mu        sync.Mutex
	hits      map[string][]time.Time
	lastSweep time.Time
}

// A hard cap on remembered IPs so a scan across many addresses can't grow
// the map without bound.
const maxTrackedIPs = 10_000

func newRateLimiter(limit int, period time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, period: period, now: time.Now, hits: map[string][]time.Time{}}
}

// allow records a hit for ip and reports whether it is within the limit. A
// limited request is not recorded, so hammering while blocked doesn't extend
// the lockout.
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

// clientIP: Google's frontend appends the connecting client's address as the
// final X-Forwarded-For entry, so with the function serving its run.app URL
// directly only that final entry is trustworthy — everything before it,
// including whole extra header lines a client sends itself, is
// client-controlled and would let a caller pick their own rate-limit key.
// Scan the lines back to front, skipping empty fields so a malformed
// trailing comma cannot make "" the key, and fall back to the socket
// address when nothing usable remains.
func clientIP(r *http.Request) string {
	lines := r.Header.Values("X-Forwarded-For")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		for {
			comma := strings.LastIndexByte(line, ',')
			if ip := strings.TrimSpace(line[comma+1:]); ip != "" {
				return ip
			}
			if comma < 0 {
				break
			}
			line = line[:comma]
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
