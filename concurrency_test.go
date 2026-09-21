package ontology

import (
	"sync"
	"testing"
)

// 并发 Add/Remove 结束后：Verify 通过、无负计数、每一点的覆盖数等于
// 该点被添加次数减去被移除次数。
func TestConcurrentAddRemove(t *testing.T) {
	const (
		workers   = 8
		perWorker = 200
	)
	c := New()
	var wg sync.WaitGroup
	// 每个 worker 反复添加并移除自己专属的区间，以及一个全员共享的区间。
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			lo := int64(w * perWorker)
			for i := 0; i < perWorker; i++ {
				if err := c.Add(lo+int64(i), lo+int64(i)+3); err != nil {
					t.Errorf("Add: %v", err)
				}
				if err := c.Add(0, 1<<62); err != nil { // 共享区间，全员叠加
					t.Errorf("Add shared: %v", err)
				}
			}
			for i := 0; i < perWorker; i++ {
				if err := c.Remove(lo+int64(i), lo+int64(i)+3); err != nil {
					t.Errorf("Remove: %v", err)
				}
				if i%2 == 0 {
					if err := c.Remove(0, 1<<62); err != nil { // 共享区间减一半
						t.Errorf("Remove shared: %v", err)
					}
				}
			}
		}(w)
	}
	wg.Wait()
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify after concurrent ops: %v", err)
	}
	// 共享区间净剩 workers * perWorker / 2 层，且覆盖所有采样点。
	want := int64(workers * perWorker / 2)
	for w := 0; w < workers; w++ {
		p := int64(w*perWorker) + 1
		if got := countAt(t, c, p); got != want {
			t.Fatalf("CountAt(%d) = %d, want %d (adds - removes)", p, got, want)
		}
	}
	if got := countAt(t, c, 1<<60); got != want {
		t.Fatalf("shared interval: CountAt = %d, want %d (adds - removes)", got, want)
	}
	mc, _, ok := c.MaxCoverage()
	if !ok || mc != want {
		t.Fatalf("MaxCoverage = (%d, %v), want (%d, true)", mc, ok, want)
	}
}

// 并发进行中的 Segments 不得观察到半更新状态：视图必须始终满足
// 升序、不重叠、无零段、相邻段 Count 不同。
func TestConcurrentSegmentsConsistency(t *testing.T) {
	c := New()
	stop := make(chan struct{})
	var writers, readers sync.WaitGroup
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			lo := int64(w * 1000)
			for i := 0; i < 500; i++ {
				_ = c.Add(lo, lo+500)
				_ = c.Add(lo+250, lo+750)
				_ = c.Remove(lo, lo+500)
				_ = c.Remove(lo+250, lo+750)
			}
		}(w)
	}
	for r := 0; r < 2; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				segs := c.Segments()
				for i, s := range segs {
					if s.Count <= 0 || s.Hi <= s.Lo {
						t.Errorf("bad segment observed: %+v", s)
						return
					}
					if i > 0 {
						prev := segs[i-1]
						if s.Lo < prev.Hi {
							t.Errorf("overlapping segments: %+v %+v", prev, s)
							return
						}
						if s.Lo == prev.Hi && s.Count == prev.Count {
							t.Errorf("unmerged adjacent segments: %+v %+v", prev, s)
							return
						}
					}
				}
				_, _, _ = c.MaxCoverage()
				_, _ = c.CountAt(0)
			}
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	// 所有添加都被成对移除，最终必须为空。
	if segs := c.Segments(); len(segs) != 0 {
		t.Fatalf("final Segments = %v, want empty", segs)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 并发对同一区间增减重数：最终计数精确等于净次数。
func TestConcurrentSameIntervalMultiplicity(t *testing.T) {
	const workers = 16
	const rounds = 100
	c := New()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if err := c.Add(10, 20); err != nil {
					t.Errorf("Add: %v", err)
				}
			}
			for i := 0; i < rounds-1; i++ {
				if err := c.Remove(10, 20); err != nil {
					t.Errorf("Remove: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	want := int64(workers) // 每个 worker 净剩 1 层
	if got := countAt(t, c, 15); got != want {
		t.Fatalf("CountAt(15) = %d, want %d", got, want)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
