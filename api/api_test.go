package api_test

import (
	"errors"
	"math"
	"ontology/api"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

type tOp [3]int64

var eight = []tOp{{1, 10, 0}, {1, 20, 1}, {0, 0, 10}, {1, 20, 11}, {0, 0, 12}, {0, 0, 30}, {1, 25, 31}, {0, 0, 40}}

func must(t *testing.T, ok bool, m string) {
	if !ok {
		t.Fatal(m)
	}
}
func feed(d, to int64, ops []tOp) (f *api.Flow, ws []int64, ds []int) {
	f, _ = api.New(d, to)
	for i, o := range ops {
		if o[0] == 1 {
			_ = f.Feed(string(rune('a'+i)), o[1], o[2])
		} else {
			_ = f.Tick(o[2])
		}
		ws, ds = append(ws, f.Watermark()), append(ds, f.Dropped())
	}
	return
}
func batch(d, to int64, ops []tOp) (wm, last int64, drop int) {
	wm, last = math.MinInt64, math.MinInt64
	for _, o := range ops {
		if o[0] == 1 {
			last = o[2]
			if m := o[1] - d; m < wm {
				drop++
			} else {
				wm = max(wm, m)
			}
		} else if last == math.MinInt64 || o[2]-last >= to {
			wm = max(wm, o[2]-d)
		}
	}
	return
}

var seqs = []struct {
	d, to, wm int64
	drop      int
	ops       []tOp
}{
	{3, 5, 37, 1, eight},
	{3, 5, math.MinInt64, 0, nil},
	{2, 4, 8, 0, []tOp{{0, 0, 10}}},
	{5, 10, 95, 0, []tOp{{1, 100, 0}, {0, 0, 5}}},
	{1, 100, 9, 1, []tOp{{1, 10, 0}, {1, 5, 1000}}},
	{3, 5, 27, 0, eight[:6]},
	{3, 5, 197, 0, []tOp{{1, 100, 0}, {0, 0, 200}}},
	{3, 5, 97, 0, []tOp{{1, 100, 0}, {0, 0, 4}}},
}

func TestEightSteps(t *testing.T) {
	_, ws, ds := feed(3, 5, eight)
	must(t, reflect.DeepEqual(ws, []int64{7, 17, 17, 17, 17, 27, 27, 37}), "eight watermarks")
	must(t, reflect.DeepEqual(ds, []int{0, 0, 0, 0, 0, 0, 1, 1}), "eight dropped")
}
func TestBatchRecompute(t *testing.T) {
	for _, c := range seqs {
		f, _, _ := feed(c.d, c.to, c.ops)
		bw, _, bd := batch(c.d, c.to, c.ops)
		must(t, bw == c.wm && bd == c.drop && f.Watermark() == bw && f.Dropped() == bd, "batch recompute")
	}
}
func TestMonotonic(t *testing.T) {
	for _, c := range seqs {
		_, ws, _ := feed(c.d, c.to, c.ops)
		for i := 1; i < len(ws); i++ {
			must(t, ws[i] >= ws[i-1], "watermark retreated")
		}
	}
}
func TestProcessingTimeIdle(t *testing.T) {
	for _, c := range seqs[5:] {
		f, _, _ := feed(c.d, c.to, c.ops)
		must(t, f.Watermark() == c.wm, "idle must use processing time")
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	_, e1 := api.New(0, 5)
	_, e2 := api.New(-1, 5)
	_, e3 := api.New(3, 0)
	must(t, errors.Is(e1, api.ErrInvalidParams) && errors.Is(e2, api.ErrInvalidParams) && errors.Is(e3, api.ErrInvalidParams), "params")
	must(t, api.ErrInvalidParams != api.ErrEmptyKey && api.ErrEmptyKey != api.ErrNegativeValue, "sentinels distinct")
	f, _ := api.New(3, 5)
	_ = f.Feed("seed", 10, 0)
	w0, d0 := f.Watermark(), f.Dropped()
	must(t, errors.Is(f.Feed("", 20, 5), api.ErrEmptyKey) && f.Watermark() == w0 && f.Dropped() == d0, "empty key")
	must(t, errors.Is(f.Feed("x", -1, 5), api.ErrNegativeValue) && f.Watermark() == w0 && f.Dropped() == d0, "neg ts")
	must(t, errors.Is(f.Feed("x", 20, -1), api.ErrNegativeValue) && f.Watermark() == w0 && f.Dropped() == d0, "neg pt feed")
	must(t, errors.Is(f.Tick(-1), api.ErrNegativeValue) && f.Watermark() == w0 && f.Dropped() == d0, "neg pt tick")
	h, _ := api.New(100, 5)
	_ = h.Feed("seed", 10, 0)
	must(t, errors.Is(h.Feed("", 9, 9), api.ErrEmptyKey), "h empty key")
	must(t, h.Tick(105) == nil && h.Watermark() == 5, "lastEventPT leaked")
	must(t, f.Feed("later", 20, 5) == nil && f.Watermark() == 17, "resume after reject")
}
func TestSelfCheck(t *testing.T) {
	for _, c := range seqs[:3] {
		f, _ := api.New(c.d, c.to)
		must(t, f.SelfCheck() == nil, "SelfCheck failed")
	}
}
func TestConcurrentReadersIdentical(t *testing.T) {
	f, _, _ := feed(3, 5, eight)
	got := make([]int64, 32)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func(i int) { defer wg.Done(); got[i] = f.Watermark() }(i)
	}
	wg.Wait()
	for _, v := range got {
		must(t, v == 37, "concurrent readers differ")
	}
}
func TestConcurrentAdvanceMonotonic(t *testing.T) {
	f, _ := api.New(3, 5)
	var wg sync.WaitGroup
	var bad int32
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			prev := f.Watermark()
			for j := 0; j < 50; j++ {
				_ = f.Tick(int64((g + 1) * 1000 * (j + 1)))
				if f.Watermark() < prev {
					atomic.StoreInt32(&bad, 1)
				}
				prev = f.Watermark()
			}
		}(g)
	}
	wg.Wait()
	must(t, bad == 0, "watermark retreated concurrently")
}
