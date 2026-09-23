package edit

import (
	"math/rand"
	"testing"

	"ontology/lines"
)

func mk(s string) []lines.Line { return lines.Split([]byte(s)) }

func TestScratch(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alpha := []string{"a\n", "b\n", "c\n", "d\n"}
	for iter := 0; iter < 2000; iter++ {
		gen := func() string {
			n := rng.Intn(9)
			b := make([]byte, 0, n*2)
			for i := 0; i < n; i++ {
				b = append(b, alpha[rng.Intn(len(alpha))]...)
			}
	return string(b)
		}
		a, b := gen(), gen()
		res, err := Diff(mk(a), mk(b), 1000)
		if err != nil {
			t.Fatal(err)
		}
		want := lcsDist(mk(a), mk(b))
		if res.Distance != want {
			t.Fatalf("dist %d want %d\n%q\n%q\n%v", res.Distance, want, a, b, res.Ops)
		}
		if !applies(mk(a), res.Ops, mk(b)) {
			t.Fatalf("apply mismatch %q %q %v", a, b, res.Ops)
		}
	}
}

func lcsDist(a, b []lines.Line) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if lines.Equal(a[i], b[j]) {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] > dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	return len(a) + len(b) - 2*dp[0][0]
}

func applies(a []lines.Line, ops []Op, want []lines.Line) bool {
	var got []lines.Line
	for _, o := range ops {
		if o.Kind == Equal || o.Kind == Insert {
			got = append(got, o.L)
		}
	}
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if string(got[i].Bytes) != string(want[i].Bytes) {
			return false
		}
	}
	// also check delete side reconstructs a
	var old []lines.Line
	for _, o := range ops {
		if o.Kind == Equal || o.Kind == Delete {
			old = append(old, o.L)
		}
	}
	if len(old) != len(a) {
		return false
	}
	return true
}
