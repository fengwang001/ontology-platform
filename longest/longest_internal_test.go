package longest

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"ontology/balance"
	"ontology/scan"
)

// naiveLongest 朴素枚举所有子串逐个判配平，仅供测试对照，不进产出 API。
func naiveLongest(s string) (int, int) {
	st, ln := 0, 0
	for i := 0; i < len(s); i++ {
		for j := i + 2; j <= len(s); j += 2 {
			if balance.IsBalanced(s[i:j]) && j-i > ln {
				st, ln = i, j-i
			}
		}
	}
	return st, ln
}

func TestBalanceCheck(t *testing.T) {
	for _, c := range []struct {
		s  string
		ok bool
		f  balance.FailureType
		i  int
	}{
		{"", true, balance.None, 0}, {"()", true, balance.None, 0},
		{"(())()", true, balance.None, 0}, {")(", false, balance.UnexpectedRight, 0},
		{"())", false, balance.UnexpectedRight, 2}, {"(()", false, balance.UnmatchedLeft, 3},
		{"(a)", false, balance.IllegalChar, 1},
	} {
		r := balance.Check(c.s)
		if r.Balanced != c.ok || r.Failure != c.f || (!c.ok && r.Index != c.i) {
			t.Fatalf("Check(%q)=%+v want ok=%v %v@%d", c.s, r, c.ok, c.f, c.i)
		}
	}
}

func TestNaiveCrosscheck(t *testing.T) {
	sol, _ := New(1 << 20)
	var all []string
	var gen func(p string, d int)
	gen = func(p string, d int) {
		if d == 0 {
			all = append(all, p)
			return
		}
		gen(p+"(", d-1)
		gen(p+")", d-1)
	}
	gen("", 11)
	for _, s := range all {
		st, ln, e := sol.Longest(s)
		nst, nln := naiveLongest(s)
		if e != nil || st != nst || ln != nln || sol.SelfCheck(s, st, ln) != nil {
			t.Fatalf("crosscheck %q (%d,%d,%v) naive (%d,%d)", s, st, ln, e, nst, nln)
		}
	}
}

func TestTieLeftmost(t *testing.T) {
	sol, _ := New(1 << 20)
	for _, c := range []struct{ s string; st, ln int }{
		{"()())(())", 0, 4}, {")()())", 1, 4}, {"(()())()()", 0, 10}, {"()(()", 0, 2},
	} {
		st, ln, _ := sol.Longest(c.s)
		if st != c.st || ln != c.ln {
			t.Fatalf("%q got (%d,%d) want (%d,%d)", c.s, st, ln, c.st, c.ln)
		}
	}
}

func TestErrors(t *testing.T) {
	if _, e := New(0); !errors.Is(e, ErrInvalidLimit) {
		t.Fatalf("New(0)=%v", e)
	}
	if _, e := New(-3); !errors.Is(e, ErrInvalidLimit) {
		t.Fatalf("New(-3)=%v", e)
	}
	sol, _ := New(4)
	if _, _, e := sol.Longest("(())("); !errors.Is(e, ErrTooLong) {
		t.Fatalf("too long=%v", e)
	}
	var ill *scan.IllegalCharError
	if _, _, e := sol.Longest("(a)"); !errors.As(e, &ill) || ill.Index != 1 {
		t.Fatalf("illegal=%v", e)
	}
	if _, _, e := sol.Longest("()()"); e != nil {
		t.Fatalf("reuse=%v", e)
	}
	for _, c := range []struct {
		s      string
		st, ln int
		want   error
	}{
		{"()", 0, 1, ErrSubstringNotBalanced},
		{"()()", 0, 2, ErrNotLongest},
		{"()", 1, 2, ErrBadRange},
	} {
		if e := sol.SelfCheck(c.s, c.st, c.ln); !errors.Is(e, c.want) {
			t.Fatalf("SelfCheck(%s,%d,%d)=%v want %v", c.s, c.st, c.ln, e, c.want)
		}
	}
}

func TestCharCountExactlyN(t *testing.T) {
	forms := []func(int) string{
		func(n int) string { return strings.Repeat("(", n) },
		func(n int) string { return strings.Repeat(")", n) },
		func(n int) string { return strings.Repeat("()", n/2) },
		func(n int) string { return strings.Repeat("(", n/2) + strings.Repeat(")", n/2) },
	}
	for _, n := range []int{1000, 100000} {
		for fi, form := range forms {
			var visits visitCount
			_, _, e := scanOnce(form(n), &visits)
			if e != nil || int(visits) != n {
				t.Fatalf("n=%d form=%d visits=%d err=%v", n, fi, visits, e)
			}
		}
	}
}

func TestConcurrentDeterministic(t *testing.T) {
	sol, _ := New(1 << 20)
	const n = 64
	var wg sync.WaitGroup
	starts, lens := make([]int, n), make([]int, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			s, l, e := sol.Longest("())(())())((()))()(")
			if e != nil {
				t.Errorf("err=%v", e)
			}
			starts[id], lens[id] = s, l
		}(g)
	}
	wg.Wait()
	for g := 1; g < n; g++ {
		if starts[g] != starts[0] || lens[g] != lens[0] {
			t.Fatalf("g%d=(%d,%d) want (%d,%d)", g, starts[g], lens[g], starts[0], lens[0])
		}
	}
}
