package edit_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"ontology/edit"
)

func lcsLen(a, b []string) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	return dp[0][0]
}

func dist(ops []edit.Op) int {
	d := 0
	for _, op := range ops {
		if op.Kind != ' ' {
			d++
		}
	}
	return d
}

func TestShortest(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		a := make([]string, rng.Intn(10))
		b := make([]string, rng.Intn(10))
		for i := range a {
			a[i] = fmt.Sprint(rng.Intn(3))
		}
		for i := range b {
			b[i] = fmt.Sprint(rng.Intn(3))
		}
		ops, err := edit.Diff(a, b, -1)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := dist(ops), len(a)+len(b)-2*lcsLen(a, b); got != want {
			t.Fatalf("a=%v b=%v: distance %d, want %d", a, b, got, want)
		}
		var out []string
		for _, op := range ops {
			if op.Kind != '-' {
				out = append(out, op.Line)
			}
		}
		if !slices.Equal(out, b) {
			t.Fatalf("a=%v b=%v: script yields %v", a, b, out)
		}
	}
}

func TestStepsBound(t *testing.T) {
	steps := map[int]int{}
	for _, n := range []int{1000, 100000} {
		a := make([]string, n)
		for i := range a {
			a[i] = fmt.Sprintf("line %d", i)
		}
		b := slices.Clone(a)
		b[n/4], b[n/2], b[3*n/4] = "x", "y", "z"
		ops, err := edit.Diff(a, b, -1)
		if err != nil {
			t.Fatal(err)
		}
		d := dist(ops)
		steps[n] = edit.LastSteps()
		if bound := 4 * (n + len(b)) * (d + 1); steps[n] > bound {
			t.Fatalf("n=%d: steps %d > bound %d", n, steps[n], bound)
		}
	}
	if steps[100000] > 150*steps[1000] {
		t.Fatalf("steps ratio too big: %d vs %d", steps[100000], steps[1000])
	}
	t.Logf("steps: n=1000 -> %d, n=100000 -> %d", steps[1000], steps[100000])
}

func TestMaxDist(t *testing.T) {
	a := []string{"1", "2", "3", "4", "5"}
	b := []string{"a", "b", "c", "d", "e"}
	_, err := edit.Diff(a, b, 3)
	if !errors.Is(err, edit.ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	if bound := 4 * (len(a) + len(b)) * 4; edit.LastSteps() > bound {
		t.Fatalf("steps %d exceed bound %d", edit.LastSteps(), bound)
	}
}
