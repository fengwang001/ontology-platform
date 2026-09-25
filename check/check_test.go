package check

import (
	"errors"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"

	"ontology/iv"
	"ontology/merge"
)

func merged(in []iv.Interval) []iv.Interval {
	var m merge.Merger
	_ = m.AddAll(in)
	return m.Ranges()
}
func TestValidate(t *testing.T) { // rule 1: start >= end rejected; sentinels
	_, e1 := iv.New(1, 1)
	_, e2 := iv.New(3, 1)
	e3 := new(merge.Merger).Add(iv.Interval{})
	for _, c := range []struct{ err, want error }{
		{e1, iv.ErrEmptyInterval}, {e2, iv.ErrInvertedInterval}, {e2, iv.ErrEmptyInterval}, {e3, merge.ErrInvalidInterval}, {e3, iv.ErrEmptyInterval}} {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%v should match %v", c.err, c.want)
		}
	}
	if _, err := iv.New(1, 2); err != nil || errors.Is(e1, iv.ErrInvertedInterval) {
		t.Error("valid rejected or empty matches inverted")
	}
}
func TestMergedProperties(t *testing.T) { // rules 2,3,5 + section 4
	cases := [][]iv.Interval{
		{iv.Must(5, 6), iv.Must(0, 2), iv.Must(3, 4), iv.Must(1, 3)},
		{iv.Must(0, 10), iv.Must(2, 3), iv.Must(4, 5)},
		{iv.Must(1, 2), iv.Must(4, 5)},
	}
	rnd := rand.New(rand.NewPCG(1, 2))
	var big []iv.Interval
	for range 10000 {
		a := rnd.IntN(1000)
		big = append(big, iv.Must(a, a+1+rnd.IntN(20)))
	}
	for _, c := range append(cases, big) {
		var ref Ref
		_ = ref.AddAll(c)
		got, want := merged(c), ref.Ranges()
		asc := slices.IsSortedFunc(got, func(a, x iv.Interval) int { return a.Start - x.Start })
		disjoint := slices.IsSortedFunc(got, func(a, x iv.Interval) int { return a.End - x.Start })
		if !asc || !disjoint || !slices.Equal(got, want) || len(got) > len(c) {
			t.Errorf("case %d: got %d ranges, want %d", len(c), len(got), len(want))
		}
		for range 8 {
			rnd.Shuffle(len(c), func(i, j int) { c[i], c[j] = c[j], c[i] })
			if g2 := merged(c); !slices.Equal(g2, got) {
				t.Errorf("order-dependent: %d ranges", len(g2))
			}
		}
	}
}
func TestAbuttingMerge(t *testing.T) { // rule 4 + section 3: abutting merges
	in := []iv.Interval{iv.Must(1, 2), iv.Must(2, 3)}
	sweep := func(gt bool) (out []iv.Interval) { // inline sweep; gt = wrong ">" predicate
		for _, v := range in {
			if n := len(out); n > 0 && (out[n-1].End > v.Start || !gt && out[n-1].End == v.Start) {
				out[n-1].End = max(out[n-1].End, v.End)
			} else {
				out = append(out, v)
			}
		}
		return out
	}
	for i, want := range [][]iv.Interval{{iv.Must(1, 3)}, in} { // i==1: wrong impl splits
		if got := sweep(i == 1); !slices.Equal(got, want) {
			t.Errorf("gt=%v: got %v, want %v", i == 1, got, want)
		}
	}
	if got := merged(in); !slices.Equal(got, []iv.Interval{iv.Must(1, 3)}) {
		t.Errorf("AddAll abutting = %v, want [[1,3)]", got)
	}
}
func TestConcurrentAdd(t *testing.T) { // section 5: 16 goroutines converge
	for _, g := range []int{16} {
		in := make([]iv.Interval, g*100)
		for i := range in {
			in[i] = iv.Must(i%50, i%50+3)
		}
		conc, wg := merge.Merger{}, sync.WaitGroup{}
		for k := range g {
			wg.Go(func() { _ = conc.AddAll(in[k*100 : (k+1)*100]) })
		}
		wg.Wait()
		if want := merged(in); !slices.Equal(conc.Ranges(), want) {
			t.Errorf("%d goroutines: %v != %v", g, conc.Ranges(), want)
		}
	}
}
