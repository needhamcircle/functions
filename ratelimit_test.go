package functions

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeClock lets the tests move time forward deterministically.
type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time { return c.at }

func testLimiter(limit int, period time.Duration) (*rateLimiter, *fakeClock) {
	clock := &fakeClock{at: time.Unix(1_000_000, 0)}
	limiter := newRateLimiter(limit, period)
	limiter.now = clock.now
	return limiter, clock
}

func TestAllowPermitsUpToTheLimitThenBlocks(t *testing.T) {
	limiter, _ := testLimiter(3, time.Minute)

	for i := 0; i < 3; i++ {
		if !limiter.allow("10.0.0.1") {
			t.Fatalf("hit %d should be allowed", i+1)
		}
	}
	if limiter.allow("10.0.0.1") {
		t.Error("hit 4 should be blocked")
	}
	if !limiter.allow("10.0.0.2") {
		t.Error("a different IP should be unaffected")
	}
}

func TestAllowRefillsAfterThePeriod(t *testing.T) {
	limiter, clock := testLimiter(2, time.Minute)

	limiter.allow("10.0.0.1")
	limiter.allow("10.0.0.1")
	if limiter.allow("10.0.0.1") {
		t.Fatal("third hit inside the window should be blocked")
	}

	clock.at = clock.at.Add(61 * time.Second)
	if !limiter.allow("10.0.0.1") {
		t.Error("hits should be allowed again after the window passes")
	}
}

func TestBlockedHitsAreNotRecorded(t *testing.T) {
	limiter, clock := testLimiter(1, time.Minute)

	limiter.allow("10.0.0.1")
	// Hammering while blocked must not extend the lockout: the sliding
	// window is anchored to allowed hits only.
	for i := 0; i < 5; i++ {
		clock.at = clock.at.Add(10 * time.Second)
		limiter.allow("10.0.0.1")
	}

	clock.at = clock.at.Add(11 * time.Second) // 61s after the allowed hit
	if !limiter.allow("10.0.0.1") {
		t.Error("the block should lift one period after the allowed hit")
	}
}

func TestClientIPUsesLastForwardedFor(t *testing.T) {
	// The frontend appends the real client address after any entries the
	// client sent itself, so every spoofed prefix entry must be ignored.
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 198.51.100.8, 203.0.113.9")
	if got := clientIP(request); got != "203.0.113.9" {
		t.Errorf("clientIP = %q, want the last forwarded address", got)
	}

	single := httptest.NewRequest(http.MethodPost, "/", nil)
	single.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := clientIP(single); got != "203.0.113.9" {
		t.Errorf("clientIP = %q, want the sole forwarded address", got)
	}

	// The rule is positional, not address-classifying: a private-range final
	// hop wins. This is what flips if a proxy is ever placed in front of the
	// functions, at which point clientIP must learn the extra hop.
	proxied := httptest.NewRequest(http.MethodPost, "/", nil)
	proxied.Header.Set("X-Forwarded-For", "203.0.113.9, 10.1.2.3")
	if got := clientIP(proxied); got != "10.1.2.3" {
		t.Errorf("clientIP = %q, want the final hop", got)
	}
}

func TestClientIPScansAllForwardedForLines(t *testing.T) {
	// A client can send X-Forwarded-For as repeated header lines; the
	// frontend-appended entry is the last field of the last line, so earlier
	// lines are as untrusted as earlier fields.
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Add("X-Forwarded-For", "198.51.100.7")
	request.Header.Add("X-Forwarded-For", "198.51.100.8, 203.0.113.9")
	if got := clientIP(request); got != "203.0.113.9" {
		t.Errorf("clientIP = %q, want the last entry of the last line", got)
	}
}

func TestClientIPSkipsEmptyForwardedForFields(t *testing.T) {
	trailing := httptest.NewRequest(http.MethodPost, "/", nil)
	trailing.Header.Set("X-Forwarded-For", "203.0.113.9,")
	if got := clientIP(trailing); got != "203.0.113.9" {
		t.Errorf("clientIP = %q, want the last non-empty entry", got)
	}

	// A header with no usable entries must not make "" the rate-limit key,
	// which would pool unrelated clients into one bucket.
	empty := httptest.NewRequest(http.MethodPost, "/", nil)
	empty.Header.Set("X-Forwarded-For", " , ")
	empty.RemoteAddr = "192.0.2.4:5555"
	if got := clientIP(empty); got != "192.0.2.4" {
		t.Errorf("clientIP = %q, want the socket host", got)
	}
}

func TestClientIPFallsBackToSocketAddress(t *testing.T) {
	bare := httptest.NewRequest(http.MethodPost, "/", nil)
	bare.RemoteAddr = "192.0.2.4:5555"
	if got := clientIP(bare); got != "192.0.2.4" {
		t.Errorf("clientIP = %q, want the socket host", got)
	}

	// A RemoteAddr with no host:port shape (a unix socket peer, or a
	// hand-built request) is used verbatim rather than dropped.
	raw := httptest.NewRequest(http.MethodPost, "/", nil)
	raw.RemoteAddr = "@"
	if got := clientIP(raw); got != "@" {
		t.Errorf("clientIP = %q, want the raw remote address", got)
	}
}
