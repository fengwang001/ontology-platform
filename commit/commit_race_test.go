package commit

import (
	"maps"
	"sync"
	"sync/atomic"
	"testing"
)

// TestNaiveReplay：并发提交的最终视图等于逐 Key 净增量求和的朴素重放。
func TestNaiveReplay(t *testing.T) {
	sets := [][][]Op{
		{
			{{"a", 1}, {"b", 2}},
			{{"a", 3}, {"c", 5}},
			{{"b", -1}, {"c", 1}},
		},
		{
			{{"x", 1}, {"x", 2}, {"y", 1}}, // 批内同 Key 先去重
			{{"y", -1}, {"z", 7}},
			{{"x", -2}, {"y", 5}, {"z", -2}},
		},
	}
	for i, batches := range sets {
		c, _ := New(64)
		want := map[string]int64{}
		var wg sync.WaitGroup
		for _, b := range batches {
			for _, op := range b {
				want[op.Key] += op.Delta
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := c.Commit(b); err != nil {
					t.Errorf("set %d: %v", i, err)
				}
			}()
		}
		wg.Wait()
		if got := c.View(); !maps.Equal(got, want) {
			t.Errorf("set %d: view = %v, want naive replay %v", i, got, want)
		}
	}
}

// TestAtomicNoPartial：并发读者任何时候都看不到「改了一半」的批（x+y 恒为 0）。
func TestAtomicNoPartial(t *testing.T) {
	c, _ := New(64)
	const writers = 32
	stop := make(chan struct{})
	var rr sync.WaitGroup
	for i := 0; i < 4; i++ {
		rr.Add(1)
		go func() {
			defer rr.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if v := c.View(); v["x"]+v["y"] != 0 {
					t.Errorf("partial commit observed: x=%d y=%d", v["x"], v["y"])
					return
				}
			}
		}()
	}
	var ww sync.WaitGroup
	for i := 0; i < writers; i++ {
		ww.Add(1)
		go func() {
			defer ww.Done()
			if err := c.Commit([]Op{{"x", 1}, {"y", -1}}); err != nil {
				t.Errorf("commit: %v", err)
			}
		}()
	}
	ww.Wait()
	close(stop)
	rr.Wait()
	if v := c.View(); v["x"] != writers || v["y"] != -writers {
		t.Fatalf("final = (%d,%d), want (%d,%d)", v["x"], v["y"], writers, -writers)
	}
}

// TestConcurrentSameKey：N 个写者对同一 Key 各 +1 不丢更新；
// 并发读者的 Retries 始终非负、单调不减。不用 sleep 制造时序。
func TestConcurrentSameKey(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		c, _ := New(4 * n)
		stop := make(chan struct{})
		monotonic := atomic.Bool{}
		monotonic.Store(true)
		var rr sync.WaitGroup
		for i := 0; i < 4; i++ {
			rr.Add(1)
			go func() {
				defer rr.Done()
				prev := int64(0)
				for {
					select {
					case <-stop:
						return
					default:
					}
					_ = c.View()
					if r := c.Retries(); r < 0 || r < prev {
						monotonic.Store(false)
						return
					} else {
						prev = r
					}
				}
			}()
		}
		var ww sync.WaitGroup
		for i := 0; i < n; i++ {
			ww.Add(1)
			go func() {
				defer ww.Done()
				if err := c.Commit([]Op{{"k", 1}}); err != nil {
					t.Errorf("commit: %v", err)
				}
			}()
		}
		ww.Wait()
		close(stop)
		rr.Wait()
		if got := c.View()["k"]; got != int64(n) {
			t.Errorf("n=%d: k = %d, want %d (lost update)", n, got, n)
		}
		if !monotonic.Load() {
			t.Errorf("n=%d: Retries went negative or decreased", n)
		}
	}
}
