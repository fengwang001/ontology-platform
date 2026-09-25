package ses

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// TestRouteChecksConstant is a white-box test: it reads the unexported
// counter directly. The counter must never be reachable via an exported API.
func TestRouteChecksConstant(t *testing.T) {
	cases := []struct {
		name string
		m    int
	}{
		{"100", 100},
		{"1000", 1000},
		{"10000", 10000},
	}
	rng := rand.New(rand.NewSource(1)) // deterministic pseudo-random target
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, s := NewCluster(), NewSession()
			for i := 0; i < tc.m; i++ {
				c.master.Advance(fmt.Sprintf("k%05d", i), "x")
			}
			target := fmt.Sprintf("k%05d", rng.Intn(tc.m)) // one of the m keys
			if _, err := c.Write(s, target, "v"); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, _, err := c.Read(s, target); err != nil {
				t.Fatalf("read: %v", err)
			}
			if got := c.checks.Load(); got != 1 {
				t.Fatalf("routing checked %d (replica,key) entries at m=%d, want constant 1", got, tc.m)
			}
		})
	}
}

// TestRoutingFallback is table-driven: sticky R1 serves only when caught up to
// the session write version; otherwise R0 must serve the freshest value.
func TestRoutingFallback(t *testing.T) {
	cases := []struct {
		name   string
		sync   []int // Sync calls performed after each write, in order
		writes []string
		wantV  string
	}{
		{"no-sync-falls-to-master", nil, []string{"A"}, "A"},
		{"caught-up-r1-serves", []int{1}, []string{"A"}, "A"},
		{"stale-r1-after-rewrite", []int{1}, []string{"A", "B"}, "B"},
		{"other-follower-sync-irrelevant", []int{2}, []string{"A"}, "A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, s := NewCluster(), NewSession()
			for i, val := range tc.writes {
				if _, err := c.Write(s, "k", val); err != nil {
					t.Fatal(err)
				}
				if i < len(tc.sync) {
					if err := c.Sync(tc.sync[i]); err != nil {
						t.Fatal(err)
					}
				}
			}
			got, _, err := c.Read(s, "k")
			if err != nil || got != tc.wantV {
				t.Fatalf("read = %q, %v; want %q", got, err, tc.wantV)
			}
		})
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	if errors.Is(ErrEmptyKey, ErrSyncIndex) || errors.Is(ErrEmptyKey, ErrSessionClosed) ||
		errors.Is(ErrSyncIndex, ErrSessionClosed) {
		t.Fatal("the three fault sentinels must be mutually distinct")
	}
}
