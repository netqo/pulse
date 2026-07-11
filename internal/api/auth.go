package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// operatorAuth builds middleware that requires a valid operator bearer token in
// the Authorization header. When token is empty the middleware rejects every
// request (fail closed), so a deployment that never configured a token cannot
// expose the protected endpoints.
func operatorAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !validOperatorToken(r, token) {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeJSON(w, http.StatusUnauthorized, errorDTO{Error: "unauthorized"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// validOperatorToken reports whether the request carries the expected bearer
// token. The comparison is constant-time to avoid leaking the token through
// response timing; an empty expected token never matches.
func validOperatorToken(r *http.Request, expected string) bool {
	if expected == "" {
		return false
	}
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	got := header[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}
