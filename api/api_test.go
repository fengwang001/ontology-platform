package api

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

// feed 向 w 注入确定性伪随机 Add/Advance 交错序列。
func feed(w *WindowCounter, seed uint64, n int) {
	clock, x := int64(math.MinInt64), seed
	next := func(m int64) int64 { x = x*6364136223846793005 + 1442695040888963407; return int64(x>>33) % m }
	for i := 0; i < n; i++ {
		if next(5) < 3 {
			w.Add("k"+string(rune('a'+next(7))), next(120)-60)
			continue
		}
		tm := next(50) - 25
		if clock != math.MinInt64 && tm < clock {
			tm = clock
		}
		if _, err := w.Advance(tm); err == nil {
			clock = tm
		}
	}
}

func TestFailureAtomicity(t *testing.T) {
	for _, p := range [][2]int64{{0, 4}, {12, 5}, {-3, 1}} {
		if _, err := New(p[0], p[1], 1); !errors.Is(err, ErrInvalidParams) {
			t.Errorf("params %v: %v", p, err)
		}
	}
	w, _ := New(12, 4, 5)
	w.Add("a", 0) // 3 个窗口
	w.Advance(0)  // 合法：时钟=0，无窗口关闭
	snap := fmt.Sprint(w.Results(), w.Dropped())
	ops := []struct {
		op   func() error
		want error
	}{
		{func() error { return w.Add("", 0) }, ErrEmptyKey},
		{func() error { return w.Add("b", 8) }, ErrMaxOpen}, // 3+3=6 > 5
		{func() error { _, e := w.Advance(-1); return e }, ErrClockBack},
	}
	for _, tc := range ops {
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Errorf("got %v want %v", err, tc.want)
		}
		if fmt.Sprint(w.Results(), w.Dropped()) != snap {
			t.Errorf("%v: state changed by rejected op", tc.want)
		}
	}
	if err := w.Add("a", 8); err != nil { // 被拒后仍可正常使用
		t.Errorf("usable after rejections: %v", err)
	}
	// 四类哨兵错误互不相同。
	all := []error{ErrInvalidParams, ErrEmptyKey, ErrClockBack, ErrMaxOpen}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinels %d/%d not distinguishable", i, j)
			}
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	w, _ := New(12, 4, 1<<20)
	feed(w, 99, 300)
	w.Flush()
	want, dropped := w.Results(), w.Dropped()
	var wg sync.WaitGroup
	var bad atomic.Bool
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !slices.Equal(w.Results(), want) || w.Dropped() != dropped || w.SelfCheck() != nil {
				bad.Store(true)
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Error("concurrent reads differ")
	}
}

func TestConcurrentAdds(t *testing.T) {
	const size, slide = 12, 4
	w, _ := New(size, slide, 1<<20)
	var events []ev
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				w.Add(fmt.Sprintf("g%d", g), int64(i)*3-70)
			}
		}(g)
		for i := 0; i < 50; i++ {
			events = append(events, ev{fmt.Sprintf("g%d", g), int64(i)*3 - 70, math.MinInt64})
		}
	}
	wg.Wait()
	w.Flush()
	if got, want := w.Results(), naiveRef(events, size, slide); !slices.Equal(got, want) {
		t.Error("concurrent adds mismatch vs naive reference")
	}
}
