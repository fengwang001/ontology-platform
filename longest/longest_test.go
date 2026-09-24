package longest

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"ontology/balance"
)

func naive(s string) (int, int) { // 朴素对照：枚举所有子串逐个判配平
	best, start := 0, 0
	for i := 0; i < len(s); i++ {
		for j := i; j < len(s); j++ {
			if ok, _ := balance.IsBalanced(s[i : j+1]); ok && j+1-i > best {
				best, start = j+1-i, i
			}
		}
	}
	return start, best
}
func TestLongestTable(t *testing.T) {
	ins := []string{"", "(((", ")))", ")()())", "())()", "(())", ")(())(", "())(()())"}
	want := [][2]int{{0, 0}, {0, 0}, {0, 0}, {1, 4}, {0, 2}, {0, 4}, {1, 4}, {3, 6}}
	for i, in := range ins {
		st, ln, err := Longest(in)
		ns, nl := naive(in)
		if err != nil || [2]int{st, ln} != want[i] || st != ns || ln != nl || !SelfCheck(in, st, ln, nl) {
			t.Errorf("%q: got (%d,%d,%v), want %v, naive (%d,%d)", in, st, ln, err, want[i], ns, nl)
		}
	}
}
func TestBalanceTable(t *testing.T) {
	ins := []string{"", "()", "())", ")(", "(()", "(x)"}
	ok := []bool{true, true, false, false, false, false}
	pos := []int{0, 0, 2, 0, 0, 1}
	errs := []error{nil, nil, balance.ErrUnexpectedRight, balance.ErrUnexpectedRight, balance.ErrUnclosedLeft, balance.ErrInvalidChar}
	for i, in := range ins {
		got, err := balance.IsBalanced(in)
		var pe *balance.PosError
		badPos := errs[i] != nil && (!errors.As(err, &pe) || pe.Pos != pos[i])
		if got != ok[i] || !errors.Is(err, errs[i]) || badPos {
			t.Errorf("%q: got (%v,%v), want (%v,%v@%d)", in, got, err, ok[i], errs[i], pos[i])
		}
	}
}
func TestRejections(t *testing.T) {
	defer func() { _ = balance.SetMaxLength(1 << 20) }()
	_ = balance.SetMaxLength(4)
	st, ln, eInv := Longest("((x)")
	_, _, eLong := Longest("(((((")
	eBad := balance.SetMaxLength(0)
	if st != 0 || ln != 0 {
		t.Error("rejection returned partial result")
	}
	if errors.Is(eInv, eLong) || errors.Is(eLong, eBad) || errors.Is(eInv, eBad) {
		t.Error("rejection errors not distinct")
	}
	var pe *balance.PosError
	if !errors.Is(eInv, balance.ErrInvalidChar) || !errors.As(eInv, &pe) || pe.Pos != 2 ||
		!errors.Is(eLong, balance.ErrTooLong) || !errors.Is(eBad, balance.ErrBadLimit) {
		t.Error("wrong sentinel or lost position")
	}
	if st, ln, err := Longest("(())"); err != nil || st != 0 || ln != 4 {
		t.Error("unusable after rejection")
	}
}
func TestVisitsSinglePass(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		shapes := []string{strings.Repeat("(", n), strings.Repeat(")", n),
			strings.Repeat("()", n/2), strings.Repeat(")(", n/2)}
		for _, sh := range shapes {
			before := stats.charVisits.Load()
			_, _, err := Longest(sh)
			if got := stats.charVisits.Load() - before; err != nil || got != int64(n) {
				t.Errorf("n=%d: visits=%d, err=%v", n, got, err)
			}
		}
	}
}
func TestConcurrentDeterministic(t *testing.T) {
	in := "())(())(()())"
	got := make([][2]int, 32)
	var wg sync.WaitGroup
	for g := range got {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			st, ln, _ := Longest(in)
			got[g] = [2]int{st, ln}
		}(g)
	}
	wg.Wait()
	for _, r := range got {
		if r != got[0] {
			t.Errorf("got %v, want %v", r, got[0])
		}
	}
}
