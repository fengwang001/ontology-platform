package ontology

import (
	"math"
	"sync"
	"testing"
)

// 多协程并发 Add：不得丢样本、计数不得错乱；全部喂同一个值时
// 均值必须是精确的这个值（与到达顺序无关）。
func TestConcurrentAdd(t *testing.T) {
	const goroutines = 16
	const perGoroutine = 5000

	a := New()
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if err := a.Add(2.5); err != nil {
					t.Errorf("Add: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	if got, want := a.Count(), uint64(goroutines*perGoroutine); got != want {
		t.Fatalf("Count() = %d, want %d (lost samples under concurrency)", got, want)
	}
	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	if mean != 2.5 {
		t.Fatalf("mean = %v, want exactly 2.5", mean)
	}
	pv, err := a.Variance()
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	if pv != 0 {
		t.Fatalf("population variance = %v, want exactly 0", pv)
	}
}

// 并发 Add 进行中持续读取统计量：不得观察到半更新状态。
// 全部样本取同一个值时，任何时刻读到的均值都必须是这个值，
// 且读到的计数永远不超过已喂入总数（在 -race 下验证无数据竞争）。
func TestConcurrentReadWhileAdding(t *testing.T) {
	const goroutines = 8
	const perGoroutine = 2000
	const total = uint64(goroutines * perGoroutine)

	a := New()
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				count := a.Count()
				if count > total {
					t.Errorf("observed count %d beyond total %d", count, total)
					return
				}
				if count == 0 {
					continue
				}
				mean, err := a.Mean()
				if err != nil {
					t.Errorf("Mean with count=%d: %v", count, err)
					return
				}
				if math.Float64bits(mean) != math.Float64bits(7.0) {
					t.Errorf("observed half-updated state: count=%d mean=%v, want 7", count, mean)
					return
				}
			}
		}()
	}

	var writers sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; i < perGoroutine; i++ {
				if err := a.Add(7.0); err != nil {
					t.Errorf("Add: %v", err)
				}
			}
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	if got := a.Count(); got != total {
		t.Fatalf("Count() = %d, want %d", got, total)
	}
}

// 并发 Merge 只读源累加器：与并发 Add 到各自源同时进行也必须安全。
func TestConcurrentMergeDoesNotBlockWriters(t *testing.T) {
	a := New()
	b := New()
	var wg sync.WaitGroup
	for _, acc := range []*Accumulator{a, b} {
		wg.Add(1)
		go func(acc *Accumulator) {
			defer wg.Done()
			for i := 0; i < 3000; i++ {
				if err := acc.Add(1.0); err != nil {
					t.Errorf("Add: %v", err)
				}
			}
		}(acc)
	}
	for i := 0; i < 100; i++ {
		m := Merge(a, b)
		if m.Count() > 6000 {
			t.Fatalf("merged count %d beyond maximum 6000", m.Count())
		}
	}
	wg.Wait()
	if got := a.Count() + b.Count(); got != 6000 {
		t.Fatalf("total count = %d, want 6000", got)
	}
}
