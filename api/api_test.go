package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

func feedOne(ap *api.API, vals []int64) error {
	for _, v := range vals {
		if err := ap.Feed([]api.Event{{Key: "k", Val: v}}); err != nil {
			return err
		}
	}
	return nil
}

// TestBatchRecompute 钉住不变量 1：第 m 次快照 == 前 m*period 个元素的批量重算。
func TestBatchRecompute(t *testing.T) {
	for _, period := range []int64{1, 2, 3, 7} {
		for _, n := range []int{50, 233} {
			rng := rand.New(rand.NewSource(period*1000 + int64(n)))
			vals := make([]int64, n)
			for i := range vals {
				vals[i] = rng.Int63n(2001) - 1000
			}
			ap, _ := api.New(period, n+1)
			if err := feedOne(ap, vals); err != nil {
				t.Fatal(err)
			}
			snaps := ap.Snapshots("k")
			if int64(len(snaps)) != int64(n)/period {
				t.Fatalf("period=%d n=%d: %d snapshots", period, n, len(snaps))
			}
			sum := int64(0)
			for i, s := range snaps {
				for j := int64(i) * period; j < int64(i+1)*period; j++ {
					sum += vals[j]
				}
				if s.Cnt != int64(i+1)*period || s.Sum != sum {
					t.Fatalf("period=%d n=%d snap %d = %+v", period, n, i, s)
				}
			}
		}
	}
}

// TestFailureAtomic 钉住不变量 4：三类错误可判定、互不相同，被拒后状态不变且可继续用。
func TestFailureAtomic(t *testing.T) {
	se := []error{api.ErrBadPeriod, api.ErrEmptyKey, api.ErrTooManySnaps}
	for i, e := range se {
		for j, o := range se {
			if i != j && errors.Is(e, o) {
				t.Fatalf("sentinels %d,%d not distinct", i, j)
			}
		}
	}
	if _, err := api.New(0, 1); !errors.Is(err, api.ErrBadPeriod) {
		t.Fatalf("bad period: %v", err)
	}
	ap, _ := api.New(3, 8)
	if err := feedOne(ap, []int64{10, 20, 30}); err != nil {
		t.Fatal(err)
	}
	if err := ap.Feed([]api.Event{{Key: "k", Val: 1}, {Key: "", Val: 2}}); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatalf("empty key: %v", err)
	}
	if s, c := ap.Totals("k"); s != 60 || c != 3 || len(ap.Snapshots("k")) != 1 {
		t.Fatal("state changed after empty-key rejection")
	}
	apS, _ := api.New(3, 1)
	evs := make([]api.Event, 6)
	for i := range evs {
		evs[i] = api.Event{Key: "k", Val: int64(i + 1)}
	}
	if err := apS.Feed(evs); !errors.Is(err, api.ErrTooManySnaps) {
		t.Fatalf("maxsnap: %v", err)
	}
	if s, c := apS.Totals("k"); s != 0 || c != 0 || len(apS.Snapshots("k")) != 0 {
		t.Fatal("state changed after maxsnap rejection")
	}
	if err := ap.Feed([]api.Event{{Key: "k", Val: 40}}); err != nil {
		t.Fatalf("not usable after rejection: %v", err)
	}
}

// TestConcurrent 钉住并发：N 个 goroutine 各喂不同 Key，Totals 与单线程一致；
// 期间并发读到的快照 cnt 不得回退；SelfCheck 并发可调。不用 sleep。
func TestConcurrent(t *testing.T) {
	const G, N = 8, 500
	ap, _ := api.New(7, N)
	var done, bad atomic.Bool
	var readers, writers sync.WaitGroup
	for r := 0; r < 3; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			last := map[string]int64{}
			for !done.Load() {
				for g := 0; g < G; g++ {
					k := fmt.Sprintf("key-%d", g)
					if sn := ap.Snapshots(k); len(sn) > 0 && sn[len(sn)-1].Cnt < last[k] {
						bad.Store(true)
					} else if len(sn) > 0 {
						last[k] = sn[len(sn)-1].Cnt
					}
				}
				_ = ap.SelfCheck()
			}
		}()
	}
	for g := 0; g < G; g++ {
		writers.Add(1)
		go func(g int) {
			defer writers.Done()
			for i := 0; i < N; i++ {
				_ = ap.Feed([]api.Event{{Key: fmt.Sprintf("key-%d", g), Val: int64(g*N + i)}})
			}
		}(g)
	}
	writers.Wait()
	done.Store(true)
	readers.Wait()
	if bad.Load() {
		t.Fatal("snapshot cnt regressed under concurrency")
	}
	for g := 0; g < G; g++ { // 与单线程公式逐字段相同
		want := int64(g*N*N + N*(N-1)/2)
		if s, c := ap.Totals(fmt.Sprintf("key-%d", g)); s != want || c != N {
			t.Fatalf("key-%d: totals=(%d,%d), want (%d,%d)", g, s, c, want, N)
		}
	}
}
