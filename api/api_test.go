package api

import "testing"

// TestSelfCheck runs the public built-in check of all four invariants.
func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
