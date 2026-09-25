package check

import (
	"errors"
	"fmt"
	"testing"

	"ontology/rng"
	"ontology/shuffle"
)

func must(t *testing.T, ok bool, msg string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(msg, args...)
	}
}

func TestRNG(t *testing.T) {
	for _, b := range []int{4, 1, 7} {
		x, _ := rng.New(42).Intn(b)
		y, _ := rng.New(42).Intn(b)
		must(t, x == y && x >= 0 && x < b, "Intn(%d)=%d,%d", b, x, y)
	}
	for _, b := range []int{0, -1} {
		_, err := rng.New(1).Intn(b)
		must(t, errors.Is(err, rng.ErrInvalidBound), "Intn(%d) err=%v", b, err)
	}
}

func TestShuffle(t *testing.T) {
	for _, in := range [][]int{{}, {1}, {1, 2}, {1, 2, 3, 4, 5}, {1, 1, 2, 2, 3}} {
		orig := append([]int(nil), in...)
		a := append([]int(nil), in...)
		b := append([]int(nil), in...)
		wantCalls := max(len(in)-1, 0)
		ca, ea := shuffle.Shuffle(a, 7)
		cb, eb := shuffle.Shuffle(b, 7)
		must(t, ea == nil && eb == nil, "err=%v/%v", ea, eb)
		must(t, ca == wantCalls && cb == wantCalls, "calls=%d,%d want %d", ca, cb, wantCalls)
		must(t, fmt.Sprint(a) == fmt.Sprint(b), "not reproducible: %v/%v", a, b)
		must(t, shuffle.IsPermutation(a, orig), "%v not perm of %v", a, orig)
	}
}

func TestUniformity(t *testing.T) {
	type tc struct {
		name string
		n    int
		fn   func([]int, uint64) (int, error)
		maxR float64
		want error
	}
	for _, c := range []tc{
		{"fy n=2", 2, shuffle.Shuffle[int], 1.2, nil},
		{"fy n=4", 4, shuffle.Shuffle[int], 1.5, nil},
		{"full-range bug n=4", 4, BuggyFullRangePick, 5, ErrNonUniform},
	} {
		t.Run(c.name, func(t *testing.T) {
			dist, err := Distribution(c.n, 120000, c.fn)
			must(t, err == nil, "distribution: %v", err)
			kinds := 1
			for i := 2; i <= c.n; i++ {
				kinds *= i
			}
			if c.want == nil {
				must(t, len(dist) == kinds, "kinds=%d want %d", len(dist), kinds)
			}
			if len(dist) < kinds {
				dist["__never__"] = 0
			}
			err = Verify(dist, c.maxR)
			must(t, errors.Is(err, c.want), "ratio=%.3f err=%v want %v", Ratio(dist), err, c.want)
		})
	}
}

func TestSentinels(t *testing.T) {
	_, e0 := Distribution(0, 10, shuffle.Shuffle[int])
	_, e1 := Distribution(4, 0, shuffle.Shuffle[int])
	_, e2 := rng.New(1).Intn(0)
	must(t, errors.Is(e0, ErrEmptyInput), "e0=%v", e0)
	must(t, errors.Is(e1, ErrEmptyInput), "e1=%v", e1)
	must(t, errors.Is(e2, rng.ErrInvalidBound), "e2=%v", e2)
}

func TestConcurrentDistinctSlices(t *testing.T) {
	done := make(chan error, 32)
	for w := 0; w < 32; w++ {
		go func(seed uint64) {
			arr := []int{0, 1, 2, 3, 4, 5, 6, 7}
			_, err := shuffle.Shuffle(arr, seed)
			done <- err
		}(uint64(w + 1))
	}
	for w := 0; w < 32; w++ {
		must(t, <-done == nil, "concurrent shuffle error")
	}
}
