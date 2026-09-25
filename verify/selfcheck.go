package verify

import "ontology/cksum"

// scenario builds the 6-record log of NOTES.md; corrupt seqs get Val+30.
func scenario(corrupt ...int64) *Engine {
	eng, _ := New(3)
	for i := int64(1); i <= 6; i++ {
		_ = eng.Append(i, 10*i)
	}
	for _, c := range corrupt {
		eng.recs[c-1].val += 30
	}
	return eng
}

// halfOpen: only Seq 5 corrupt; closed Verify(1,5) finds it, a
// [from,to) scan of 1..4 misses it and reports clean.
func halfOpen() (miss int64, saysClean bool) {
	eng := scenario(5)
	c, ok, _ := eng.Verify(1, 5)
	if ok || c != 5 {
		return 0, false
	}
	for i := int64(1); i < 5; i++ {
		if _, clean, _ := eng.Verify(i, i); !clean {
			return 5, false
		}
	}
	return 5, true
}

// descFirst returns the "first" corrupt seq a descending scan reports.
func descFirst() int64 {
	eng := scenario(4, 5)
	for i := int64(6); i >= 1; i-- {
		if _, ok, _ := eng.Verify(i, i); !ok {
			return i
		}
	}
	return 0
}

// naiveOK (invariant 1): Verify agrees with a naive ascending rescan.
func naiveOK() bool {
	cases := []struct{ from, to, want int64 }{{1, 6, 4}, {1, 5, 5}, {3, 4, 4}, {1, 6, 0}}
	corrupt := [][]int64{{4, 5}, {5}, {4, 5}, nil}
	for i, c := range cases {
		got, ok, _ := scenario(corrupt[i]...).Verify(c.from, c.to)
		if got != c.want || ok != (c.want == 0) {
			return false
		}
	}
	return true
}

// totalOK (invariant 2): total grows by exactly e per Append and equals the sum of segment sums.
func totalOK() bool {
	eng, _ := New(3)
	var want int64
	for i := int64(1); i <= 7; i++ {
		before := eng.Total()
		if err := eng.Append(i, 10*i); err != nil {
			return false
		}
		want += cksum.Elem(i, 10*i)
		segs := eng.SegSum(1) + eng.SegSum(2) + eng.SegSum(3)
		if eng.Total() != want || eng.Total() != before+cksum.Elem(i, 10*i) || segs != want {
			return false
		}
	}
	eng.recs[6].val += 30
	_, tot, _ := eng.Recompute(1, 7)
	return tot == eng.Total() && tot == want+30
}

// locateOK (invariant 3): the located seq is in range, is the smallest
// corrupt one, and clean prefixes report ok.
func locateOK() bool {
	for _, at := range []int64{1, 4, 6} {
		eng := scenario(at)
		if c, ok, _ := eng.Verify(1, 6); ok || c != at {
			return false
		}
		if _, clean, _ := eng.Verify(1, at-1); at > 1 && !clean {
			return false
		}
	}
	return true
}

// rejectOK (invariant 4): rejected operations leave state untouched.
func rejectOK() bool {
	if _, err := New(0); err != ErrBadSegSize {
		return false
	}
	if ErrBadSegSize == ErrSeqGap || ErrSeqGap == ErrBadRange || ErrBadSegSize == ErrBadRange {
		return false
	}
	eng := scenario()
	snap := [3]int64{eng.SegSum(1), eng.SegSum(2), eng.Total()}
	errs := []error{eng.Append(8, 80), eng.Append(3, 30)}
	for _, r := range [][2]int64{{0, 3}, {4, 2}, {1, 7}} {
		_, _, err := eng.Verify(r[0], r[1])
		errs = append(errs, err)
	}
	_, _, err := eng.Recompute(0, 6)
	for _, e := range append(errs, err) {
		if e == nil {
			return false
		}
	}
	same := [3]int64{eng.SegSum(1), eng.SegSum(2), eng.Total()} == snap
	return same && eng.Append(7, 70) == nil
}

// scaleOK: Verify touches only records inside the queried range,
// independent of the log size N.
func scaleOK() bool {
	for _, n := range []int64{100, 1000, 10000} {
		eng, _ := New(7)
		for i := int64(1); i <= n; i++ {
			_ = eng.Append(i, i)
		}
		if _, ok, _ := eng.Verify(1, 10); !ok || eng.lastCmp > 10 {
			return false
		}
	}
	return true
}

// concOK: concurrent Verify calls on a corrupted log all locate the
// same first corrupt record.
func concOK() bool {
	eng := scenario(4, 5)
	ch := make(chan int64, 32)
	for i := 0; i < 32; i++ {
		go func() {
			c, _, _ := eng.Verify(1, 6)
			ch <- c
		}()
	}
	for i := 0; i < 32; i++ {
		if <-ch != 4 {
			return false
		}
	}
	return true
}
