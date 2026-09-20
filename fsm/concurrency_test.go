package fsm

import (
	"sync"
	"testing"
)

// TestConcurrentFire 并发触发同一事件：日志长度必须恰好等于
// 成功迁移次数，且日志中的转移首尾相接（不重不漏）。
func TestConcurrentFire(t *testing.T) {
	table := []Transition{
		{From: "s0", Event: "step", To: "s1"},
		{From: "s1", Event: "step", To: "s2"},
		{From: "s2", Event: "step", To: "s0"},
	}
	m := mustNew(t, "s0", nil, table)

	const workers = 8
	const perWorker = 200
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := 0
			for i := 0; i < perWorker; i++ {
				if _, err := m.Fire("step"); err == nil {
					local++
				}
			}
			mu.Lock()
			successes += local
			mu.Unlock()
		}()
	}
	wg.Wait()

	log := m.Log()
	if len(log) != successes {
		t.Fatalf("log length = %d, want %d (successful fires)", len(log), successes)
	}
	for i, tr := range log {
		wantFrom := State("s0")
		if i > 0 {
			wantFrom = log[i-1].To
		}
		if tr.From != wantFrom {
			t.Fatalf("log[%d].From = %q, want %q (chain broken)", i, tr.From, wantFrom)
		}
	}
}

// TestSlowObserverNeverBlocksFire 慢观察者（从不读取）不得阻塞
// Fire；及时读取的观察者收到的序列必须是日志 To 序列的保序子序列。
func TestSlowObserverNeverBlocksFire(t *testing.T) {
	table := []Transition{
		{From: "s0", Event: "step", To: "s1"},
		{From: "s1", Event: "step", To: "s0"},
	}
	m := mustNew(t, "s0", nil, table)
	slow := m.Observe() // 从不读取，缓冲满后通知被丢弃
	fast := m.Observe()

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				m.Fire("step")
			}
		}()
	}

	// 并发地尽量快地收取 fast 通道。
	var mu sync.Mutex
	var received []State
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case s := <-fast:
				mu.Lock()
				received = append(received, s)
				mu.Unlock()
			default:
				return
			}
		}
	}()
	wg.Wait()
	<-done

	if len(slow) > observerBuffer {
		t.Fatalf("slow observer buffer = %d, exceeds capacity %d", len(slow), observerBuffer)
	}

	// received 必须是日志 To 序列的保序子序列。
	mu.Lock()
	defer mu.Unlock()
	log := m.Log()
	idx := 0
	for _, tr := range log {
		if idx < len(received) && received[idx] == tr.To {
			idx++
		}
	}
	if idx != len(received) {
		t.Fatalf("received sequence is not an in-order subsequence of log (matched %d/%d)",
			idx, len(received))
	}
}
