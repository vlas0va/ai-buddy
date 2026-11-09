//go:build !demo

package buildflags

import "testing"

// Run `go test -tags demo ./...` to cover the other build.
func TestDemoOffByDefault_R55(t *testing.T) {
	if Demo {
		t.Fatal("Demo must be false without the demo build tag")
	}
}
