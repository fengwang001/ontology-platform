package api

import "testing"

func TestDebugSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}
