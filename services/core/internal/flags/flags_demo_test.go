//go:build demo

package flags

import "testing"

func TestDemoOnWithTag(t *testing.T) {
	if !Demo {
		t.Fatal("Demo must be true with the demo build tag")
	}
}
