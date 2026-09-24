package api_test

import (
	"errors"
	"sort"
	"sync"
	"testing"

	"ontology/api"
	"ontology/evt"
	"ontology/sess"
)

type S = api.Session

func mkE(ts ...int64) []evt.Event {
	r := make([]evt.Event, len(ts))
	for i, t := range ts {
		r[i] = evt.Event{Key: "k", TS: t}
	}
	return r
}

// recompute：全部事件排序后从头扫一遍分会话（题面参考实现）。
func recompute(ts []int64, gap int64) []S {
	cp := append([]int64(nil), ts...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	var out []S
	for _, t := range cp {
		if n := len(out); n > 0 && t-out[n-1].End <= gap {
			out[n-1].End, out[n-1].N = t, out[n-1].N+1
		} else {
			out = append(out, S{Start: t, End: t, N: 1})
		}
	}
	return out
}

// TestNew 钉住构造期 gap 校验。
func TestNew(t *testing.T) {
	for _, g := range []int64{0, -1} {
		if _, err := api.New(g, 0); !errors.Is(err, api.ErrBadGap) {
			t.Fatalf("gap=%d: %v", g, err)
		}
	}
	if a, err := api.New(10, 0); err != nil || a == nil {
		t.Fatalf("valid New: %v", err)
	}
}

// TestFeedAtomic 钉住不变量 4：任一批失败则整批不留痕，成功后与排序重算逐会话一致。
func TestFeedAtomic(t *testing.T) {
	for _, c := range []struct {
		name string
		max  int
		evs  []evt.Event
		want error
	}{
		{"empty key", 0, []evt.Event{{Key: "k", TS: 1}, {Key: "", TS: 2}}, api.ErrInvalidEvent},
		{"over limit", 2, mkE(0, 100, 200), api.ErrTooMany},
	} {
		a, _ := api.New(10, c.max)
		if err := a.Feed(mkE(500)); err != nil { // 预置一个既有会话
			t.Fatal(err)
		}
		before := a.Snapshot("k")
		if err := a.Feed(c.evs); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
		if got := a.Snapshot("k"); !sess.Equal(got, before) {
			t.Errorf("%s: state changed after rejected feed: %v != %v", c.name, got, before)
		}
	}
	batch := []int64{100, 105, 130, 135, 118, 100, 3, 12}
	a, _ := api.New(10, 0)
	if err := a.Feed(mkE(batch...)); err != nil {
		t.Fatal(err)
	}
	if got, want := a.Snapshot("k"), recompute(batch, 10); !sess.Equal(got, want) {
		t.Fatalf("accepted feed: %v != %v", got, want)
	}
}

// TestSelfCheck 钉住内置自检：四条不变量核验必须全部通过。
func TestSelfCheck(t *testing.T) {
	a, err := api.New(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentSnapshot 钉住并发：N 个 goroutine 在同一已喂满集合上并发只读
// Snapshot 并混跑 SelfCheck，各自结果必须逐字段相同；用起跑线 channel 同步，无 sleep。
func TestConcurrentSnapshot(t *testing.T) {
	a, _ := api.New(10, 0)
	batch := []int64{100, 105, 130, 135, 118, 100, 200, 190, 3}
	if err := a.Feed(mkE(batch...)); err != nil {
		t.Fatal(err)
	}
	ref := a.Snapshot("k")
	const N = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	failed := false
	fail := func(format string, args ...any) {
		mu.Lock()
		failed = true
		mu.Unlock()
		t.Errorf(format, args...)
	}
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start // 全部 goroutine 同一刻起跑，不使用 sleep
			for i := 0; i < 200; i++ {
				if got := a.Snapshot("k"); !sess.Equal(got, ref) {
					fail("snapshot diverged: %v != %v", got, ref)
					return
				}
				if id%2 == 0 {
					if err := a.SelfCheck(); err != nil {
						fail("SelfCheck under concurrency: %v", err)
						return
					}
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	if failed {
		t.Fatal("concurrent readers observed inconsistent state")
	}
}
