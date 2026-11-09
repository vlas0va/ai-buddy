// Package config parses service configuration from environment variables.
package config

import "strconv"

// Config holds mcp-absence settings.
type Config struct {
	Port int
}

// Load reads configuration using getenv (os.Getenv in production).
func Load(getenv func(string) string) Config {
	port := 8101
	if v := getenv("MCP_ABSENCE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			port = p
		}
	}
	return Config{
		Port: port,
	}
}
