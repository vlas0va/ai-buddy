//go:build demo

package buildflags

import "testing"

func TestDemoOnWithTag_R55(t *testing.T) {
	if !Demo {
		t.Fatal("Demo must be true with the demo build tag")
	}
}
