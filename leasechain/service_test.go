package leasechain

import "testing"

func TestSkeletonCompiles(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("invalid config should return nil")
	}
}
