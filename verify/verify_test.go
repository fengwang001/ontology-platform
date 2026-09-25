package verify

import (
	"errors"
	"testing"

	"ontology/cksum"
)

func build(t *testing.T, seg int, n int64, corrupt ...int64) *Engine {
	t.Helper()
	eng, err := New(seg)
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= n; i++ {
		if err := eng.Append(i, 10*i); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range corrupt {
		eng.recs[c-1].val += 30
	}
	return eng
}

func TestVerifyMatchesNaive(t *testing.T) {
	cases := []struct{ n, from, to int64 }{{6, 1, 6}, {6, 1, 5}, {10, 3, 8}, {9, 1, 9}}
	corrupt := [][]int64{{4, 5}, {5}, {2, 7}, nil}
	for ci, c := range cases {
		eng := build(t, 3, c.n, corrupt[ci]...)
		want := int64(0)
		for i := c.from; i <= c.to && want == 0; i++ {
			cur := 10 * i
			for _, bad := range corrupt[ci] {
				if bad == i {
					cur += 30
				}
			}
			if cksum.Elem(i, cur) != cksum.Elem(i, 10*i) {
				want = i
			}
		}
		got, ok, err := eng.Verify(c.from, c.to)
		if err != nil || got != want || ok != (want == 0) {
			t.Errorf("%+v: got (%d,%v,%v) want first=%d", c, got, ok, err, want)
		}
	}
}

func TestTotalInvariant(t *testing.T) {
	eng := build(t, 3, 0)
	var want int64
	for i := int64(1); i <= 8; i++ {
		before := eng.Total()
		if err := eng.Append(i, 10*i); err != nil {
			t.Fatal(err)
		}
		want += cksum.Elem(i, 10*i)
		segs := eng.SegSum(1) + eng.SegSum(2) + eng.SegSum(3)
		if eng.Total() != want || eng.Total() != before+cksum.Elem(i, 10*i) || segs != want {
			t.Fatalf("after append %d: total=%d want=%d segsum=%d", i, eng.Total(), want, segs)
		}
	}
}

func TestVerifyLocatesFirst(t *testing.T) {
	for _, at := range []int64{1, 4, 6} {
		eng := build(t, 3, 6, at)
		if c, ok, _ := eng.Verify(1, 6); ok || c != at {
			t.Errorf("corrupt at %d: got (%d,%v)", at, c, ok)
		}
		if _, ok, _ := eng.Verify(1, at-1); at > 1 && !ok {
			t.Errorf("corrupt at %d: prefix not clean", at)
		}
	}
}

func TestRejectionLeavesState(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrBadSegSize) {
		t.Fatalf("New(0): %v", err)
	}
	eng := build(t, 3, 6)
	snap := [3]int64{eng.SegSum(1), eng.SegSum(2), eng.Total()}
	bad := []struct {
		op  func() error
		err error
	}{
		{func() error { return eng.Append(8, 80) }, ErrSeqGap},
		{func() error { return eng.Append(3, 30) }, ErrSeqGap},
		{func() error { _, _, e := eng.Verify(0, 3); return e }, ErrBadRange},
		{func() error { _, _, e := eng.Verify(4, 2); return e }, ErrBadRange},
		{func() error { _, _, e := eng.Verify(1, 7); return e }, ErrBadRange},
		{func() error { _, _, e := eng.Recompute(0, 6); return e }, ErrBadRange},
	}
	for _, b := range bad {
		err := b.op()
		if !errors.Is(err, b.err) || [3]int64{eng.SegSum(1), eng.SegSum(2), eng.Total()} != snap {
			t.Errorf("op err=%v (want %v) or state changed", err, b.err)
		}
	}
	if ErrBadSegSize == ErrSeqGap || ErrSeqGap == ErrBadRange || ErrBadSegSize == ErrBadRange {
		t.Error("sentinel errors not distinct")
	}
	if err := eng.Append(7, 70); err != nil {
		t.Error("log unusable after rejections")
	}
}

func TestVerifyCounterBounded(t *testing.T) {
	for _, n := range []int64{100, 1000, 10000} {
		eng := build(t, 7, n)
		if _, ok, _ := eng.Verify(1, 10); !ok || eng.lastCmp > 10+1 {
			t.Errorf("n=%d: ok=%v compared=%d, want <= 11", n, ok, eng.lastCmp)
		}
	}
}

func TestConcurrentVerifyAgrees(t *testing.T) {
	eng := build(t, 3, 6, 4)
	ch := make(chan int64, 32)
	for i := 0; i < 32; i++ {
		go func() {
			c, _, _ := eng.Verify(1, 6)
			ch <- c
		}()
	}
	for i := 0; i < 32; i++ {
		if c := <-ch; c != 4 {
			t.Errorf("corrupt=%d, want 4", c)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	r := SelfCheck()
	want1 := [8]int64{110, 330, 660, 660, 660, 660, 660, 660}
	want2 := [8]int64{0, 0, 0, 440, 990, 1650, 1710, 1710}
	wantT := [8]int64{110, 330, 660, 1100, 1650, 2310, 2370, 2370}
	if r.Seg1 != want1 || r.Seg2 != want2 || r.Tot != wantT {
		t.Errorf("steps: %v %v %v", r.Seg1, r.Seg2, r.Tot)
	}
	if r.First != 4 || r.HalfMiss != 5 || !r.HalfSaysClean || r.WrongTot != 231 || r.WrongSeg2 != 165 || r.Desc != 5 {
		t.Errorf("traps: %+v", r)
	}
	if r.Inv != [4]bool{true, true, true, true} || !r.ScaleOK || !r.ConcOK {
		t.Errorf("invariants: %+v", r)
	}
}
