package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/playground"
)

// Save request bounds. maxSaveBytes caps the whole body; the field limits mirror
// the schema's CHECK constraints so a client gets a clear 400 rather than a 500
// from a rejected write.
const (
	maxSaveBytes  = 128 << 10
	maxTitleChars = 200
	maxSQLChars   = 20000
	maxChartBytes = 32 << 10
)

// saveQueryRequest is the JSON body of a save request. Title and ChartConfig are
// optional; ChartConfig is stored opaquely and interpreted by the frontend.
type saveQueryRequest struct {
	Title       *string         `json:"title"`
	Query       string          `json:"query"`
	ChartConfig json.RawMessage `json:"chart_config"`
}

// saveQueryResponse returns the new query's id and the path that loads it.
type saveQueryResponse struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// savedQueryDTO is the loaded representation of a saved query. The SQL is
// exposed as "query" for symmetry with the execute endpoint, so a loaded query
// drops straight back into the editor.
type savedQueryDTO struct {
	ID          string          `json:"id"`
	Title       *string         `json:"title,omitempty"`
	Query       string          `json:"query"`
	ChartConfig json.RawMessage `json:"chart_config,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// handleSaveQuery serves POST /api/v1/playground/save: it validates and persists
// a shareable query, returning its id and load path.
func (s *Server) handleSaveQuery(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSaveBytes)
	var req saveQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeClientError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if msg, ok := validateSaveRequest(req); !ok {
		s.writeClientError(w, http.StatusBadRequest, msg)
		return
	}

	saved, err := s.queries.SaveQuery(r.Context(), db.SaveQueryInput{
		Title:       req.Title,
		SQL:         req.Query,
		ChartConfig: req.ChartConfig,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, saveQueryResponse{ID: saved.ID, URL: loadPath(saved.ID)})
}

// handleLoadQuery serves GET /api/v1/playground/q/{id}: it loads a saved query by
// its UUID. A malformed id yields a 400 and an unknown one a 404, mapped by
// writeError from the store's ErrInvalidID / ErrNotFound.
func (s *Server) handleLoadQuery(w http.ResponseWriter, r *http.Request) {
	saved, err := s.queries.SavedQuery(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSavedQueryDTO(saved))
}

// validateSaveRequest enforces the request-level bounds and the read-only rule
// before a query is persisted, so only a valid, runnable query is stored. It
// returns a client-facing message and ok=false when the request is rejected.
func validateSaveRequest(req saveQueryRequest) (msg string, ok bool) {
	if m, rejected := readOnlyRejectionMessage(playground.Validate(req.Query)); rejected {
		return m, false
	}
	switch {
	case utf8.RuneCountInString(req.Query) > maxSQLChars:
		return fmt.Sprintf("query exceeds %d characters", maxSQLChars), false
	case req.Title != nil && utf8.RuneCountInString(*req.Title) > maxTitleChars:
		return fmt.Sprintf("title exceeds %d characters", maxTitleChars), false
	case len(req.ChartConfig) > maxChartBytes:
		return fmt.Sprintf("chart_config exceeds %d bytes", maxChartBytes), false
	default:
		return "", true
	}
}

// toSavedQueryDTO renders a stored query for the wire.
func toSavedQueryDTO(s db.SavedQuery) savedQueryDTO {
	return savedQueryDTO{
		ID:          s.ID,
		Title:       s.Title,
		Query:       s.SQL,
		ChartConfig: json.RawMessage(s.ChartConfig),
		CreatedAt:   s.CreatedAt,
	}
}

// loadPath is the API path that loads a saved query by id.
func loadPath(id string) string {
	return "/api/v1/playground/q/" + id
}
