package blockstore

import (
	"fmt"
	"testing"
)

func dstr(n int) Digest { return Digest(fmt.Sprintf("blk-%03d", n)) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func contains[S ~[]E, E comparable](set S, v E) bool {
	for _, x := range set {
		if x == v {
			return true
		}
	}
	return false
}
