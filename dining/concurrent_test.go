package dining

import (
	"sync"
	"testing"
)

// TestConcurrentAccess 用多个 goroutine 并发调用状态转移与投递；
// 配合 `go test -race` 验证数据竞争安全，并在结束时校验全部不变量。
func TestConcurrentAccess(t *testing.T) {
	procs, edges := ringTopology(6)
	net := NewQueueNetwork()
	c, err := NewCoordinator(procs, edges, net, &SliceLogger{})
	if err != nil {
		t.Fatal(err)
	}

	const workers, rounds = 8, 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				p := procs[(id+i)%len(procs)]
				s, err := c.State(p)
				if err != nil {
					t.Error(err)
					return
				}
				switch s {
				case Thinking:
					_ = c.BecomeHungry(p)
				case Hungry:
					if canEat(c, p) {
						_ = c.StartEating(p)
					}
				case Eating:
					_ = c.FinishEating(p)
				}
				// 并发地注入队首消息。
				if pending := c.Pending(); len(pending) > 0 {
					_ = c.Deliver(pending[0])
				}
				_ = c.CheckInvariants()
			}
		}(w)
	}
	wg.Wait()

	// 排空并保证最终无人饿死。
	if left := fairDrain(t, c, procs, -1); len(left) != 0 {
		t.Fatalf("starved after concurrent phase: %v", left)
	}
	if v := c.CheckInvariants(); len(v) != 0 {
		t.Fatalf("final invariants: %v", v)
	}
}
