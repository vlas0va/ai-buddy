//go:build !demo

package flags

import "testing"

// Covers the default build. `go test -tags demo ./...` covers the other side
// (see flags_demo_test.go).
func TestDemoOffByDefault(t *testing.T) {
	if Demo {
		t.Fatal("Demo must be false without the demo build tag")
	}
}
