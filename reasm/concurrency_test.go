package reasm

import (
	"sync"
	"testing"
	"time"
)

// TestConcurrentSingleDeliverer hammers one message from many
// goroutines and requires exactly one caller to observe completion,
// with byte-exact payload.
func TestConcurrentSingleDeliverer(t *testing.T) {
	c := newClock()
	r := New(c.Now, time.Hour, 1<<20)

	full := make([]byte, 4000)
	for i := range full {
		full[i] = byte(i * 31)
	}
	const chunk = 100
	n := len(full) / chunk

	var wg sync.WaitGroup
	var mu sync.Mutex
	deliveries := 0
	var assembled []byte

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			off := i * chunk
			msg, done, err := r.Submit("race", off, full[off:off+chunk], len(full))
			if err != nil {
				t.Errorf("submit: %v", err)
				return
			}
			if done {
				mu.Lock()
				deliveries++
				assembled = msg
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if deliveries != 1 {
		t.Fatalf("delivered %d times, want exactly 1", deliveries)
	}
	if len(assembled) != len(full) {
		t.Fatalf("assembled length %d", len(assembled))
	}
	for i := range full {
		if assembled[i] != full[i] {
			t.Fatalf("byte %d differs", i)
		}
	}
	if r.Used() != 0 {
		t.Fatalf("budget leaked: %d", r.Used())
	}
}

// TestConcurrentMixedMessages submits fragments for several messages
// concurrently; every message must be delivered exactly once.
func TestConcurrentMixedMessages(t *testing.T) {
	c := newClock()
	r := New(c.Now, time.Hour, 1<<20)

	const msgs = 8
	const size = 500
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := map[string]int{}

	for m := 0; m < msgs; m++ {
		id := string(rune('a' + m))
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(m + i)
		}
		for off := 0; off < size; off += 50 {
			wg.Add(1)
			go func(id string, off int, data []byte) {
				defer wg.Done()
				end := off + 50
				_, ok, err := r.Submit(id, off, data[off:end], size)
				if err != nil {
					t.Errorf("submit %s: %v", id, err)
					return
				}
				if ok {
					mu.Lock()
					done[id]++
					mu.Unlock()
				}
			}(id, off, data)
		}
	}
	wg.Wait()

	for m := 0; m < msgs; m++ {
		id := string(rune('a' + m))
		if done[id] != 1 {
			t.Fatalf("message %q delivered %d times", id, done[id])
		}
	}
	if r.Used() != 0 {
		t.Fatalf("budget leaked: %d", r.Used())
	}
}
