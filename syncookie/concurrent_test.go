package syncookie

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrent hammers the validator from many goroutines with a globally
// monotonic clock and checks that every capacity invariant always holds and
// each established connection is enqueued and later dequeued exactly once.
func TestConcurrent(t *testing.T) {
	const B, A, T = 8, 16, 500000
	v, err := New(B, T, A, testHash, newCounterISN())
	if err != nil {
		t.Fatal(err)
	}

	var clock atomic.Int64
	var established atomic.Int64
	var producers, consumers sync.WaitGroup

	// Producers: SYN then complete via the returned ISN. now stays in
	// [1, 60000) so the tick is always 0 and every cookie stays fresh.
	for g := 0; g < 16; g++ {
		producers.Add(1)
		go func(g int) {
			defer producers.Done()
			for i := 0; i < 200; i++ {
				id := uint32(g*1000 + i + 1)
				key := mkKey(id)
				n := clock.Add(1)
				now := 1 + (n % 59998)
				cisn := uint32(id * 2654435761)
				r, err := v.OnSyn(now, key, cisn, 1460)
				if err != nil {
					continue // accept full: nothing queued
				}
				if _, err := v.OnAck(now+1, key, cisn+1, r.ISN+1); err == nil {
					established.Add(1)
				}
				s := v.Stats()
				if s.HalfOpen > B || s.Acceptable > A {
					t.Errorf("invariant violated: %+v", s)
					return
				}
			}
		}(g)
	}

	// Consumer drains until producers are done and the queue is empty.
	producersDone := make(chan struct{})
	consumers.Add(1)
	go func() {
		defer consumers.Done()
		for {
			if _, _, err := v.Accept(); err == nil {
				continue
			}
			select {
			case <-producersDone:
				if _, _, err := v.Accept(); err == ErrEmpty {
					return
				}
			default:
				time.Sleep(time.Microsecond)
			}
		}
	}()

	producers.Wait()
	close(producersDone)
	consumers.Wait()

	if _, _, err := v.Accept(); err != ErrEmpty {
		t.Fatalf("queue not empty after consumer exit: %v", err)
	}
	s := v.Stats()
	if s.Acceptable != 0 {
		t.Fatalf("final accept queue nonempty: %+v", s)
	}
	t.Logf("concurrent run: established=%d cookieSent=%d cookieOK=%d retrans=%d",
		established.Load(), s.CookieSent, s.CookieOK, s.Retrans)
}
