package api

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// 不变量 1：多档 size/R、随机到达顺序下，View 与批量重算逐 (Key,桶) 一致，
// 且 视图计数和 + Dropped == 事件总数（每个事件要么被保留、要么被丢弃）。
func TestViewMatchesBatchRecompute(t *testing.T) {
	confs := []struct{ size, r int64 }{{10, 3}, {1, 1}, {7, 5}, {100, 2}}
	keys := []string{"a", "b", "c"}
	for _, c := range confs {
		for seed := int64(0); seed < 20; seed++ {
			rng := rand.New(rand.NewSource(seed))
			n := 1 + rng.Intn(200)
			evs := make([]Event, n)
			for i := range evs {
				evs[i] = Event{TS: rng.Int63n(400) - 200, Key: keys[rng.Intn(len(keys))]}
			}
			m, err := New(c.size, c.r)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.Feed(evs); err != nil {
				t.Fatal(err)
			}
			want := batchRecompute(evs, c.size, c.r)
			if !reflect.DeepEqual(m.View(), want) {
				t.Fatalf("conf=%+v seed=%d: view=%v, want %v", c, seed, m.View(), want)
			}
			var kept int64
			for _, b := range m.View() {
				for _, cnt := range b {
					kept += cnt
				}
			}
			if kept+m.Dropped() != int64(n) {
				t.Fatalf("conf=%+v seed=%d: kept=%d dropped=%d total=%d", c, seed, kept, m.Dropped(), n)
			}
		}
	}
}

// 不变量 3：cur 单调不减；保留桶数 <= R 且桶键都在 [cur-R+1, cur] 内。
func TestRetentionBounds(t *testing.T) {
	confs := []struct{ size, r int64 }{{10, 3}, {5, 1}, {3, 8}}
	for _, c := range confs {
		rng := rand.New(rand.NewSource(42))
		m, _ := New(c.size, c.r)
		var prevCur int64
		hasPrev := false
		for batch := 0; batch < 30; batch++ {
			evs := make([]Event, 1+rng.Intn(10))
			for i := range evs {
				evs[i] = Event{TS: rng.Int63n(300) - 100, Key: "k"}
			}
			if err := m.Feed(evs); err != nil {
				t.Fatal(err)
			}
			view := m.View()["k"]
			if len(view) == 0 {
				continue
			}
			if len(view) > int(c.r) {
				t.Fatalf("conf=%+v: retained %d buckets > R=%d", c, len(view), c.r)
			}
			cur := int64(-1) << 62
			for k := range view {
				if k > cur {
					cur = k
				}
			}
			if hasPrev && cur < prevCur {
				t.Fatalf("conf=%+v: cur 回退 %d -> %d", c, prevCur, cur)
			}
			prevCur, hasPrev = cur, true
			for k := range view {
				if k < cur-c.r+1 || k > cur {
					t.Fatalf("conf=%+v: bucket %d outside [%d,%d]", c, k, cur-c.r+1, cur)
				}
			}
		}
	}
}

// 并发：N 个 goroutine 并发只读 View/Dropped/SelfCheck，结果逐 (Key,桶) 相同。
func TestConcurrentReads(t *testing.T) {
	m, _ := New(10, 3)
	rng := rand.New(rand.NewSource(7))
	evs := make([]Event, 500)
	for i := range evs {
		evs[i] = Event{TS: rng.Int63n(1000) - 500, Key: "k"}
	}
	if err := m.Feed(evs); err != nil {
		t.Fatal(err)
	}
	wantView, wantDrop := m.View(), m.Dropped()
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				if !reflect.DeepEqual(m.View(), wantView) {
					errs <- "view differs"
					return
				}
				if m.Dropped() != wantDrop {
					errs <- "dropped differs"
					return
				}
				if err := m.SelfCheck(); err != nil {
					errs <- err.Error()
					return
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

// 自检方法本身必须通过。
func TestSelfCheck(t *testing.T) {
	m, err := New(10, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
