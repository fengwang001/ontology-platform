package edit_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/edit"
)

func lcsLen(a, b []string) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}
	return dp[len(a)][len(b)]
}

func dist(ops []edit.Op) (d int) {
	for _, op := range ops {
		if op.Kind != ' ' {
			d++
		}
	}
	return
}

func randLines(r *rand.Rand, n int) []string {
	ls := make([]string, n)
	for i := range ls {
		ls[i] = string(rune('a'+r.Intn(4))) + "\n"
	}
	return ls
}

func TestMinimal(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		a, b := randLines(r, r.Intn(9)), randLines(r, r.Intn(9))
		ops, err := edit.Diff(a, b, -1)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := dist(ops), len(a)+len(b)-2*lcsLen(a, b); got != want {
			t.Fatalf("a=%q b=%q: dist %d, want minimal %d", a, b, got, want)
		}
	}
}

func TestDeterministic(t *testing.T) {
	cases := [][2][]string{
		{{"a\n", "b\n"}, {"b\n", "a\n"}},
		{{"x\n", "x\n", "y\n"}, {"y\n", "x\n", "x\n"}},
		{{"a\n", "b\n", "c\n", "a\n"}, {"a\n", "c\n", "b\n", "a\n"}},
	}
	for _, c := range cases {
		first, _ := edit.Diff(c[0], c[1], -1)
		for i := 0; i < 100; i++ {
			ops, _ := edit.Diff(c[0], c[1], -1)
			if fmt.Sprint(ops) != fmt.Sprint(first) {
				t.Fatalf("nondeterministic: %q -> %q", c[0], c[1])
			}
		}
	}
	// tie-break: deletions come before insertions
	ops, _ := edit.Diff([]string{"a\n", "b\n"}, []string{"b\n", "a\n"}, -1)
	want := []edit.Op{{'-', "a\n"}, {' ', "b\n"}, {'+', "a\n"}}
	if fmt.Sprint(ops) != fmt.Sprint(want) {
		t.Fatalf("tie-break ops = %v, want %v", ops, want)
	}
}

func TestLimit(t *testing.T) {
	a := []string{"1\n", "2\n", "3\n"}
	b := []string{"4\n", "5\n", "6\n"}
	if _, err := edit.Diff(a, b, 2); !errors.Is(err, edit.ErrTooBig) {
		t.Fatalf("want ErrTooBig, got %v", err)
	}
	bound := int64(4 * (len(a) + len(b)) * (2 + 1))
	if s := edit.Steps(); s > bound {
		t.Fatalf("steps %d > bound %d under limit", s, bound)
	}
	if ops, err := edit.Diff(a, b, 6); err != nil || dist(ops) != 6 {
		t.Fatalf("limit 6: ops=%v err=%v", ops, err)
	}
}

func TestStepsScale(t *testing.T) {
	var prev int64
	for _, n := range []int{1000, 100000} {
		a := make([]string, n)
		for i := range a {
			a[i] = fmt.Sprintf("line-%d\n", i)
		}
		b := make([]string, n)
		copy(b, a)
		for _, i := range []int{n / 4, n / 2, 3 * n / 4} {
			b[i] = "changed\n"
		}
		ops, err := edit.Diff(a, b, -1)
		if err != nil {
			t.Fatal(err)
		}
		d := int64(dist(ops))
		s := edit.Steps()
		if bound := 4 * int64(n+n) * (d + 1); s > bound {
			t.Fatalf("n=%d: steps %d > bound %d", n, s, bound)
		}
		if prev > 0 && s > 150*prev {
			t.Fatalf("n=%d: steps %d > 150x previous %d (not near-linear)", n, s, prev)
		}
		prev = s
	}
}
