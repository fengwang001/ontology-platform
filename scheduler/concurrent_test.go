package scheduler

import (
	"sync"
	"testing"
)

// TestConcurrentSafe 并发混合调用；配合 -race 检测数据竞争。
func TestConcurrentSafe(t *testing.T) {
	s := mustNew(t, 100, 3, 1_000_000)
	_ = s.AddNode(1, Spot, 100)
	_ = s.AddNode(2, OnDemand, 100)
	for i := int64(1); i <= 200; i++ {
		_ = s.AddTask(i, i%256, 500, 50, 10)
	}

	var wg sync.WaitGroup
	launch := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
	}

	launch(func() {
		for k := 0; k < 50; k++ {
			s.Place()
		}
	})
	launch(func() {
		for k := 0; k < 2000; k++ {
			id := int64(k%200) + 1
			_ = s.Report(id, 0)
			_ = s.Budget()
		}
	})
	launch(func() {
		for i := int64(3); i < 30; i++ {
			_ = s.AddNode(i, Spot, 10)
			_, _ = s.Notice(i, 0)
		}
	})

	wg.Wait()

	for id, task := range s.tasks {
		if task.p < task.cp {
			t.Fatalf("task %d p<cp", id)
		}
		if task.st == Running {
			n := s.nodes[task.node]
			if n != nil && n.Kind == Spot && task.rs > 1 {
				t.Fatalf("task %d on Spot with rs=%d", id, task.rs)
			}
		}
	}
	for id, n := range s.nodes {
		if n.used > n.Slots {
			t.Fatalf("node %d over capacity", id)
		}
	}
	if s.Budget() < 0 {
		t.Fatalf("negative budget %d", s.Budget())
	}
}
