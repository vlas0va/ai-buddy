package config

import "testing"

func TestLoad(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want int
	}{
		{"default", nil, 8101},
		{"override", map[string]string{"MCP_ABSENCE_PORT": "9001"}, 9001},
		{"bad", map[string]string{"MCP_ABSENCE_PORT": "x"}, 8101},
		{"out of range", map[string]string{"MCP_ABSENCE_PORT": "0"}, 8101},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Load(func(k string) string { return tc.env[k] }).Port; got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}
