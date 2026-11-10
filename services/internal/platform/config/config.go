// Package config reads service settings from environment variables. An Env collects every missing or invalid
// variable, so a service reports all configuration problems at once and refuses to start (docs/backend §7).
//
//	env := config.New(os.Getenv)
//	cfg := Config{Port: env.Int("CORE_PORT", 8080), JWTSecret: env.Required("JWT_SECRET")}
//	if err := env.Err(); err != nil { return err }
//
// Values are trimmed; an empty value counts as unset (compose often passes VAR= ).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env reads variables through a getenv function and records problems for Err. Not safe for concurrent use.
type Env struct {
	getenv  func(string) string
	missing []string
	invalid []string
}

// New returns an Env over getenv; nil means os.Getenv.
func New(getenv func(string) string) *Env {
	if getenv == nil {
		getenv = os.Getenv
	}
	return &Env{getenv: getenv}
}

func (e *Env) get(key string) string { return strings.TrimSpace(e.getenv(key)) }

// String returns the value of key, or def when it is unset.
func (e *Env) String(key, def string) string {
	if v := e.get(key); v != "" {
		return v
	}
	return def
}

// Required returns the value of key; when it is unset the key is recorded as missing and "" is returned.
func (e *Env) Required(key string) string {
	v := e.get(key)
	if v == "" {
		e.missing = append(e.missing, key)
	}
	return v
}

// Int returns key as a decimal integer, def when unset; an unparsable value is recorded as invalid (def returned).
func (e *Env) Int(key string, def int) int {
	v := e.get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.invalid = append(e.invalid, fmt.Sprintf("%s=%q (want an integer)", key, v))
		return def
	}
	return n
}

// Duration returns key as a Go duration ("24h", "90s") or a bare number of seconds ("30"), def when unset;
// an unparsable or negative value is recorded as invalid (def returned).
func (e *Env) Duration(key string, def time.Duration) time.Duration {
	v := e.get(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		secs, aerr := strconv.Atoi(v)
		if aerr != nil {
			e.invalid = append(e.invalid, fmt.Sprintf("%s=%q (want a duration like 30s or 24h)", key, v))
			return def
		}
		d = time.Duration(secs) * time.Second
	}
	if d < 0 {
		e.invalid = append(e.invalid, fmt.Sprintf("%s=%q (must not be negative)", key, v))
		return def
	}
	return d
}

// List returns key split on commas with blanks dropped (e.g. CORS_ORIGINS), def when unset or empty.
func (e *Env) List(key string, def []string) []string {
	var out []string
	for _, part := range strings.Split(e.get(key), ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// Err returns nil, or one error listing every missing required variable and every invalid value read so far.
func (e *Env) Err() error {
	var parts []string
	if len(e.missing) > 0 {
		parts = append(parts, "missing required environment variables: "+strings.Join(e.missing, ", "))
	}
	if len(e.invalid) > 0 {
		parts = append(parts, "invalid values: "+strings.Join(e.invalid, ", "))
	}
	if len(parts) == 0 {
		return nil
	}
	return fmt.Errorf("config: %s", strings.Join(parts, "; "))
}
