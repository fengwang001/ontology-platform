package longest

import (
	"errors"
	"ontology/balance"
	"strings"
	"sync"
	"testing"
)

func naive(s string) (start, length int) { // test-only oracle: all substrings, leftmost tie
	for i := 0; i < len(s); i++ {
		for j, d := i, 0; j < len(s) && d >= 0; j++ {
			d += 1 - 2*int(s[j]&1) // '(' is even, ')' is odd
			if d == 0 && j-i+1 > length {
				start, length = i, j-i+1
			}
		}
	}
	return
}
func shapes(n int) []string {
	return []string{strings.Repeat("(", n), strings.Repeat(")", n), strings.Repeat("(", n/2) + strings.Repeat(")", n/2), strings.Repeat("()", n/2)}
}
func solver(limit int) *Solver {
	chk, _ := balance.New(limit) // limits below are always positive
	return New(chk)
}
func TestIsBalanced(t *testing.T) {
	chk, _ := balance.New(1 << 20)
	cases := []struct {
		s    string
		pos  int // -1 means balanced
		kind balance.FailKind
	}{{"()(()())", -1, 0}, {"())(", 2, balance.ExcessRight}, {"(()", 0, balance.UnclosedLeft}}
	for _, c := range cases {
		ok, err := chk.IsBalanced(c.s)
		var f *balance.Failure
		if ok != (c.pos < 0) || !ok && (!errors.As(err, &f) || f.Pos != c.pos || f.Kind != c.kind) {
			t.Errorf("%q: %v, %v", c.s, ok, err)
		}
	}
}
func TestLongestVsNaive(t *testing.T) {
	sv := solver(1 << 20)
	cases := []string{")()())", "", "(((", ")))", "()(())", "())()", "()(", ")()()(", "(()))(())"}
	for _, n := range []int{100, 200} {
		cases = append(cases, shapes(n)...)
	}
	for _, s := range cases {
		ns, nl := naive(s)
		if st, ln, err := sv.Longest(s); err != nil || st != ns || ln != nl || !sv.SelfCheck(s, st, ln) {
			t.Errorf("%q: (%d,%d) naive (%d,%d)", s, st, ln, ns, nl)
		}
	}
	if sv.SelfCheck(")()())", 0, 4) || sv.SelfCheck(")()())", 1, 2) || sv.SelfCheck(")()())", -1, 4) {
		t.Error("SelfCheck accepted a tampered result")
	}
}
func TestErrors(t *testing.T) {
	for _, lim := range []int{0, -3} {
		if _, err := balance.New(lim); !errors.Is(err, balance.ErrInvalidLimit) {
			t.Errorf("New(%d): %v", lim, err)
		}
	}
	sv := solver(1 << 20)
	st, ln, err := sv.Longest("(a)")
	badChar := !errors.Is(err, balance.ErrInvalidChar) || !strings.Contains(err.Error(), "1") || st+ln != 0
	st, ln, err = solver(4).Longest("()()()")
	badLong := !errors.Is(err, balance.ErrTooLong) || errors.Is(err, balance.ErrInvalidChar) || st+ln != 0
	if st, ln, err = sv.Longest("()"); badChar || badLong || err != nil || st != 0 || ln != 2 {
		t.Errorf("char=%v long=%v reuse=(%d,%d,%v)", badChar, badLong, st, ln, err)
	}
}
func TestVisits(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		for _, s := range shapes(n) {
			sv := solver(n)
			if _, _, err := sv.Longest(s); err != nil || sv.visits.Load() != int64(n) {
				t.Errorf("n=%d: visits=%d", n, sv.visits.Load())
			}
		}
	}
}
func TestConcurrent(t *testing.T) {
	sv := solver(1 << 20)
	in := "())(())(()"
	ws, wl, _ := sv.Longest(in)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st, ln, err := sv.Longest(in); err != nil || st != ws || ln != wl || !sv.SelfCheck(in, st, ln) {
				t.Errorf("got (%d,%d,%v) want (%d,%d)", st, ln, err, ws, wl)
			}
		}()
	}
	wg.Wait()
}
