// Command demo exercises the segtree package and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"

	"ontology/check"
	"ontology/seg"
	"ontology/segtree"
)

func main() {
	fails := 0
	ok := func(cond bool, msg string) {
		if cond {
			fmt.Println("OK", msg)
		} else {
			fmt.Println("FAIL", msg)
			fails++
		}
	}
	const n = 1000
	st, nv := segtree.New(n), check.NewNaive(n)
	_ = st.AddRange(0, n-1, 1)
	nv.Add(0, n-1, 1)
	got, _ := st.SumRange(0, 0)
	ok(got == 1, "query pushdown: full-range add then SumRange(0,0)==1")
	_ = st.AddRange(10, 99, 5)
	nv.Add(10, 99, 5)
	sum, _ := st.SumRange(0, n-1)
	ok(sum == nv.Sum(0, n-1), "range sum matches naive reference")
	pt, _ := st.SumRange(50, 50)
	ok(pt == nv.Sum(50, 50), "single-point sum matches naive")
	errA := st.AddRange(5, 2, 1)
	ok(errors.Is(errA, seg.ErrBadRange) && errors.Is(errA, seg.ErrReversed), "l>r rejected")
	_, errS := st.SumRange(0, n)
	ok(errors.Is(errS, seg.ErrBadRange) && errors.Is(errS, seg.ErrOutOfBounds), "out-of-bounds rejected")
	_, _ = st.SumRange(0, n-1)
	limit := int64(4*math.Ceil(math.Log2(n)) + 8)
	ok(st.LastVisited() <= limit, "visit count within 4*log2(n)+8")
	rg := seg.Range{L: 7, R: 7}
	_ = st.AddRange(rg.L.Int(), rg.R.Int(), 3)
	nv.Add(7, 7, 3)
	v, _ := st.SumRange(rg.L.Int(), rg.R.Int())
	ok(v == nv.Sum(7, 7), "seg.Index/Range interop")
	if fails == 0 {
		fmt.Println("OK all 7 checks passed")
	} else {
		fmt.Printf("FAIL %d checks failed\n", fails)
		os.Exit(1)
	}
}
