package check_test

import (
	"errors"
	"ontology/arr"
	"ontology/check"
	"ontology/kad"
	"sync"
	"testing"
)

var cases = []struct {
	name string
	in   []int
}{
	{"mixed", []int{-2, 1, -3, 4, -1, 2, 1, -5, 4}},
	{"all negative", []int{-2, -3, -1}},
	{"all positive", []int{1, 2, 3, 4}},
	{"zeros", []int{0, 0, 0}},
}

func TestSumMatchesNaive(t *testing.T) {
	for _, c := range cases {
		if got, want := kad.MaxSubarraySum(c.in), check.NaiveMaxSum(c.in); got != want {
			t.Errorf("%s: sum=%d, naive=%d", c.name, got, want)
		}
	}
}
func TestNonEmptySemantics(t *testing.T) {
	if got := kad.MaxSubarraySum([]int{-2, -3, -1}); got != -1 {
		t.Errorf("non-empty kadane = %d, want -1", got)
	}
	wrong, cur := 0, 0
	for _, v := range []int{-2, -3, -1} { // buggy variant: clamp to 0 allows empty
		cur = max(cur+v, 0)
		wrong = max(wrong, cur)
	}
	if wrong != 0 {
		t.Errorf("clamp-to-0 variant = %d, want 0", wrong)
	}
}
func TestRangeConsistent(t *testing.T) {
	for _, c := range cases {
		lo, hi, sum := kad.MaxSubarrayRange(c.in)
		got := 0
		for _, v := range c.in[lo:hi] {
			got += v
		}
		if got != sum || sum != check.NaiveMaxSum(c.in) || lo >= hi {
			t.Errorf("%s: range [%d,%d) sum %d inconsistent", c.name, lo, hi, sum)
		}
	}
}
func TestSentinelErrors(t *testing.T) {
	for _, c := range []struct {
		in  []int
		err error
	}{{nil, arr.ErrNil}, {[]int{}, arr.ErrEmpty}, {make([]int, arr.MaxLen+1), arr.ErrTooLarge}} {
		if _, err := arr.MaxSum(c.in); !errors.Is(err, c.err) {
			t.Errorf("MaxSum: got %v, want %v", err, c.err)
		}
		if _, _, _, err := arr.MaxRange(c.in); !errors.Is(err, c.err) {
			t.Errorf("MaxRange: got %v, want %v", err, c.err)
		}
	}
}
func TestBoundaries(t *testing.T) {
	for _, c := range []struct {
		in          []int
		lo, hi, sum int
	}{{[]int{42}, 0, 1, 42}, {[]int{1, 2, 3, 4}, 0, 4, 10}} {
		if lo, hi, sum := kad.MaxSubarrayRange(c.in); lo != c.lo || hi != c.hi || sum != c.sum {
			t.Errorf("range(%v) = [%d,%d)=%d", c.in, lo, hi, sum)
		}
	}
}
func TestLinearAccess(t *testing.T) {
	kad.ResetAccessCount()
	kad.MaxSubarraySum(make([]int, 100000))
	if got := kad.AccessCount(); got > 100000 {
		t.Errorf("accesses = %d, want <= 100000", got)
	}
}
func TestConcurrentPure(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(64)
	for range 64 {
		go func() {
			defer wg.Done()
			if kad.MaxSubarraySum(cases[0].in) != check.NaiveMaxSum(cases[0].in) {
				t.Error("concurrent result mismatch")
			}
		}()
	}
	wg.Wait()
}
