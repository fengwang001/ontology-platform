package batcher

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// While Deliver runs, the batch must be neither in Pending nor in Failed.
func TestVisibilityDuringDeliver(t *testing.T) {
	var b *Batcher
	var seenItems, seenBytes, seenFailed int
	sink := SinkFunc(func(batch Batch) error {
		seenItems, seenBytes = b.Pending()
		seenFailed = len(b.Failed())
		return errors.New("boom")
	})
	b = New(sink, 2, 100, time.Hour, nil)
	_ = b.Add("a")
	if err := b.Add("b"); err != nil {
		t.Fatalf("Add = %v, want nil (failure goes to Failed)", err)
	}
	if seenItems != 0 || seenBytes != 0 {
		t.Fatalf("during Deliver pending = %d/%d", seenItems, seenBytes)
	}
	if seenFailed != 0 {
		t.Fatalf("during Deliver failed = %d", seenFailed)
	}
	if got := len(b.Failed()); got != 1 {
		t.Fatalf("after Deliver failed = %d", got)
	}
}

// Concurrent Add/Tick/Flush must be race-clean and conserve messages:
// delivered + failed + pending == accepted Adds.
func TestConcurrentConservation(t *testing.T) {
	var mu sync.Mutex
	delivered := 0
	sink := SinkFunc(func(batch Batch) error {
		if batch.Seq%7 == 0 {
			return errors.New("boom")
		}
		mu.Lock()
		delivered += len(batch.Items)
		mu.Unlock()
		return nil
	})
	clk := &fakeClock{t: time.Unix(0, 0)}
	b := New(sink, 5, 64, time.Millisecond, clk.now)

	var wg sync.WaitGroup
	var acceptedMu sync.Mutex
	accepted := 0
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if err := b.Add(fmt.Sprintf("g%d-%d", g, i)); err == nil {
					acceptedMu.Lock()
					accepted++
					acceptedMu.Unlock()
				}
				if i%10 == 0 {
					clk.advance(time.Millisecond)
					b.Tick()
				}
				if i%25 == 0 {
					_ = b.Flush()
				}
			}
		}(g)
	}
	wg.Wait()
	if err := b.Close(); err != nil {
		t.Logf("Close returned delivery error: %v", err)
	}

	failed := 0
	var seqs []uint64
	for _, fb := range b.Failed() {
		failed += len(fb.Items)
		seqs = append(seqs, fb.Seq)
	}
	pendingItems, pendingBytes := b.Pending()
	mu.Lock()
	got := delivered
	mu.Unlock()
	if got+failed+pendingItems != accepted {
		t.Fatalf("conservation: %d+%d+%d != %d accepted",
			got, failed, pendingItems, accepted)
	}
	if pendingItems == 0 && pendingBytes != 0 {
		t.Fatalf("pending bytes = %d with no items", pendingBytes)
	}
	t.Logf("accepted=%d delivered=%d failed=%d pending=%d",
		accepted, got, failed, pendingItems)
}
