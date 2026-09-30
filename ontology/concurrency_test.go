package ontology

import (
	"bytes"
	"sync"
	"testing"
)

// 同一分区并发开始恰好成功一次；所有方法可并发调用。
func TestConcurrentStartSucceedsOnce(t *testing.T) {
	p, _ := basicGraph(t)
	if err := p.ExternalWrite("S", 3); err != nil {
		t.Fatal(err)
	}

	const n = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes, rejected int
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := p.Start("A", 3); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			} else if reasonOf(err) == ReasonAlreadyRunning {
				mu.Lock()
				rejected++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 || rejected != n-1 {
		t.Fatalf("successes=%d rejected=%d", successes, rejected)
	}
}

func TestConcurrentMixedOperations(t *testing.T) {
	var logBuf bytes.Buffer
	p, err := New(
		[]AssetSpec{{"S", 0, 100}, {"A", 0, 100}, {"B", 0, 100}},
		[]EdgeSpec{{"S", "A", 0, 0}, {"A", "B", 0, 0}},
		WithLogOutput(&logBuf))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	// 一个协调者按依赖顺序推进，其余 goroutine 不断做只读判定与外部写。
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = p.Plan([]PartitionRef{{"B", 7}})
					_, _ = p.Impact("S", 7)
				}
			}
		}()
	}

	if err := p.ExternalWrite("S", 7); err != nil {
		t.Fatal(err)
	}
	ra := mustStart(t, p, "A", 7)
	if err := p.Complete(ra, true); err != nil {
		t.Fatal(err)
	}
	rb := mustStart(t, p, "B", 7)
	if err := p.Complete(rb, true); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()

	plan, err := p.Plan([]PartitionRef{{"B", 7}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 0 {
		t.Fatalf("fresh B#7 plan=%v", plan)
	}
}
