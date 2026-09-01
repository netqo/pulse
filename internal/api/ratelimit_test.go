package api

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestIPRateLimiterBurstAndIsolation(t *testing.T) {
	l := newIPRateLimiter(rate.Limit(1), 2, time.Minute)
	now := time.Unix(1000, 0)

	// The burst of 2 is consumed, then further requests are denied.
	first := l.allow("1.1.1.1", now)
	second := l.allow("1.1.1.1", now)
	if !first || !second {
		t.Fatal("first two requests should be allowed within the burst")
	}
	if l.allow("1.1.1.1", now) {
		t.Error("third request should be rate limited")
	}
	// A different IP has its own bucket.
	if !l.allow("2.2.2.2", now) {
		t.Error("a different IP should have an independent bucket")
	}
}

func TestIPRateLimiterEvictsStaleBuckets(t *testing.T) {
	l := newIPRateLimiter(rate.Limit(1), 1, time.Minute)
	t0 := time.Unix(1000, 0)

	l.allow("1.1.1.1", t0)
	// A request past the TTL triggers a sweep that evicts the idle bucket.
	l.allow("2.2.2.2", t0.Add(2*time.Minute))

	l.mu.Lock()
	_, present := l.buckets["1.1.1.1"]
	l.mu.Unlock()
	if present {
		t.Error("bucket idle beyond the TTL should be evicted")
	}
}
