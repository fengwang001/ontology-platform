package api_test

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

func ev(k string, ts, v int64) api.Event { return api.Event{Key: k, TS: ts, Val: v} }

func TestSevenStep(t *testing.T) {
	steps := []struct{ ts, val, sum, drop int64 }{
		{5, 10, 10, 0}, {15, 20, 20, 0}, {12, 30, 50, 0}, {12, 40, 90, 0}, {25, 50, 50, 0}, {15, 60, 50, 1}, {35, 70, 70, 1},
	}
	w, _ := api.New(10, 0)
	for i, s := range steps {
		_, err := w.Feed([]api.Event{ev("k", s.ts, s.val)})
		if err != nil || w.View()["k"] != s.sum || w.Dropped() != s.drop {
			t.Fatalf("第%d步: err=%v sum=%d(want %d) drop=%d(want %d)",
				i+1, err, w.View()["k"], s.sum, w.Dropped(), s.drop)
		}
	}
}

func reference(seq []api.Event, size int64) (map[string]int64, int64) {
	wm, view, drop := map[string]int64{}, map[string]int64{}, int64(0)
	for _, e := range seq {
		wm[e.Key] = max(wm[e.Key], e.TS)
		view[e.Key] += 0 // 全部事件被丢弃的 Key 也要有 0 值条目
		if e.TS <= wm[e.Key]-size {
			drop++
		}
	}
	for _, e := range seq {
		if e.TS > wm[e.Key]-size {
			view[e.Key] += e.Val
		}
	}
	return view, drop
}

// 不变量1：任意序列喂完后 View 与批量重算逐 Key 相同，丢弃数与模拟一致。
func TestBatchEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, size := range []int64{1, 3, 10, 100} {
		for _, n := range []int{1, 10, 200} {
			t.Run(fmt.Sprintf("size=%d/n=%d", size, n), func(t *testing.T) {
				w, _ := api.New(size, 0)
				seq := make([]api.Event, n)
				for i := range seq {
					seq[i] = ev(string(rune('a'+rng.Intn(4))), 1+rng.Int63n(300), rng.Int63n(100))
				}
				for start := 0; start < len(seq); { // 随机切分批喂入
					end := min(start+1+rng.Intn(7), len(seq))
					if _, err := w.Feed(seq[start:end]); err != nil {
						t.Fatal(err)
					}
					start = end
				}
				wantView, wantDrop := reference(seq, size)
				if !maps.Equal(w.View(), wantView) || w.Dropped() != wantDrop {
					t.Fatalf("View=%v(want %v) Dropped=%d(want %d)", w.View(), wantView, w.Dropped(), wantDrop)
				}
			})
		}
	}
}

// 不变量4：任何被拒操作不改变任何状态，且之后仍可正常使用。
func TestRejectedLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name      string
		maxOpen   int
		seed, bad []api.Event
		want      error
	}{
		{"空Key", 0, []api.Event{ev("k", 5, 10)}, []api.Event{ev("k", 6, 1), ev("", 7, 2)}, api.ErrEmptyKey},
		{"窗口超限", 2, []api.Event{ev("k", 1, 1), ev("k", 2, 2)}, []api.Event{ev("k", 3, 3)}, api.ErrTooManyOpen},
		{"超限在批次中途", 2, []api.Event{ev("k", 1, 1)}, []api.Event{ev("k", 2, 2), ev("k", 3, 3)}, api.ErrTooManyOpen},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, _ := api.New(10, c.maxOpen)
			if _, err := w.Feed(c.seed); err != nil {
				t.Fatal(err)
			}
			before, d0 := w.View(), w.Dropped()
			if _, err := w.Feed(c.bad); !errors.Is(err, c.want) {
				t.Fatalf("err=%v, want %v", err, c.want)
			}
			if !maps.Equal(w.View(), before) || w.Dropped() != d0 {
				t.Fatalf("被拒后状态改变: view=%v dropped=%d", w.View(), w.Dropped())
			}
			if _, err := w.Feed([]api.Event{ev("k", 100, 5)}); err != nil || w.View()["k"] != 5 {
				t.Fatalf("被拒后继续使用出错: err=%v view=%v", err, w.View())
			}
		})
	}
}

func TestSentinelErrors(t *testing.T) {
	_, errSize := api.New(0, 0)
	w, _ := api.New(10, 1)
	_, errKey := w.Feed([]api.Event{ev("", 1, 1)})
	_, errSeed := w.Feed([]api.Event{ev("k", 1, 1)})
	_, errOpen := w.Feed([]api.Event{ev("k", 2, 2)})
	for got, want := range map[error]error{
		errSize: api.ErrBadSize, errKey: api.ErrEmptyKey, errOpen: api.ErrTooManyOpen,
	} {
		if !errors.Is(got, want) {
			t.Errorf("err=%v, want %v", got, want)
		}
	}
	if errSeed != nil || api.ErrBadSize == api.ErrEmptyKey || api.ErrEmptyKey == api.ErrTooManyOpen ||
		api.ErrBadSize == api.ErrTooManyOpen {
		t.Fatal("三类哨兵错误必须可判定且互不相同")
	}
}

func TestConcurrentReaders(t *testing.T) {
	w, _ := api.New(10, 0)
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 500; i++ {
		_, _ = w.Feed([]api.Event{ev(string(rune('a'+rng.Intn(5))), 1+rng.Int63n(1000), rng.Int63n(50))})
	}
	want, wantDrop := w.View(), w.Dropped()
	var wg sync.WaitGroup
	var bad atomic.Bool
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if !maps.Equal(w.View(), want) || w.Dropped() != wantDrop || !w.SelfCheck() {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() || !w.SelfCheck() {
		t.Fatal("并发读到的视图不一致")
	}
}
