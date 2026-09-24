package api_test

import (
	"sync"
	"testing"

	"ontology/api"
	"ontology/stats"
)

// SelfCheck 可被测试直接调用，核验四条不变量。
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 并发：N 个 goroutine 只读同一已喂满实例，结果逐字段相同；不用 sleep。
func TestConcurrentReadOnlyViews(t *testing.T) {
	e := api.New()
	for i := 0; i < 200; i++ {
		if err := e.Apply(stats.Op{Kind: stats.OpAdd, Key: "g", X: float64(i) * 1.5}); err != nil {
			t.Fatal(err)
		}
	}
	want := e.View("g")
	const n = 32
	start := make(chan struct{})
	errs := make(chan stats.View, n*10)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				if v := e.View("g"); v != want {
					errs <- v
				}
				if err := e.SelfCheck(); err != nil {
					errs <- stats.View{N: -1}
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for v := range errs {
		t.Errorf("并发读到不一致结果: %+v, want %+v", v, want)
	}
}
