package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// metrics groups the HTTP request collectors the API maintains.
type metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "api_http_requests_total",
			Help: "Total number of HTTP requests handled, by route, method and status.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "api_http_request_duration_seconds",
			Help:    "HTTP request handling latency in seconds, by route and method.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

// statusRecorder captures the response status code so the metrics middleware can
// label by it. It defaults to 200, the status implied by a bare body write.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// instrument wraps next with request-count and latency instrumentation. It is
// applied around the mux, so r.Pattern holds the matched route by the time next
// returns; unmatched requests are grouped under a single "other" label to bound
// metric cardinality. Recording runs in a defer so a request is still counted if
// an inner handler panics before returning.
func (m *metrics) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			route := r.Pattern
			if route == "" {
				route = "other"
			}
			m.duration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
			m.requests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		}()

		next.ServeHTTP(rec, r)
	})
}
