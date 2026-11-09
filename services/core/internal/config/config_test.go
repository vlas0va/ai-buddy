package config

import "testing"

func TestLoad(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Config
	}{
		{"defaults", nil, Config{8080, "postgres://buddy:buddy@postgres:5432/buddy?sslmode=disable", "redis://redis:6379/0", "stub"}},
		{"overrides", map[string]string{"CORE_PORT": "9000", "POSTGRES_URL": "p", "REDIS_URL": "r", "LLM_PROVIDER": "openai_compat"}, Config{9000, "p", "r", "openai_compat"}},
		{"bad port", map[string]string{"CORE_PORT": "abc"}, Config{Port: 8080}},
		{"out of range", map[string]string{"CORE_PORT": "70000"}, Config{Port: 8080}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Load(func(k string) string { return tc.env[k] })
			if tc.want.PostgresURL == "" {
				tc.want.PostgresURL, tc.want.RedisURL, tc.want.LLMProvider = got.PostgresURL, got.RedisURL, got.LLMProvider
			}
			if got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}
