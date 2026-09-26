package dp

import (
	"math/rand"
	"strings"
	"testing"
)

func TestSolveNaive(t *testing.T) {
	cases := []struct {
		a, b  string
		l, st int
	}{
		{"banana", "ananas", 5, 1},
		{"abcx", "abc", 3, 0},
		{"abcde", "abfce", 2, 0},
		{"aaaa", "aa", 2, 0},
		{"x", "y", 0, 0},
		{"abab", "xab", 2, 0},
		{"hello", "world", 1, 2}, // only single chars match; 'l' first at a[2]
	}
	for _, c := range cases {
		if l, st := New(c.a).Solve(c.b); l != c.l || st != c.st {
			t.Errorf("Solve(%q,%q)=(%d,%d), want (%d,%d)", c.a, c.b, l, st, c.l, c.st)
		}
	}

	rng := rand.New(rand.NewSource(1))
	alpha := "abc"
	for iter := 0; iter < 300; iter++ {
		a := randStr(rng, alpha, 1+rng.Intn(30))
		b := randStr(rng, alpha, 1+rng.Intn(30))
		gotL, gotS := New(a).Solve(b)
		wantL, wantS := naive(a, b)
		if gotL != wantL || gotS != wantS {
			t.Fatalf("Solve(%q,%q)=(%d,%d), naive=(%d,%d)", a, b, gotL, gotS, wantL, wantS)
		}
	}
}

func TestRollingVsFullTable(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	alpha := "abx"
	for iter := 0; iter < 200; iter++ {
		a := randStr(rng, alpha, 1+rng.Intn(40))
		b := randStr(rng, alpha, 1+rng.Intn(40))
		l0, s0 := New(a).Solve(b)
		l1, s1 := fullTable(a, b)
		if l0 != l1 || s0 != s1 {
			t.Fatalf("rolling(%q,%q)=(%d,%d), table=(%d,%d)", a, b, l0, s0, l1, s1)
		}
	}
}

func TestRetainedCells(t *testing.T) {
	const n = 100
	sol := New(strings.Repeat("ab", n/2))
	prev := 0
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		b := strings.Repeat("ba", m/2)
		if l, _ := sol.Solve(b); l == 0 {
			t.Fatalf("m=%d: zero length match", m)
		}
		got := int(sol.cells.Load())
		want := min(n, m) + 1
		if got != want {
			t.Fatalf("m=%d: retained %d cells, want %d", m, got, want)
		}
		if m > n && prev != 0 && got != prev {
			t.Fatalf("cells grew with m after m>n: %d -> %d", prev, got)
		}
		prev = got
	}
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func randStr(rng *rand.Rand, alpha string, k int) string {
	var sb strings.Builder
	for i := 0; i < k; i++ {
		sb.WriteByte(alpha[rng.Intn(len(alpha))])
	}
	return sb.String()
}
