package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"sync"
	"testing"

	"ontology/api"
)

func ev(ts int64, id string) api.Event   { return api.Event{TS: ts, ID: id} }
func out(id string, ts, o int64) api.Out { return api.Out{ID: id, TS: ts, OutTS: o} }
func randEvs(r *rand.Rand, n, span int64) []api.Event {
	evs := make([]api.Event, n)
	for i := range evs {
		evs[i] = ev(r.Int63n(span), fmt.Sprintf("e%d", i))
	}
	return evs
}
func naive(evs []api.Event) []api.Out {
	s := append([]api.Event(nil), evs...)
	sort.SliceStable(s, func(i, j int) bool { return s[i].TS < s[j].TS })
	var outs []api.Out
	for _, e := range s {
		o := api.Out{ID: e.ID, TS: e.TS, OutTS: e.TS}
		if len(outs) > 0 && outs[len(outs)-1].OutTS > o.OutTS {
			o.OutTS = outs[len(outs)-1].OutTS
		}
		outs = append(outs, o)
	}
	return outs
}

// 第三节七事件分步表：每步发射、每步水位线、Flush 排空。
func TestSevenEventSequence(t *testing.T) {
	evs := []api.Event{ev(10, "a"), ev(7, "b"), ev(12, "c"), ev(9, "d"), ev(5, "e"), ev(11, "f"), ev(8, "g")}
	wantEm := [][]api.Out{{}, {out("b", 7, 7)}, {}, {out("d", 9, 9)}, {out("e", 5, 9)}, {}, {out("g", 8, 9)}}
	wantWM := []int64{7, 7, 9, 9, 9, 9, 9}
	eng, _ := api.New(3, 100)
	for i, e := range evs {
		got, err := eng.Feed([]api.Event{e})
		wm, _ := eng.WM()
		if err != nil || !slices.Equal(got, wantEm[i]) || wm != wantWM[i] {
			t.Errorf("step %d: got %v wm=%d err=%v, want %v wm=%d", i, got, wm, err, wantEm[i], wantWM[i])
		}
	}
	if got := eng.Flush(); !slices.Equal(got, []api.Out{out("a", 10, 10), out("f", 11, 11), out("c", 12, 12)}) {
		t.Errorf("flush: got %v", got)
	}
}

// 不变量 1：整批喂入随机序列（{seed, n, delay, span}），Flush 后 Output == 朴素重算。
func TestNaiveConsistency(t *testing.T) {
	for _, c := range [][4]int64{{1, 10, 3, 20}, {2, 100, 5, 50}, {3, 1000, 10, 200}, {4, 100, 0, 10}, {5, 5000, 100, 1000}} {
		evs := randEvs(rand.New(rand.NewSource(c[0])), c[1], c[3])
		eng, _ := api.New(c[2], int(c[1]))
		if _, err := eng.Feed(evs); err != nil {
			t.Fatalf("%v: %v", c, err)
		}
		eng.Flush()
		if got, want := eng.Output(), naive(evs); !slices.Equal(got, want) {
			t.Errorf("%v: output != naive", c)
		}
	}
}
func feedOneByOne(t *testing.T, c [4]int64) ([]api.Out, []int64) {
	evs := randEvs(rand.New(rand.NewSource(c[0])), c[1], c[3])
	eng, _ := api.New(c[2], int(c[1]))
	var wms []int64
	for _, e := range evs {
		if _, err := eng.Feed([]api.Event{e}); err != nil {
			t.Fatalf("%v: %v", c, err)
		}
		if wm, ok := eng.WM(); ok {
			wms = append(wms, wm)
		}
	}
	eng.Flush()
	return eng.Output(), wms
}

// 不变量 2+3：任意前缀 outTS 非递减且 >= TS、每事件恰好一次；水位线只进不退。
func TestMonotonicAndWatermark(t *testing.T) {
	for _, c := range [][4]int64{{7, 100, 3, 100}, {8, 500, 0, 50}, {9, 1000, 20, 2000}} {
		got, wms := feedOneByOne(t, c)
		if len(got) != int(c[1]) {
			t.Fatalf("%v: emitted %d, want %d", c, len(got), c[1])
		}
		for i := 1; i < len(wms); i++ {
			if wms[i] < wms[i-1] {
				t.Fatalf("%v: wm regressed %d -> %d", c, wms[i-1], wms[i])
			}
		}
		for i, o := range got {
			if o.OutTS < o.TS || (i > 0 && o.OutTS < got[i-1].OutTS) {
				t.Fatalf("%v: not monotonic at %d", c, i)
			}
		}
	}
}

// 不变量 4：三类可判定错误，被拒后状态不变、可继续用。
func TestFailuresLeaveNoTrace(t *testing.T) {
	if _, err := api.New(-1, 1); !errors.Is(err, api.ErrNegativeDelay) {
		t.Errorf("negative delay: %v", err)
	}
	eng, _ := api.New(1, 3)
	eng.Feed([]api.Event{ev(5, "a"), ev(6, "b")})
	out0 := eng.Output()
	wm0, _ := eng.WM()
	badEvs := [][]api.Event{{ev(7, "x"), ev(8, "")}, {ev(7, "x"), ev(8, "y"), ev(9, "z")}}
	badErrs := []error{api.ErrEmptyID, api.ErrTooManyPending}
	for i := range badEvs {
		if _, err := eng.Feed(badEvs[i]); !errors.Is(err, badErrs[i]) {
			t.Errorf("case %d: got %v, want %v", i, err, badErrs[i])
		}
	}
	out1 := eng.Output()
	wm1, _ := eng.WM()
	if !slices.Equal(out0, out1) || wm1 != wm0 {
		t.Error("state changed after rejection")
	}
	if _, err := eng.Feed([]api.Event{ev(7, "c"), ev(8, "d")}); err != nil { // 被拒事件未残留占位
		t.Errorf("engine unusable after rejection: %v", err)
	}
}
func TestConcurrentReaders(t *testing.T) {
	eng, _ := api.New(3, 100)
	eng.Feed(randEvs(rand.New(rand.NewSource(17)), 50, 100))
	eng.Flush()
	want := eng.Output()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !slices.Equal(eng.Output(), want) || eng.SelfCheck() != nil {
				t.Error("concurrent read mismatch")
			}
		}()
	}
	wg.Wait()
}
