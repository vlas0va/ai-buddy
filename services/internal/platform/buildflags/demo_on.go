//go:build demo

// Package buildflags holds compile-time feature flags (R55). Build with `-tags demo` to enable demo-only endpoints.
package buildflags

// Demo enables demo-only endpoints such as POST /api/cron/run and PUT /api/flags/{key}.
const Demo = true
