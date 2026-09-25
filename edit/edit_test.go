package edit_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/edit"
)

func mk(ss ...string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

// lcsLen is the O(NM) dynamic-programming reference for shortest-script
// checks: distance == n + m - 2*LCS.
func lcsLen(a, b [][]byte) int {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if bytes.Equal(a[i], b[j]) {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] > dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	return dp[0][0]
}

func runScript(s edit.Script, a, b [][]byte) [][]byte {
	var out [][]byte
	for _, op := range s {
		switch op.Kind {
		case ' ':
			out = append(out, a[op.Old])
		case '-':
		case '+':
			out = append(out, b[op.New])
		}
	}
	return out
}

func TestShortestRandom(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	alpha := mk("a\n", "b\n", "c\n")
	for it := 0; it < 400; it++ {
		gen := func() [][]byte {
			n := r.Intn(9)
			out := make([][]byte, n)
			for i := range out {
				out[i] = alpha[r.Intn(len(alpha))]
			}
			return out
		}
		a, b := gen(), gen()
		s, err := edit.Diff(a, b, -1)
		if err != nil {
			t.Fatalf("iter %d: %v", it, err)
		}
		want := len(a) + len(b) - 2*lcsLen(a, b)
		if s.Distance() != want {
			t.Fatalf("iter %d: distance %d, want %d", it, s.Distance(), want)
		}
		if got := runScript(s, a, b); !bytes.Equal(bytes.Join(got, nil), bytes.Join(b, nil)) {
			t.Fatalf("iter %d: script does not build b", it)
		}
	}
}

func TestTieBreakPrefersDelete(t *testing.T) {
	cases := []struct {
		a, b [][]byte
		want string
	}{
		{mk("a\n", "b\n"), mk("b\n", "a\n"), "-a"},
		{mk("x\n"), mk("y\n"), "-x"},
	}
	for _, c := range cases {
		s, err := edit.Diff(c.a, c.b, -1)
		if err != nil {
			t.Fatal(err)
		}
		var first edit.Op
		for _, op := range s {
			if op.Kind != ' ' {
				first = op
				break
			}
		}
		if first.Kind != '-' {
			t.Fatalf("want first change '-' for %s, got %q", c.want, first.Kind)
		}
	}
}

func TestDeterministicScript(t *testing.T) {
	a := mk("1\n", "2\n", "3\n", "2\n", "3\n", "4\n")
	b := mk("2\n", "3\n", "4\n", "2\n", "3\n")
	ref, err := edit.Diff(a, b, -1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		s, err := edit.Diff(a, b, -1)
		if err != nil || fmt.Sprint(s) != fmt.Sprint(ref) {
			t.Fatalf("iteration %d differs or errors: %v", i, err)
		}
	}
}

func changedLines(n int) (a, b [][]byte) {
	a = make([][]byte, n)
	for i := range a {
		a[i] = []byte(fmt.Sprintf("L%06d\n", i))
	}
	b = append([][]byte(nil), a...)
	for _, i := range []int{n / 10, n / 2, n - 1 - n/10} {
		b[i] = []byte(fmt.Sprintf("X%06d\n", i))
	}
	return
}

func TestCounterLinear(t *testing.T) {
	var prev int64
	for _, n := range []int{1000, 100000} {
		a, b := changedLines(n)
		if _, err := edit.Diff(a, b, -1); err != nil {
			t.Fatal(err)
		}
		st := edit.Steps()
		bound := int64(4 * (len(a) + len(b)) * 7)
		if st > bound {
			t.Fatalf("n=%d steps=%d bound=%d", n, st, bound)
		}
		if prev > 0 && st > prev*150 {
			t.Fatalf("n=%d steps=%d > 150x %d", n, st, prev)
		}
		prev = st
	}
}

func TestTooLarge(t *testing.T) {
	cases := []int{0, 5, 50}
	for _, lim := range cases {
		n := 100
		a := make([][]byte, n)
		b := make([][]byte, n)
		for i := 0; i < n; i++ {
			a[i] = []byte(fmt.Sprintf("L%06d\n", i))
			b[i] = []byte(fmt.Sprintf("Q%06d\n", i))
		}
		_, err := edit.Diff(a, b, lim)
		if !errors.Is(err, edit.ErrTooLarge) {
			t.Fatalf("lim=%d want ErrTooLarge, got %v", lim, err)
		}
		if st := edit.Steps(); st > int64(4*(len(a)+len(b))*(lim+1)) {
			t.Fatalf("lim=%d steps=%d exceeds bound", lim, st)
		}
	}
}
