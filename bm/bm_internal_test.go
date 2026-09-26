package bm

import (
	"strings"
	"testing"
)

// White-box test: the comparison counter is unexported and is read here
// inside the package only; no exported function ever exposes its value.
func TestComparisonCounterSublinear(t *testing.T) {
	pat := "abcdefghij" // m distinct bytes; trailing 'j' never occurs below
	m := len(pat)
	rows := []int{100, 500, 1000, 5000, 10000}
	for _, n := range rows {
		e := New(pat)
		e.Search(strings.Repeat("z", n))
		bound := n/m + m // allowed by the task: sublinear, about n/m
		if e.cmp <= 0 {
			t.Fatalf("n=%d: counter never advanced", n)
		}
		if e.cmp > bound {
			t.Errorf("n=%d: %d comparisons > bound %d (grew linearly?)", n, e.cmp, bound)
		}
	}
}

func TestComparisonCounterResets(t *testing.T) {
	e := New("abc")
	e.Search("abcabc")
	first := e.cmp
	e.Search("bbbb") // two windows: 'b' at j=2 jumps 1 each, one comparison per window
	if e.cmp >= first {
		t.Fatalf("counter not reset between searches: %d then %d", first, e.cmp)
	}
	if e.cmp != 2 { // two windows, one mismatch comparison each
		t.Errorf("counter=%d, want 2", e.cmp)
	}
}
