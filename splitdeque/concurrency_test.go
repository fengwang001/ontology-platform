package splitdeque

import (
	"sync"
	"testing"
)

func TestConcurrentOperations(t *testing.T) {
	d := mustNew(t, 256, 64, 2, 5)

	var wg sync.WaitGroup
	var pushed int64
	var popped int64
	var stolen int64

	wg.Add(3)
	go func() {
		defer wg.Done()
		for value := int64(0); value < 1000; value++ {
			if err := d.Push(value); err == nil {
				pushed++
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			if _, ok := d.Pop(); ok {
				popped++
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			values, err := d.Steal(7)
			if err != nil {
				t.Errorf("Steal(7) error: %v", err)
				return
			}
			stolen += int64(len(values))
		}
	}()

	wg.Wait()

	d.mu.Lock()
	remaining := d.bottom - d.top
	shared := d.split - d.top
	d.mu.Unlock()

	stats := d.Stats()
	if stats.Pushed != pushed || stats.Popped != popped || stats.Stolen != stolen {
		t.Fatalf("local counts = pushed/popped/stolen %d/%d/%d; stats = %+v",
			pushed, popped, stolen, stats)
	}
	if stats.Pushed != stats.Popped+stats.Stolen+remaining {
		t.Fatalf("accounting: pushed=%d popped=%d stolen=%d remaining=%d",
			stats.Pushed, stats.Popped, stats.Stolen, remaining)
	}
	if shared > 64 {
		t.Fatalf("shared size %d exceeds limit 64", shared)
	}
	if d.movedElements != 0 {
		t.Fatalf("moved elements = %d; want 0", d.movedElements)
	}
	if d.copiedElements != stolen {
		t.Fatalf("copied elements = %d; want stolen %d", d.copiedElements, stolen)
	}
}
