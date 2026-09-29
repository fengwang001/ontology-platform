package prefixsum

import "testing"

func TestSkeleton(t *testing.T) {
	v := New()
	if v != nil {
		t.Fatal("skeleton")
	}
}
