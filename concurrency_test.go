package ontology

import (
	"sync"
	"testing"
)

// 多协程并发 Add：不得丢样本；结束后计数精确、均值与真值一致。
// 并发进行中持续读取统计量，-race 下验证不存在半更新状态的数据竞争。
func TestConcurrentAddAndRead(t *testing.T) {
	const goroutines = 8
	const perG = 10000
	const total = goroutines * perG

	var a Accumulator
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 读者：并发进行中不断读取统计量。
	var readers sync.WaitGroup
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
				_, _ = a.Mean()
				_, _ = a.PopulationVariance()
				_, _ = a.SampleVariance()
				_ = a.Count()
			}
		}()
	}

	// 写者：第 g 个协程喂入 [g*perG, (g+1)*perG)。
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := a.Add(float64(base + i)); err != nil {
					t.Errorf("Add: %v", err)
				}
			}
		}(g * perG)
	}
	wg.Wait()
	close(stop)
	readers.Wait()

	if got := a.Count(); got != total {
		t.Fatalf("Count = %d, want %d (lost samples)", got, total)
	}
	// 0..total-1 的真实均值为 (total-1)/2 = 39999.5，在 float64 下精确可表示。
	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	if e := relErr(mean, float64(total-1)/2); e > 1e-12 {
		t.Fatalf("mean = %v, rel err %v > 1e-12", mean, e)
	}
	if a.Skipped() != 0 {
		t.Fatalf("Skipped = %d, want 0", a.Skipped())
	}
}

// 并发 Merge 与读取同时进行：源累加器不得被 Merge 修改。
func TestConcurrentMergeDoesNotMutate(t *testing.T) {
	a := mustAcc(t, []float64{1, 2, 3, 4, 5})
	b := mustAcc(t, []float64{10, 20, 30})
	an, amean, am2 := bitsOf(t, a)
	bn, bmean, bm2 := bitsOf(t, b)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				m := Merge(a, b)
				if m.Count() != 8 {
					t.Errorf("merged Count = %d, want 8", m.Count())
				}
			}
		}()
	}
	wg.Wait()

	if n, mean, m2 := bitsOf(t, a); n != an || mean != amean || m2 != am2 {
		t.Fatal("concurrent Merge mutated source a")
	}
	if n, mean, m2 := bitsOf(t, b); n != bn || mean != bmean || m2 != bm2 {
		t.Fatal("concurrent Merge mutated source b")
	}
}
