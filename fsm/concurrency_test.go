package fsm

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pingPong builds a two-state loop: a --ping--> b --pong--> a.
func pingPong(t *testing.T) *Machine {
	t.Helper()
	m, err := New("a", nil, []Transition{
		{From: "a", Event: "ping", To: "b"},
		{From: "b", Event: "pong", To: "a"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func TestConcurrentFireLogsEverySuccess(t *testing.T) {
	m := pingPong(t)
	const goroutines = 8
	const firesEach = 250
	var successes atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < firesEach; i++ {
				e := Event("ping")
				if (g+i)%2 == 1 {
					e = "pong"
				}
				if _, err := m.Fire(e); err == nil {
					successes.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()
	if got := int64(len(m.Log())); got != successes.Load() {
		t.Fatalf("log length = %d, successful fires = %d", got, successes.Load())
	}
	log := m.Log()
	for i, tr := range log {
		want := State("b")
		if i%2 == 1 {
			want = "a"
		}
		if tr.To != want {
			t.Fatalf("log[%d].To = %q, want %q", i, tr.To, want)
		}
	}
}

func TestSlowObserverNeverBlocksFire(t *testing.T) {
	m := pingPong(t)
	m.Observe() // subscribed but never drained; buffer fills quickly
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			e := Event("ping")
			if i%2 == 1 {
				e = "pong"
			}
			if _, err := m.Fire(e); err != nil {
				t.Errorf("fire %d: %v", i, err)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Fire blocked on a slow observer")
	}
}

func TestObserverReceivesInOrderSubsequence(t *testing.T) {
	m := pingPong(t)
	ch := m.Observe()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				e := Event("ping")
				if (g+i)%2 == 1 {
					e = "pong"
				}
				m.Fire(e) // failures are fine here
			}
		}(g)
	}
	wg.Wait()
	var received []State
drain:
	for {
		select {
		case s := <-ch:
			received = append(received, s)
		default:
			break drain
		}
	}
	// Whatever was delivered must be an in-order subsequence of the
	// logged To states (drops only remove elements, never reorder).
	log := m.Log()
	i := 0
	for _, tr := range log {
		if i < len(received) && received[i] == tr.To {
			i++
		}
	}
	if i != len(received) {
		t.Fatalf("received %v is not a subsequence of logged states", received)
	}
}
