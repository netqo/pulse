// Package health provides the observability HTTP surface (liveness, readiness
// and Prometheus metrics) and the container health-check client shared by every
// Pulse service, keeping the endpoints and probing rules defined in one place.
package health

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ReadyFunc reports whether a service is ready to serve. A non-nil error marks
// the service as not ready and its message is returned to the caller.
type ReadyFunc func(ctx context.Context) error

// probeTimeout bounds both the readiness check and the container health probe.
const probeTimeout = 2 * time.Second

// Handler builds the observability HTTP surface: /healthz reports process
// liveness, /readyz reflects real dependency health via ready (bounded by a
// timeout), and /metrics serves the Prometheus registry.
func Handler(reg *prometheus.Registry, ready ReadyFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeOK(w)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
		defer cancel()
		if err := ready(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeOK(w)
	})
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return mux
}

// Check probes the local liveness endpoint derived from a listen address such
// as ":9101" or "0.0.0.0:9101" and returns a process exit code (0 healthy, 1
// unhealthy). It backs the container HEALTHCHECK, which cannot use a shell
// because the runtime image is distroless.
func Check(addr string) int {
	url, err := healthURL(addr)
	if err != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	return probe(ctx, url)
}

// writeOK writes a 200 OK plain-text response.
func writeOK(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// probe issues a GET and returns 0 only on a 200 OK response.
func probe(ctx context.Context, url string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// healthURL derives the liveness URL to probe from a listen address, targeting
// the loopback interface when the address binds to a wildcard host.
func healthURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}
