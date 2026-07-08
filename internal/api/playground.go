package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/netqo/pulse/internal/playground"
)

// maxQueryBytes bounds the size of a submitted SQL string.
const maxQueryBytes = 64 << 10

// playgroundRequest is the JSON body of a Playground query.
type playgroundRequest struct {
	Query string `json:"query"`
}

// queryErrorDTO is the error envelope for a rejected query, carrying the
// PostgreSQL error code when one is available.
type queryErrorDTO struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// handlePlaygroundQuery serves POST /api/v1/playground/query, running the
// submitted SQL under the sandbox and returning the capped result.
func (s *Server) handlePlaygroundQuery(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxQueryBytes)
	var req playgroundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeClientError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := s.sandbox.Execute(r.Context(), req.Query)
	if err != nil {
		s.writePlaygroundError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// writePlaygroundError maps a sandbox error to an HTTP response: validation and
// database query errors are client errors (400), a canceled request is dropped,
// and anything else is a logged 500.
func (s *Server) writePlaygroundError(w http.ResponseWriter, r *http.Request, err error) {
	if msg, rejected := readOnlyRejectionMessage(err); rejected {
		s.writeClientError(w, http.StatusBadRequest, msg)
		return
	}
	var queryErr *playground.QueryError
	switch {
	case errors.As(err, &queryErr):
		writeJSON(w, http.StatusBadRequest, queryErrorDTO{Error: queryErr.Message, Code: queryErr.Code})
	case errors.Is(err, context.Canceled):
		s.logger.Debug("playground query canceled by client", "path", r.URL.Path)
	default:
		s.logger.Error("playground query failed", "error", err)
		s.writeClientError(w, http.StatusInternalServerError, "internal error")
	}
}

// readOnlyRejectionMessage maps a playground read-only validation sentinel to a
// client-facing message. rejected is false when err is nil or not a known
// validation rejection, so callers can fall through to other handling.
func readOnlyRejectionMessage(err error) (msg string, rejected bool) {
	switch {
	case errors.Is(err, playground.ErrEmptyQuery):
		return "query is empty", true
	case errors.Is(err, playground.ErrNotReadOnly):
		return "only SELECT, WITH, VALUES or TABLE queries are allowed", true
	default:
		return "", false
	}
}
