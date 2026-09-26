package sam

import (
	"math/rand"
	"testing"
)

func randStr(r *rand.Rand, n int, alpha string) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}

// TestStateCountLinear proves the state count grows linearly:
// n+1 <= states <= 2n for every size tier, not O(n^2).
func TestStateCountLinear(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	cases := []struct {
		n     int
		alpha string
	}{
		{100, "abcde"}, {500, "abcde"}, {1000, "abcde"},
		{5000, "abcde"}, {10000, "abcde"},
		{100, "a"}, {10000, "a"}, // worst case: exactly n+1 states
		{10000, "ab"},
	}
	for _, c := range cases {
		a, err := New(randStr(r, c.n, c.alpha))
		if err != nil {
			t.Fatalf("n=%d: %v", c.n, err)
		}
		if a.nstates < c.n+1 || a.nstates > 2*c.n {
			t.Errorf("n=%d alpha=%q: states=%d outside [%d, %d]",
				c.n, c.alpha, a.nstates, c.n+1, 2*c.n)
		}
		if !a.WithinStateBound() {
			t.Errorf("n=%d alpha=%q: WithinStateBound reports false", c.n, c.alpha)
		}
	}
}

// TestLinkTreeInvariants checks, for every state: len[v] > len[link[v]],
// the link chain reaches the root without cycles, and every transition
// v --c--> u satisfies len[u] >= len[v]+1.
func TestLinkTreeInvariants(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for _, n := range []int{1, 2, 5, 50, 300} {
		a, err := New(randStr(r, n, "abc"))
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		for v := 1; v < len(a.st); v++ {
			if a.st[v].length <= a.st[a.st[v].link].length {
				t.Errorf("n=%d state %d: len %d <= len[link] %d",
					n, v, a.st[v].length, a.st[a.st[v].link].length)
			}
			seen := map[int]bool{}
			for u := v; u != 0; u = a.st[u].link {
				if u < 0 || u >= len(a.st) {
					t.Fatalf("n=%d state %d: link chain escapes to %d", n, v, u)
				}
				if seen[u] {
					t.Fatalf("n=%d state %d: link cycle at %d", n, v, u)
				}
				seen[u] = true
			}
		}
		for v := range a.st {
			for c, u := range a.st[v].next {
				if a.st[u].length < a.st[v].length+1 {
					t.Errorf("n=%d: transition %d --%q--> %d has len %d < %d",
						n, v, c, u, a.st[u].length, a.st[v].length+1)
				}
			}
		}
	}
}
