package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestEnv_ValuesAndDefaults(t *testing.T) {
	env := New(envOf(map[string]string{
		"CORE_PORT":     " 9090 ",
		"AGENT_URL":     "http://agent:8090",
		"EMPTY":         "   ",
		"CRON_INTERVAL": "24h",
		"LLM_TIMEOUT_S": "30",
		"CORS_ORIGINS":  "http://localhost:5173, ,https://buddy.example,",
		"JWT_SECRET":    "s3cret",
	}))
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"int set", env.Int("CORE_PORT", 8080), 9090},
		{"int default", env.Int("MCP_MOCKS_PORT", 8100), 8100},
		{"string set", env.String("AGENT_URL", "x"), "http://agent:8090"},
		{"string blank is unset", env.String("EMPTY", "def"), "def"},
		{"duration", env.Duration("CRON_INTERVAL", time.Hour), 24 * time.Hour},
		{"duration bare seconds", env.Duration("LLM_TIMEOUT_S", time.Second), 30 * time.Second},
		{"duration default", env.Duration("JWT_TTL", 12*time.Hour), 12 * time.Hour},
		{"required", env.Required("JWT_SECRET"), "s3cret"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %v want %v", tt.name, tt.got, tt.want)
		}
	}
	if got := env.List("CORS_ORIGINS", nil); !slices.Equal(got, []string{"http://localhost:5173", "https://buddy.example"}) {
		t.Errorf("List %v", got)
	}
	if got := env.List("NOPE", []string{"d"}); !slices.Equal(got, []string{"d"}) {
		t.Errorf("List default %v", got)
	}
	if err := env.Err(); err != nil {
		t.Fatalf("unexpected %v", err)
	}
}

func TestEnv_ErrListsAllProblems(t *testing.T) {
	env := New(envOf(map[string]string{"CORE_PORT": "eighty", "CRON_INTERVAL": "daily", "JWT_TTL": "-1h"}))
	if env.Int("CORE_PORT", 8080) != 8080 || env.Duration("CRON_INTERVAL", time.Hour) != time.Hour ||
		env.Duration("JWT_TTL", 12*time.Hour) != 12*time.Hour {
		t.Fatal("invalid values must fall back to defaults")
	}
	_ = env.Required("JWT_SECRET")
	_ = env.Required("CONFIRMATION_HMAC_SECRET")
	err := env.Err()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"JWT_SECRET, CONFIRMATION_HMAC_SECRET", `CORE_PORT="eighty"`, `CRON_INTERVAL="daily"`, `JWT_TTL="-1h"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q missing from %v", want, err)
		}
	}
	only := New(envOf(nil))
	_ = only.Required("X")
	if err := only.Err(); err == nil || strings.Contains(err.Error(), "invalid") {
		t.Fatalf("missing only: %v", err)
	}
}

func TestNew_NilUsesOSGetenv(t *testing.T) {
	t.Setenv("AIBUDDY_CONFIG_TEST", "from-os")
	if got := New(nil).String("AIBUDDY_CONFIG_TEST", ""); got != "from-os" {
		t.Fatalf("got %q", got)
	}
}
