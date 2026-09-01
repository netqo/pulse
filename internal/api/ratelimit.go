package api

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipRateLimiter throttles requests per client IP with an independent token
// bucket per IP. Idle buckets are swept opportunistically so the map stays
// bounded without a background goroutine.
type ipRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*ipBucket
	limit    rate.Limit
	burst    int
	ttl      time.Duration
	lastZap  time.Time
	sweepGap time.Duration
}

type ipBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// newIPRateLimiter builds a limiter allowing r requests per second per IP with
// the given burst. Buckets unused for ttl are eligible for eviction.
func newIPRateLimiter(r rate.Limit, burst int, ttl time.Duration) *ipRateLimiter {
	return &ipRateLimiter{
		buckets:  make(map[string]*ipBucket),
		limit:    r,
		burst:    burst,
		ttl:      ttl,
		sweepGap: ttl,
	}
}

// allow records a request from ip and reports whether it is within the rate.
func (l *ipRateLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[ip] = b
	}
	b.lastSeen = now
	return b.limiter.Allow()
}

// sweep evicts buckets idle beyond the TTL, at most once per sweepGap. The
// caller holds the lock.
func (l *ipRateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastZap) < l.sweepGap {
		return
	}
	l.lastZap = now
	for ip, b := range l.buckets {
		if now.Sub(b.lastSeen) > l.ttl {
			delete(l.buckets, ip)
		}
	}
}

// middleware rejects requests from an IP that exceeds its rate with 429.
func (l *ipRateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r), time.Now()) {
			writeJSON(w, http.StatusTooManyRequests, errorDTO{Error: "rate limit exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the client's IP from the request's remote address. It
// deliberately ignores X-Forwarded-For: without a configured trusted proxy that
// header is attacker-controlled, and honoring it would let a client mint
// unlimited buckets and bypass the limit. A deployment behind a reverse proxy
// must surface the real client IP through the connection (for example the PROXY
// protocol) rather than a spoofable header.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
