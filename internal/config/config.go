// Package config loads and validates the runtime configuration shared by every
// Pulse service from the process environment. Service-specific configuration
// embeds Config and adds its own fields, keeping a single, tested source of
// truth for the settings common to the whole platform.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
)

// Environment identifiers accepted by APP_ENV.
const (
	EnvDevelopment = "development"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

// Default values applied when the corresponding variable is unset. They target
// the local Docker Compose stack so the services run out of the box.
const (
	defaultEnv          = EnvDevelopment
	defaultLogLevel     = "info"
	defaultDatabaseURL  = "postgres://pulse:pulse@localhost:5432/pulse?sslmode=disable"
	defaultRedisURL     = "redis://localhost:6379/0"
	defaultKafkaBrokers = "localhost:9092"
	defaultKafkaTopic   = "market.ticks"
)

// Config holds the settings shared across every Pulse service.
type Config struct {
	// Env is the deployment environment: development, staging or production.
	Env string
	// LogLevel is the minimum severity emitted by the structured logger.
	LogLevel slog.Level
	// DatabaseURL is the PostgreSQL connection string (pgx / libpq format).
	DatabaseURL string
	// RedisURL is the Redis connection string.
	RedisURL string
	// KafkaBrokers is the list of Kafka/Redpanda bootstrap brokers.
	KafkaBrokers []string
	// KafkaTopic is the topic carrying normalized market ticks.
	KafkaTopic string
}

// lookup resolves an environment variable, reporting whether it was present. It
// matches the signature of os.LookupEnv so production code injects that and
// tests inject a map-backed stub.
type lookup func(key string) (string, bool)

// Load reads and validates configuration from the process environment.
func Load() (*Config, error) {
	return load(os.LookupEnv)
}

// load is the testable core of Load, parameterized by the variable resolver.
func load(get lookup) (*Config, error) {
	level, err := parseLogLevel(getString(get, "LOG_LEVEL", defaultLogLevel))
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Env:          getString(get, "APP_ENV", defaultEnv),
		LogLevel:     level,
		DatabaseURL:  getString(get, "DATABASE_URL", defaultDatabaseURL),
		RedisURL:     getString(get, "REDIS_URL", defaultRedisURL),
		KafkaBrokers: getCSV(get, "KAFKA_BROKERS", defaultKafkaBrokers),
		KafkaTopic:   getString(get, "KAFKA_TOPIC", defaultKafkaTopic),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// IsProduction reports whether the service runs in the production environment.
func (c *Config) IsProduction() bool {
	return c.Env == EnvProduction
}

// String returns the trimmed value of the environment variable named key, or
// fallback when it is unset or blank. Service-specific configuration uses it to
// read its own variables with the same semantics as the shared settings, so the
// parsing rules stay defined in one place.
func String(key, fallback string) string {
	return getString(os.LookupEnv, key, fallback)
}

// CSV parses a comma-separated environment variable into a slice, trimming and
// dropping empty entries, falling back to the parsed fallback when unset.
func CSV(key, fallback string) []string {
	return getCSV(os.LookupEnv, key, fallback)
}

// validate ensures every field holds a usable value, returning the first error.
func (c *Config) validate() error {
	switch c.Env {
	case EnvDevelopment, EnvStaging, EnvProduction:
	default:
		return fmt.Errorf("config: invalid APP_ENV %q (want %s, %s or %s)",
			c.Env, EnvDevelopment, EnvStaging, EnvProduction)
	}
	if err := validateURL("DATABASE_URL", c.DatabaseURL, "postgres", "postgresql"); err != nil {
		return err
	}
	if err := validateURL("REDIS_URL", c.RedisURL, "redis", "rediss"); err != nil {
		return err
	}
	if len(c.KafkaBrokers) == 0 {
		return fmt.Errorf("config: KAFKA_BROKERS must list at least one broker")
	}
	if c.KafkaTopic == "" {
		return fmt.Errorf("config: KAFKA_TOPIC must not be empty")
	}
	return nil
}

// getString returns the trimmed value of key, or fallback when unset or blank.
func getString(get lookup, key, fallback string) string {
	if v, ok := get(key); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

// getCSV parses a comma-separated variable into a slice, trimming and dropping
// empty entries.
func getCSV(get lookup, key, fallback string) []string {
	raw := getString(get, key, fallback)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// parseLogLevel maps a textual level to slog.Level.
func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("config: invalid LOG_LEVEL %q", s)
	}
}

// validateURL parses raw and checks its scheme is one of the accepted schemes.
func validateURL(name, raw string, schemes ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("config: %s is not a valid URL: %w", name, err)
	}
	for _, s := range schemes {
		if u.Scheme == s {
			return nil
		}
	}
	return fmt.Errorf("config: %s has unsupported scheme %q (want one of: %s)",
		name, u.Scheme, strings.Join(schemes, ", "))
}
