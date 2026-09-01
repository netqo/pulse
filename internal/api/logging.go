package api

import (
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Bounds applied to the request path before it reaches a log entry.
const (
	// maxLoggedPathLen caps the logged path. The path is caller-controlled up to
	// the server's request-line limit, so an unbounded copy would let a hostile
	// caller decide how large each log record is.
	maxLoggedPathLen = 256
	truncationMarker = "..."
	// unmatchedRoute labels a request that matched no route, keeping the route
	// attribute drawn from a bounded set. It mirrors the metrics middleware,
	// which uses the same label for the same reason.
	unmatchedRoute = "other"
)

// requestAttrs returns the request-identifying attributes shared by every API
// log entry, as a flat slog key/value sequence. Callers append their own
// attributes to the result, which is freshly allocated per call.
//
// The matched route is the attribute to aggregate on: it comes from the mux's
// own registered patterns, so it is server-defined and bounded, and it is the
// same identifier the Prometheus middleware labels with. The concrete path is
// kept as well because it is what makes a 500 diagnosable, but it is sanitized
// and truncated first, since it is the one caller-controlled value here.
//
// Sanitizing is not what prevents log forgery: both slog handlers already
// escape control characters, so a newline in the path cannot open a second log
// record. It exists for what happens after the handler, where a pipeline that
// extracts this field and renders it somewhere less careful (a terminal, a
// dashboard) would otherwise re-materialize those bytes.
func requestAttrs(r *http.Request) []any {
	return []any{
		"method", r.Method,
		"route", routePattern(r),
		"path", sanitizeLogPath(r.URL.Path),
	}
}

// routePattern returns the mux pattern the request matched, or unmatchedRoute
// when it matched none. It is only populated once the mux has routed the
// request, so middleware must read it after the inner handler returns.
func routePattern(r *http.Request) string {
	if r.Pattern == "" {
		return unmatchedRoute
	}
	return r.Pattern
}

// sanitizeLogPath bounds a request path to a length safe to log and drops every
// non-printable rune, including the replacement rune that invalid UTF-8 decodes
// to. Truncation cuts on a byte boundary and so may split a multi-byte rune;
// dropping the replacement rune is what keeps that fragment out of the log.
func sanitizeLogPath(path string) string {
	if len(path) > maxLoggedPathLen {
		path = path[:maxLoggedPathLen] + truncationMarker
	}
	return strings.Map(func(r rune) rune {
		if r == utf8.RuneError || !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, path)
}
