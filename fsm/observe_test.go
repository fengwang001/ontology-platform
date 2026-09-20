package fsm

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// 语义 6：一次成功迁移 exit/entry 恰好各一次，先 exit 后 entry；
// 同一状态多次注册按注册顺序全部执行。
func TestActionsRunExactlyOnceInOrder(t *testing.T) {
	m := newTestMachine(t)
	var seq []string
	m.OnExit("idle", func() { seq = append(seq, "exit1") })
	m.OnExit("idle", func() { seq = append(seq, "exit2") })
	m.OnEntry("active", func() error { seq = append(seq, "entry1"); return nil })
	m.OnEntry("active", func() error { seq = append(seq, "entry2"); return nil })

	if _, err := m.Fire("connect"); err != nil {
		t.Fatal(err)
	}
	want := []string{"exit1", "exit2", "entry1", "entry2"}
	if fmt.Sprint(seq) != fmt.Sprint(want) {
		t.Fatalf("seq = %v, want %v", seq, want)
	}
}

// 语义 7：日志与观察者序列一一对应；Log 返回副本，外部改动不影响内部。
func TestLogMatchesObservers(t *testing.T) {
	m := newTestMachine(t)
	ch1 := m.Observe()
	ch2 := m.Observe()
	events := []Event{"connect", "ping", "ping", "close"}
	for _, e := range events {
		if _, err := m.Fire(e); err != nil {
			t.Fatal(err)
		}
	}
	log := m.Log()
	if len(log) != len(events) {
		t.Fatalf("log len = %d, want %d", len(log), len(events))
	}
	for i, tr := range log {
		for _, ch := range []<-chan State{ch1, ch2} {
			got, ok := recvWithTimeout(t, ch)
			if !ok || got != tr.To {
				t.Fatalf("transition %d: observed %q, log.To %q", i, got, tr.To)
			}
		}
	}

	log[0].To = "corrupted"
	if m.Log()[0].To != "active" {
		t.Fatal("mutating returned log must not affect internal state")
	}
}

// 语义 8：并发 Fire 下日志不重不漏，慢观察者不阻塞 Fire。
func TestConcurrentFire(t *testing.T) {
	table := []Transition{
		{From: "s0", Event: "go", To: "s1"},
		{From: "s1", Event: "go", To: "s0"},
	}
	m, err := New("s0", nil, table)
	if err != nil {
		t.Fatal(err)
	}
	slow := m.Observe() // 从不消费，缓冲满后丢弃，不得阻塞 Fire。

	const workers = 8
	const perWorker = 500
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := m.Fire("go"); err == nil {
					mu.Lock()
					succeeded++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	log := m.Log()
	if len(log) != succeeded {
		t.Fatalf("log len = %d, successes = %d", len(log), succeeded)
	}
	for i, tr := range log {
		wantFrom, wantTo := State("s0"), State("s1")
		if i%2 == 1 {
			wantFrom, wantTo = wantTo, wantFrom
		}
		if tr.From != wantFrom || tr.To != wantTo {
			t.Fatalf("log[%d] = %+v, want %q->%q", i, tr, wantFrom, wantTo)
		}
	}

	// 慢观察者收到的是按序子序列，首条必为 s1。
	got, ok := <-slow
	if !ok || got != "s1" {
		t.Fatalf("slow observer first state = %q, %v", got, ok)
	}
	prev := got
	for {
		select {
		case s := <-slow:
			if s == prev {
				t.Fatalf("slow observer got duplicate consecutive state %q", s)
			}
			prev = s
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}
