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
	// window is anchored to allowed hits only, matching the Ruby middleware.
	for i := 0; i < 5; i++ {
		clock.at = clock.at.Add(10 * time.Second)
		limiter.allow("10.0.0.1")
	}

	clock.at = clock.at.Add(11 * time.Second) // 61s after the allowed hit
	if !limiter.allow("10.0.0.1") {
		t.Error("the block should lift one period after the allowed hit")
	}
}

func TestClientIPPrefersForwardedFor(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("X-Forwarded-For", "203.0.113.9, 10.1.2.3")
	if got := clientIP(request); got != "203.0.113.9" {
		t.Errorf("clientIP = %q, want the first forwarded address", got)
	}

	bare := httptest.NewRequest(http.MethodPost, "/", nil)
	bare.RemoteAddr = "192.0.2.4:5555"
	if got := clientIP(bare); got != "192.0.2.4" {
		t.Errorf("clientIP = %q, want the socket host", got)
	}
}
