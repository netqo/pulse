-- name: CreateSavedQuery :one
-- Persists a Playground query and returns the stored row, including its
-- generated id and timestamp, so the caller can build the share URL.
INSERT INTO saved_queries (title, sql_text, chart_config)
VALUES ($1, $2, $3)
RETURNING id, title, sql_text, chart_config, created_at;

-- name: GetSavedQuery :one
-- Loads a saved query by its UUID, or no rows if the id is unknown.
SELECT id, title, sql_text, chart_config, created_at
FROM saved_queries
WHERE id = $1;
