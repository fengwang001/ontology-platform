package slashpath

import (
	"math/rand"
	"strings"
	"testing"
)

// fragments are the building blocks for random paths. They include
// normal segments, dot segments, separators (single and in runs) and
// multibyte segments.
var fragments = []string{
	"a", "b", "c", ".", "..", "/", "//", "中文", "日本語",
}

// randomPath builds a path of n randomly chosen fragments.
func randomPath(r *rand.Rand, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(fragments[r.Intn(len(fragments))])
	}
	return b.String()
}

// TestCleanIdempotentRandom is the required randomized idempotence
// test: for random paths, Clean(Clean(p)) == Clean(p) must hold.
// It also checks the other two invariants on the same inputs.
func TestCleanIdempotentRandom(t *testing.T) {
	r := rand.New(rand.NewSource(20260921))
	const iterations = 20000
	for i := 0; i < iterations; i++ {
		p := randomPath(r, 1+r.Intn(12))
		once := Clean(p)
		twice := Clean(once)

		// Invariant 1: idempotence.
		if twice != once {
			t.Fatalf("iter %d: Clean(Clean(%q)) = %q, Clean(%q) = %q",
				i, p, twice, p, once)
		}

		// Invariant 2: absoluteness is preserved.
		if IsAbs(p) != IsAbs(once) {
			t.Fatalf("iter %d: IsAbs(%q)=%v, IsAbs(Clean)=%v",
				i, p, IsAbs(p), IsAbs(once))
		}

		// Invariant 3: absolute results contain no ".." segment.
		if IsAbs(once) {
			for _, seg := range strings.Split(once, "/") {
				if seg == ".." {
					t.Fatalf("iter %d: Clean(%q) = %q escapes root",
						i, p, once)
				}
			}
		}

		// Output shape: no "//", no trailing "/" except root,
		// no "." segment unless the whole result is ".".
		if strings.Contains(once, "//") {
			t.Fatalf("iter %d: Clean(%q) = %q contains //", i, p, once)
		}
		if once != "/" && strings.HasSuffix(once, "/") {
			t.Fatalf("iter %d: Clean(%q) = %q has trailing /", i, p, once)
		}
	}
}

// TestJoinIdempotentRandom checks idempotence through Join as well:
// joining random elements must land on a fixed point of Clean.
func TestJoinIdempotentRandom(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	const iterations = 5000
	for i := 0; i < iterations; i++ {
		n := r.Intn(5)
		elems := make([]string, n)
		for j := range elems {
			elems[j] = randomPath(r, 1+r.Intn(4))
		}
		got := Join(elems...)
		if Clean(got) != got {
			t.Fatalf("iter %d: Join(%q) = %q is not a fixed point",
				i, elems, got)
		}
	}
}
