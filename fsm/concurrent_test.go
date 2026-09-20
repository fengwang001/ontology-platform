package fsm

import (
	"errors"
	"sync"
	"testing"
)

func TestConcurrentFire(t *testing.T) {
	// 线性状态链 s0 -> s1 -> ... -> sN，每个状态只接受唯一事件，
	// 因此任意时刻只有一种事件能成功转移。
	const n = 200
	table := make([]Transition, 0, n)
	for i := 0; i < n; i++ {
		table = append(table, Transition{
			From:  State("s" + itoaTest(i)),
			Event: Event("e" + itoaTest(i)),
			To:    State("s" + itoaTest(i+1)),
		})
	}
	m, err := New("s0", nil, table)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		e := Event("e" + itoaTest(i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				got, err := m.Fire(e)
				_ = got
				switch {
				case err == nil:
					return
				case errors.Is(err, ErrNoTransition):
					// 事件尚未轮到或已被别的 goroutine 消费，重试。
				default:
					t.Errorf("unexpected Fire error: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	log := m.Log()
	if len(log) != n {
		t.Fatalf("successful transitions = %d, want %d (重/漏)", len(log), n)
	}
	if m.State() != State("s"+itoaTest(n)) {
		t.Fatalf("final state = %q", m.State())
	}
	// 日志必须严格按链顺序。
	for i, tr := range log {
		want := Transition{
			From:  State("s" + itoaTest(i)),
			Event: Event("e" + itoaTest(i)),
			To:    State("s" + itoaTest(i+1)),
		}
		if tr != want {
			t.Fatalf("log[%d] = %v, want %v", i, tr, want)
		}
	}
}

func TestConcurrentFireWithObservers(t *testing.T) {
	m, _ := New("a", nil, []Transition{
		{From: "a", Event: "n", To: "b"},
		{From: "b", Event: "n", To: "a"},
	})
	const observers = 8
	obs := make([]<-chan State, observers)
	for i := range obs {
		obs[i] = m.Observe()
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, err := m.Fire("n"); err != nil {
					t.Errorf("Fire: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if len(m.Log()) != 400 {
		t.Fatalf("log len = %d, want 400", len(m.Log()))
	}
	// 每个观察者收到的序列必须是完整 Log.To 序列的有序子序列。
	log := m.Log()
	for oi, ch := range obs {
		idx := 0
		count := 0
	drain:
		for {
			select {
			case got := <-ch:
				for idx < len(log) && log[idx].To != got {
					idx++
				}
				if idx >= len(log) {
					t.Fatalf("observer %d delivered out-of-order %q", oi, got)
				}
				idx++
				count++
			default:
				break drain
			}
		}
		if count > 400 {
			t.Fatalf("observer %d got %d > 400", oi, count)
		}
	}
}

func itoaTest(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
