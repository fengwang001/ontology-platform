package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/lex"
	"ontology/pratt"
)

type evalCase struct {
	s    string
	want int64
}

func ipow(b, e int64) int64 {
	v := int64(1)
	for ; e > 0; e-- {
		v *= b
	}
	return v
}

// TestReferenceConsistency pins invariant 1 (fixed cases + random templates).
func TestReferenceConsistency(t *testing.T) {
	fixed := []evalCase{
		{"2^3^2", 512}, {"10-4-3", 3}, {"-2^2", 4}, {"2^10", 1024},
		{"- -3", 3}, {"1- -2", 3}, {"(1+2)^2", 9}, {"(0-7)/2", -3},
		{"0^0", 1}, {"2+3*4", 14},
	}
	for _, c := range fixed {
		if got, err := Eval(c.s); err != nil || got != c.want {
			t.Errorf("%s = %d, %v; want %d", c.s, got, err, c.want)
		}
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 100; i++ {
		a, b, c, d := r.Int63n(50), r.Int63n(50), r.Int63n(4), 1+r.Int63n(9)
		fuzz := []evalCase{
			{fmt.Sprintf("%d+%d*%d", a, b, c), a + b*c},
			{fmt.Sprintf("(%d+%d)*%d", a, b, c), (a + b) * c},
			{fmt.Sprintf("%d-%d-%d", a, b, c), a - b - c},
			{fmt.Sprintf("%d/%d", a, d), a / d},
			{fmt.Sprintf("-%d^%d", a, c), ipow(-a, c)},
			{fmt.Sprintf("%d^%d^%d", a, c, 2), ipow(a, ipow(c, 2))},
			{fmt.Sprintf("%d*(%d-%d)^%d", a, b, c, 2), a * ipow(b-c, 2)},
		}
		for _, f := range fuzz {
			if got, err := Eval(f.s); err != nil || got != f.want {
				t.Fatalf("%s = %d, %v; want %d", f.s, got, err, f.want)
			}
		}
	}
}

// TestPowerRightAssoc pins invariant 2.
func TestPowerRightAssoc(t *testing.T) {
	for _, c := range []evalCase{
		{"2^3^2", 512}, // 2^(3^2), not (2^3)^2
		{"2^2^3", 256}, // 2^(2^3)
	} {
		if got, err := Eval(c.s); err != nil || got != c.want {
			t.Errorf("%s = %d, %v; want %d", c.s, got, err, c.want)
		}
	}
}

// TestUnaryVsBinary pins invariant 3.
func TestUnaryVsBinary(t *testing.T) {
	for _, c := range []evalCase{
		{"- -3", 3},  // stacked prefix minus
		{"1- -2", 3}, // infix minus, then prefix minus
		{"-2^2", 4},  // prefix minus binds tighter than ^
		{"5- -3", 8}, // infix minus, then prefix minus
	} {
		if got, err := Eval(c.s); err != nil || got != c.want {
			t.Errorf("%s = %d, %v; want %d", c.s, got, err, c.want)
		}
	}
}

// TestFailuresLeaveNoTrace pins invariant 4.
func TestFailuresLeaveNoTrace(t *testing.T) {
	sents := []error{lex.ErrIllegalChar, pratt.ErrParens, pratt.ErrExponent, pratt.ErrDivZero}
	for _, c := range []struct {
		s    string
		sent error
	}{
		{"1&2", lex.ErrIllegalChar},
		{"99999999999999999999", lex.ErrNumberOverflow},
		{"(1+2", pratt.ErrParens},
		{"1+2)", pratt.ErrParens},
		{"2^(0-1)", pratt.ErrExponent},
		{"1/0", pratt.ErrDivZero},
		{"", pratt.ErrSyntax},
		{"2^63", pratt.ErrOverflow},
	} {
		_, err := Eval(c.s)
		if !errors.Is(err, c.sent) {
			t.Errorf("%q: got %v, want %v", c.s, err, c.sent)
		}
		for _, o := range sents { // the four classes are mutually distinct
			if o != c.sent && errors.Is(err, o) {
				t.Errorf("%q: %v unexpectedly matches %v", c.s, err, o)
			}
		}
		if v, err := Eval("2+3"); err != nil || v != 5 {
			t.Fatalf("state corrupted after rejecting %q", c.s)
		}
	}
}

// TestParseExprEarlyStop pins section 4: at min_bp 40 the loop stops
// after one comparison for m = 100..10000; counter stays unexported.
func TestParseExprEarlyStop(t *testing.T) {
	if err := pratt.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	if err := SelfCheck(); err != nil { // public self-check method as well
		t.Fatal(err)
	}
}

// TestConcurrentEval pins section 6: N goroutines, same values.
func TestConcurrentEval(t *testing.T) {
	exprs := []string{"2^3^2", "-2^2", "(1+2)^2", "10-4-3", "2^10", "- -3"}
	want := []int64{512, 4, 9, 3, 1024, 3}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, e := range exprs {
				n, err := Parse(e)
				if err != nil {
					t.Error(e, err)
					continue
				}
				if got, err := EvalNode(n); err != nil || got != want[i] {
					t.Errorf("%s = %d, %v; want %d", e, got, err, want[i])
				}
			}
		}()
	}
	wg.Wait()
}
