package ontology

import (
	"sync"
	"testing"
)

// 多协程并发 Add：不得丢样本、计数不得错乱；
// 并发读取统计量不得观察到半更新状态（由 -race 与不变量共同保证）。
func TestConcurrentAdd(t *testing.T) {
	const goroutines = 8
	const perG = 1250
	const total = goroutines * perG

	a := New()
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := a.Add(float64(g*perG + i)); err != nil {
					t.Errorf("Add 返回错误: %v", err)
				}
			}
		}(g)
	}

	// 写入进行中并发读取：不得报错、不得出现负方差。
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
				n := a.Count()
				if n > 0 {
					if _, err := a.Mean(); err != nil {
						t.Errorf("并发读取 Mean 报错: %v", err)
					}
					if v, err := a.Variance(); err != nil || v < 0 {
						t.Errorf("并发读取 Variance = %v, %v", v, err)
					}
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	readers.Wait()

	if got := a.Count(); got != total {
		t.Fatalf("Count = %d, want %d（并发 Add 丢样本）", got, total)
	}
	// 样本为 0..total-1，均值为 (total-1)/2。
	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean 返回错误: %v", err)
	}
	if want := float64(total-1) / 2; relErr(mean, want) > 1e-12 {
		t.Fatalf("Mean = %v, want %v", mean, want)
	}
	// 0..n-1 的总体方差为 (n^2-1)/12。
	pv, err := a.Variance()
	if err != nil {
		t.Fatalf("Variance 返回错误: %v", err)
	}
	wantPV := (float64(total)*float64(total) - 1) / 12
	if relErr(pv, wantPV) > 1e-9 {
		t.Fatalf("Variance = %v, want %v", pv, wantPV)
	}
}

// 并发 Merge 与 Add 混合：Merge 只读源，不得与并发写入产生数据竞争。
func TestConcurrentMergeWhileAdding(t *testing.T) {
	a := New()
	b := New()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_ = a.Add(float64(i))
				_ = b.Add(float64(2 * i))
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				m := Merge(a, b)
				_ = m.Count()
			}
		}()
	}
	wg.Wait()
	if got := a.Count(); got != 2000 {
		t.Fatalf("a.Count = %d, want 2000", got)
	}
	if got := b.Count(); got != 2000 {
		t.Fatalf("b.Count = %d, want 2000", got)
	}
}
