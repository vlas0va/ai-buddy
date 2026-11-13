//go:build !demo

package buildflags

import "testing"

// Запустите `go test -tags demo ./...`, чтобы покрыть другую сборку.
func TestDemoOffByDefault_R55(t *testing.T) {
	if Demo {
		t.Fatal("Demo must be false without the demo build tag")
	}
}
