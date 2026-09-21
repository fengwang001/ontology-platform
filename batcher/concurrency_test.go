package batcher

import (
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentConservation hammers Add/Tick/Flush from many goroutines
// and checks the invariant: delivered + failed + pending == accepted,
// and that sequence numbers form a gapless 1..N chain.
func TestConcurrentConservation(t *testing.T) {
	s := &sink{}
	b := New(s, 7, 64, time.Millisecond, nil)

	var accepted atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				err := b.Add("g" + strconv.Itoa(g) + "-" + strconv.Itoa(i))
				if err == nil || err == errDeliver {
					// errDeliver means the item was accepted but its
					// batch delivery failed.
					accepted.Add(1)
				} else if err == ErrTooLarge || err == ErrClosed {
					t.Errorf("unexpected add error: %v", err)
				}
				if i%10 == 0 {
					b.Tick()
				}
				if i%25 == 0 {
					_ = b.Flush()
				}
			}
		}(g)
	}
	// One goroutine injects delivery failures concurrently.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				s.mu.Lock()
				s.failNext++
				s.mu.Unlock()
				time.Sleep(time.Millisecond)
			}
		}
	}()
	wg.Wait()
	close(stop)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	total := 0
	var seqs []uint64
	for _, bt := range s.all() {
		total += len(bt.Items)
		seqs = append(seqs, bt.Seq)
	}
	failed := b.Failed()
	failedN := 0
	for _, bt := range failed {
		failedN += len(bt.Items)
	}
	delivered := total - failedN // s.all() also records failed batches
	pendN, _ := b.Pending()
	if delivered+failedN+pendN != int(accepted.Load()) {
		t.Fatalf("conservation broken: delivered=%d failed=%d pending=%d accepted=%d",
			delivered, failedN, pendN, accepted.Load())
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, sq := range seqs {
		if sq != uint64(i+1) {
			t.Fatalf("seq gap/dup at %d: got %d", i+1, sq)
		}
	}
	if len(seqs) == 0 {
		t.Fatal("no batches produced")
	}
}

// TestConcurrentFailedAccess ensures Failed/Pending stay race-free while
// deliveries are in flight.
func TestConcurrentFailedAccess(t *testing.T) {
	s := &sink{failNext: 1 << 30}
	b := New(s, 3, 1<<20, time.Millisecond, nil)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = b.Add("x")
				_ = b.Failed()
				b.Pending()
			}
		}()
	}
	wg.Wait()
	_ = b.Close()
}
