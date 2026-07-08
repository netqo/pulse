package config

import (
	"log/slog"
	"testing"
)

// envStub builds a lookup backed by a map for deterministic, isolated tests.
func envStub(vars map[string]string) lookup {
	return func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(envStub(nil))
	if err != nil {
		t.Fatalf("load with empty env: unexpected error: %v", err)
	}

	if cfg.Env != EnvDevelopment {
		t.Errorf("Env = %q, want %q", cfg.Env, EnvDevelopment)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelInfo)
	}
	if cfg.KafkaTopic != defaultKafkaTopic {
		t.Errorf("KafkaTopic = %q, want %q", cfg.KafkaTopic, defaultKafkaTopic)
	}
	if len(cfg.KafkaBrokers) != 1 || cfg.KafkaBrokers[0] != "localhost:9092" {
		t.Errorf("KafkaBrokers = %v, want [localhost:9092]", cfg.KafkaBrokers)
	}
	if cfg.IsProduction() {
		t.Error("IsProduction() = true, want false for development")
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := load(envStub(map[string]string{
		"APP_ENV":       EnvProduction,
		"LOG_LEVEL":     "warn",
		"DATABASE_URL":  "postgresql://u:p@db:5432/pulse",
		"REDIS_URL":     "rediss://cache:6380/1",
		"KAFKA_BROKERS": " a:9092 , b:9092 ,, c:9092 ",
		"KAFKA_TOPIC":   "market.ticks.v2",
	}))
	if err != nil {
		t.Fatalf("load with overrides: unexpected error: %v", err)
	}

	if cfg.LogLevel != slog.LevelWarn {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelWarn)
	}
	if !cfg.IsProduction() {
		t.Error("IsProduction() = false, want true")
	}
	want := []string{"a:9092", "b:9092", "c:9092"}
	if len(cfg.KafkaBrokers) != len(want) {
		t.Fatalf("KafkaBrokers = %v, want %v", cfg.KafkaBrokers, want)
	}
	for i, b := range want {
		if cfg.KafkaBrokers[i] != b {
			t.Errorf("KafkaBrokers[%d] = %q, want %q", i, cfg.KafkaBrokers[i], b)
		}
	}
}

func TestString(t *testing.T) {
	t.Setenv("PULSE_TEST_STRING", "  hello  ")
	if got := String("PULSE_TEST_STRING", "fallback"); got != "hello" {
		t.Errorf("String(set) = %q, want hello", got)
	}
	if got := String("PULSE_TEST_STRING_MISSING", "fallback"); got != "fallback" {
		t.Errorf("String(unset) = %q, want fallback", got)
	}
	t.Setenv("PULSE_TEST_STRING_BLANK", "   ")
	if got := String("PULSE_TEST_STRING_BLANK", "fallback"); got != "fallback" {
		t.Errorf("String(blank) = %q, want fallback", got)
	}
}

func TestCSV(t *testing.T) {
	t.Setenv("PULSE_TEST_CSV", " a , b ,, c ")
	got := CSV("PULSE_TEST_CSV", "x")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("CSV = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CSV[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got := CSV("PULSE_TEST_CSV_MISSING", "d,e"); len(got) != 2 {
		t.Errorf("CSV(unset) = %v, want fallback parsed to 2 entries", got)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	tests := map[string]map[string]string{
		"invalid env":          {"APP_ENV": "prod"},
		"invalid log level":    {"LOG_LEVEL": "verbose"},
		"bad database scheme":  {"DATABASE_URL": "mysql://u:p@db:3306/pulse"},
		"bad redis scheme":     {"REDIS_URL": "http://cache:6379"},
		"empty kafka brokers":  {"KAFKA_BROKERS": " , "},
		"unparseable database": {"DATABASE_URL": "postgres://user:%zz@db"},
	}

	for name, env := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := load(envStub(env)); err == nil {
				t.Errorf("load(%v): expected error, got nil", env)
			}
		})
	}
}
