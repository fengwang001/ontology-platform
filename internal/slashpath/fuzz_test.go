package slashpath

import (
	"math/rand"
	"strings"
	"testing"
)

// randomPath builds a random path from fragments such as "a", "b", ".",
// "..", "/" and a multi-byte segment, so that separators, dot segments
// and parent references appear in arbitrary combinations.
func randomPath(r *rand.Rand) string {
	frags := []string{"a", "b", ".", "..", "/", "//", "目录", ""}
	n := r.Intn(12)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(frags[r.Intn(len(frags))])
	}
	return sb.String()
}

// TestRandomIdempotence randomly generates paths and verifies the first
// invariant Clean(Clean(p)) == Clean(p), along with the other invariants
// and output shape guarantees, for every generated input.
func TestRandomIdempotence(t *testing.T) {
	seeds := []int64{1, 42, 2026, 987654321}
	for _, seed := range seeds {
		r := rand.New(rand.NewSource(seed))
		for i := 0; i < 5000; i++ {
			p := randomPath(r)
			c := Clean(p)
			if Clean(c) != c {
				t.Fatalf("seed=%d i=%d: Clean(Clean(%q)) = %q, Clean(%q) = %q",
					seed, i, p, Clean(c), p, c)
			}
			checkInvariants(t, p)
		}
	}
}

// TestRandomJoinIdempotence does the same for paths assembled via Join
// from random elements.
func TestRandomJoinIdempotence(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	elems := []string{"a", "b", ".", "..", "/", "", "目录", "/x", "y/"}
	for i := 0; i < 5000; i++ {
		n := r.Intn(5)
		parts := make([]string, n)
		for j := range parts {
			parts[j] = elems[r.Intn(len(elems))]
		}
		got := Join(parts...)
		if Clean(got) != got {
			t.Fatalf("Join(%q) = %q is not clean", parts, got)
		}
		checkInvariants(t, got)
	}
}
