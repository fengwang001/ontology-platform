// Command demo exercises the streamset package end to end. It takes no
// arguments, uses no network, and always exits 0; every check prints one
// OK/FAIL line and a final summary line reports the tally.
package main

import (
	"errors"
	"fmt"
	"math"

	"ontology/streamset"
)

var passed, failed int

func check(ok bool, format string, args ...any) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

func main() {
	s1 := []float64{1, 1, 2, 2}
	s2 := []float64{1, 1, 2, 2, 2}
	setU, _, _ := streamset.Union(streamset.Set, s1, s2)
	mulU, _, _ := streamset.Union(streamset.Multiset, s1, s2)
	check(len(setU) == 2 && len(mulU) == 5,
		"union differs by semantics: set=%v multiset=%v", setU, mulU)
	setI, _, _ := streamset.Intersect(streamset.Set, s1, s2)
	mulI, _, _ := streamset.Intersect(streamset.Multiset, s1, s2)
	check(len(setI) == 2 && len(mulI) == 4,
		"intersect differs by semantics: set=%v multiset=%v", setI, mulI)
	diff, _, _ := streamset.Difference(streamset.Multiset,
		[]float64{1, 1, 1, 2, 3}, []float64{1}, []float64{3, 4})
	check(fmt.Sprint(diff) == "[1 1 2]", "multiset difference [1 1 1 2 3]-[1]-[3 4] = %v", diff)
	floor, _, _ := streamset.Difference(streamset.Multiset, []float64{1}, []float64{1, 1, 1})
	check(len(floor) == 0, "difference floored at zero: [1]-[1 1 1] = %v", floor)
	zu, _, _ := streamset.Union(streamset.Set, []float64{0.0}, []float64{math.Copysign(0, -1)})
	check(len(zu) == 1 && zu[0] == 0, "+0.0 and -0.0 are one value: union size = %d", len(zu))
	_, _, nanErr := streamset.Union(streamset.Set, []float64{1}, []float64{0, math.NaN()})
	var ne *streamset.NaNError
	check(errors.As(nanErr, &ne) && ne.Stream == 1 && ne.Index == 1,
		"NaN rejected with location: %v", nanErr)
	_, _, ordErr := streamset.Union(streamset.Set, []float64{1, 3, 2})
	var oe *streamset.OrderError
	check(errors.As(ordErr, &oe) && oe.Stream == 0 && oe.Index == 2,
		"unsorted stream rejected with location: %v", ordErr)
	_, _, intErr := streamset.Intersect(streamset.Set)
	check(errors.Is(intErr, streamset.ErrEmptyIntersection),
		"zero-stream intersection is a decidable error: %v", intErr)
	const k, per = 3, 400_000
	streams := make([][]float64, k)
	for s := range streams {
		streams[s] = make([]float64, per)
		for i := range streams[s] {
			streams[s][i] = float64(i*k + s)
		}
	}
	m, err := streamset.NewMerger(streams)
	maxHeap := 0
	for err == nil {
		if _, _, ok := m.Next(); !ok {
			break
		}
		if hs := m.Stats().HeapSize; hs > maxHeap {
			maxHeap = hs
		}
	}
	stats := m.Stats()
	n := int64(k * per)
	bound := int64(6) * n // 3*n*ceil(log2(3))
	check(err == nil && maxHeap <= k && stats.Comparisons > 0 && stats.Comparisons <= bound,
		"n=%d k=%d: max heap=%d (<=%d), comparisons=%d (<= %d, linear bound 3*n*ceil(log2 k))",
		n, k, maxHeap, k, stats.Comparisons, bound)
	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
}
