// Command demo verifies the matrix-inverse deliverable and prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"

	"ontology/api"
	"ontology/gauss"
	"ontology/pivot"
)

var all = true

func ck(name string, pass bool) {
	all = all && pass
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[pass], name)
}
func aug(m []float64) string {
	return fmt.Sprintf("[%g %g|%g %g]/[%g %g|%g %g]",
		m[0], m[1], m[2], m[3], m[4], m[5], m[6], m[7])
}

// buggy replays n=2 elimination with optional defects: no pivot (乙),
// right half not synchronized (甲), above-elimination skipped (丙).
func buggy(a []float64, usePivot, syncR, above bool) []float64 {
	m := []float64{a[0], a[1], 1, 0, a[2], a[3], 0, 1}
	row := func(i int, fn func(j int)) {
		for j := 0; j < 4; j++ {
			if j < 2 || syncR {
				fn(j)
			}
		}
	}
	for k := 0; k < 2; k++ {
		p := k // strict > keeps the smallest index on a tie
		if usePivot && math.Abs(m[4+k]) > math.Abs(m[k]) {
			p = 1
		}
		if p != k {
			row(k, func(j int) { m[k*4+j], m[p*4+j] = m[p*4+j], m[k*4+j] })
		}
		inv := 1.0 / m[k*4+k]
		row(k, func(j int) { m[k*4+j] *= inv })
		for i := 0; i < 2; i++ {
			if i == k || (!above && i < k) {
				continue
			}
			fac := m[i*4+k]
			row(i, func(j int) { m[i*4+j] -= fac * m[k*4+j] })
		}
	}
	return []float64{m[2], m[3], m[6], m[7]}
}

func main() {
	a := []float64{0, 2, 1, 1}
	svc := api.New()
	p0, f0 := pivot.Pick(a, 2, 0)
	_, fz := pivot.Pick([]float64{0, 0, 0, 0}, 2, 0)
	ck("pivot: k=0 picks row1, ties keep min index, all-zero -> ok=false",
		p0 == 1 && f0 && !fz)

	// Five-row derivation on the augmented 2x4 matrix, calling pivot.Pick
	// on the left half at each column and mirroring ops onto the right.
	m := []float64{0, 2, 1, 0, 1, 1, 0, 1}
	l := func() []float64 { return []float64{m[0], m[1], m[4], m[5]} }
	snaps := []string{aug(m)}
	pr, _ := pivot.Pick(l(), 2, 0) // candidates |0|,|1| -> row 1, swap
	for j := 0; j < 4; j++ {
		m[j], m[4+j] = m[4+j], m[j]
	}
	snaps = append(snaps, aug(m)) // pivot 1 normalized; below factor is 0
	q, _ := pivot.Pick(l(), 2, 1) // pivot 2 -> normalize row q
	inv := 1.0 / m[q*4+1]
	for j := 0; j < 4; j++ {
		m[q*4+j] *= inv
	}
	snaps = append(snaps, aug(m))
	fac := m[1] // above-elimination: row0 -= 1*row1
	for j := 0; j < 4; j++ {
		m[j] -= fac * m[4+j]
	}
	snaps = append(snaps, aug(m))
	want := []string{"[0 2|1 0]/[1 1|0 1]", "[1 1|0 1]/[0 2|1 0]",
		"[1 1|0 1]/[0 1|0.5 0]", "[1 0|-0.5 1]/[0 1|0.5 0]"}
	ai, _ := svc.Inv(a, 2)
	ck("five steps: pivots/swap/augmented changes yield A^-1",
		pr == 1 && q == 1 && slices.Equal(snaps, want) &&
			slices.Equal(ai, []float64{-0.5, 1, 0.5, 0}))

	jia := buggy(a, true, false, true)  // (甲) right half never touched
	yi := buggy(a, false, true, true)   // (乙) pivot 0 -> divide by zero
	bing := buggy(a, true, true, false) // (丙) above-elimination missing
	bad := false
	for _, v := range yi {
		bad = bad || math.IsNaN(v) || math.IsInf(v, 0)
	}
	ck("defects: 甲 A^-1[0][1]=0; 乙 Inf/NaN; 丙 A^-1[0][0]=0",
		jia[1] == 0 && bad && bing[0] == 0)

	d := 0.0 // A * A^-1 == I within 1e-9
	for i := 0; i < 4; i++ {
		v := a[(i/2)*2]*ai[(i%2)] + a[(i/2)*2+1]*ai[2+(i%2)]
		if i%3 == 0 { // diagonal entries are i=0,3
			v--
		}
		d = math.Max(d, math.Abs(v))
	}
	ck("A*A^-1 == I within 1e-9", d <= 1e-9)

	snap := slices.Clone(a)
	_, _ = svc.Inv(a, 2)
	ck("input a byte-identical after Inv", slices.Equal(a, snap))

	_, e0 := svc.Inv(nil, 0)
	_, e1 := svc.Inv([]float64{1, 0, 0, 1}, 3)
	_, e2 := svc.Inv([]float64{0, 0, 0, 0}, 2)
	ck("three distinct decidable errors (empty/dimension/singular)",
		errors.Is(e0, api.ErrEmptyMatrix) && errors.Is(e1, api.ErrBadDimension) &&
			errors.Is(e2, api.ErrSingular) && e0 != e1 && e1 != e2 && e0 != e2)
	ck("rejected calls leave no state; api.SelfCheck passes",
		gauss.RejectionsLeaveCounterUntouched() && svc.SelfCheck())
	ck("diagonal n=100/1000/10000: multiply-subtract row ops == 0",
		gauss.DiagonalNeedsNoRowOps(100, 1000, 10000))

	const N = 16
	outs := make([][]float64, N)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range outs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; outs[i], _ = svc.Inv(a, 2) }(i)
	}
	close(start)
	wg.Wait()
	same := true
	for i := 1; i < N; i++ {
		same = same && slices.Equal(outs[i], outs[0])
	}
	ck("16 goroutines share one input: byte-identical results, race clean", same)

	if !all {
		panic("demo failed")
	}
}
