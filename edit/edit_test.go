package edit_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/edit"
)

func randLines(r *rand.Rand, n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte{byte('a' + r.Intn(3)), '\n'}
	}
	return out
}

// dpDist computes the minimal edit distance (del+ins) by O(NM) LCS DP.
func dpDist(a, b [][]byte) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if bytes.Equal(a[i], b[j]) {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return len(a) + len(b) - 2*dp[0][0]
}

// replay applies a script to a and returns the result; it must equal b.
func replay(a, b [][]byte, ops []edit.Op) ([]byte, error) {
	var out [][]byte
	ai, bi := 0, 0
	for _, op := range ops {
		switch op.Kind {
		case edit.Keep:
			if ai >= len(a) || bi >= len(b) || !bytes.Equal(a[ai], b[bi]) {
				return nil, fmt.Errorf("bad keep at %d,%d", ai, bi)
			}
			out = append(out, a[ai])
			ai++
			bi++
		case edit.Del:
			ai++
		case edit.Ins:
			out = append(out, b[bi])
			bi++
		}
	}
	if ai != len(a) || bi != len(b) {
		return nil, fmt.Errorf("script does not cover input")
	}
	return bytes.Join(out, nil), nil
}

func TestMinimal(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		a := randLines(r, r.Intn(9))
		b := randLines(r, r.Intn(9))
		ops, err := edit.Diff(a, b, 0)
		if err != nil {
			t.Fatal(err)
		}
		d := 0
		for _, op := range ops {
			if op.Kind != edit.Keep {
				d++
			}
		}
		if d != dpDist(a, b) {
			t.Fatalf("dist %d != dp %d for %q -> %q", d, dpDist(a, b), a, b)
		}
		got, err := replay(a, b, ops)
		if err != nil || !bytes.Equal(got, bytes.Join(b, nil)) {
			t.Fatalf("replay failed: %v", err)
		}
	}
}

func TestStepsBound(t *testing.T) {
	var prev int64
	for i, n := range []int{1000, 100000} {
		a := make([][]byte, n)
		for j := range a {
			a[j] = []byte(fmt.Sprintf("line%d\n", j))
		}
		b := make([][]byte, n)
		copy(b, a)
		for _, j := range []int{n / 10, n / 2, 9 * n / 10} {
			b[j] = []byte("changed\n")
		}
		ops, err := edit.Diff(a, b, 0)
		if err != nil {
			t.Fatal(err)
		}
		d := 0
		for _, op := range ops {
			if op.Kind != edit.Keep {
				d++
			}
		}
		s := edit.Steps()
		if bound := int64(4 * (n + n) * (d + 1)); s > bound {
			t.Fatalf("n=%d steps %d > bound %d", n, s, bound)
		}
		if i == 1 && s > 150*prev {
			t.Fatalf("steps ratio too big: %d vs %d", s, prev)
		}
		prev = s
		t.Logf("n=%d D=%d steps=%d", n, d, s)
	}
}

func TestMaxDist(t *testing.T) {
	a := randLines(rand.New(rand.NewSource(2)), 30)
	b := randLines(rand.New(rand.NewSource(3)), 30)
	if _, err := edit.Diff(a, b, 2); !errors.Is(err, edit.ErrTooBig) {
		t.Fatalf("want ErrTooBig, got %v", err)
	}
	if s := edit.Steps(); s > int64(4*(30+30)*(2+2)) {
		t.Fatalf("counter %d exceeds bound after ErrTooBig", s)
	}
}
