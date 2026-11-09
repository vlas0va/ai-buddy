// Package config parses service configuration from environment variables.
package config

import "strconv"

// Config holds core settings.
type Config struct {
	Port        int
	PostgresURL string
	RedisURL    string
	LLMProvider string
}

// Load reads configuration using getenv (os.Getenv in production).
func Load(getenv func(string) string) Config {
	str := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	port := 8080
	if v := getenv("CORE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			port = p
		}
	}
	return Config{
		Port:        port,
		PostgresURL: str("POSTGRES_URL", "postgres://buddy:buddy@postgres:5432/buddy?sslmode=disable"),
		RedisURL:    str("REDIS_URL", "redis://redis:6379/0"),
		LLMProvider: str("LLM_PROVIDER", "stub"),
	}
}
