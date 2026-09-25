package rec

import (
	"sync"
	"testing"
)

// TestFlushNoRescan proves flushTotal is maintained incrementally: the number
// of positions re-accumulated by the last Flush is 0 for every scale m.
func TestFlushNoRescan(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		p := New()
		for i := 1; i <= m; i++ {
			if err := p.Apply(int64(i), int64(i%7-3)); err != nil {
				t.Fatalf("m=%d apply: %v", m, err)
			}
		}
		p.Flush()
		if p.flushScan != 0 {
			t.Fatalf("m=%d: Flush re-accumulated %d positions, want 0", m, p.flushScan)
		}
	}
}

// TestConcurrentSnapshot: after single-threaded feeding, N goroutines read
// Snapshot concurrently (interleaved with idempotent Checkpoints); every
// snapshot must be field-by-field identical. No sleeps: a barrier channel.
func TestConcurrentSnapshot(t *testing.T) {
	p := New()
	for i := 1; i <= 200; i++ {
		if err := p.Apply(int64(i), int64(i*3-2)); err != nil {
			t.Fatal(err)
		}
	}
	p.Flush()
	p.Checkpoint()
	want := p.Snapshot()
	if !want.Ckpt.Valid {
		t.Fatal("expected a checkpoint")
	}
	const n = 16
	start, bad := make(chan struct{}), make(chan struct{}, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < 2000; i++ {
				if g%2 == 1 {
					p.Checkpoint() // idempotent: flushed has not advanced
				}
				if p.Snapshot() != want {
					bad <- struct{}{}
					return
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	if len(bad) != 0 {
		t.Fatal("concurrent snapshots diverged")
	}
	if got := p.Snapshot(); got != want {
		t.Fatalf("state changed under concurrent reads: got %+v want %+v", got, want)
	}
}
