package correlate

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentDeliveryExactlyOnce(t *testing.T) {
	c, _ := newTestCorrelator(t, 1)
	id := mustRequest(t, c, time.Minute)

	const n = 64
	var wg sync.WaitGroup
	var success, idle int64
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			switch err := c.Deliver(id); {
			case err == nil:
				atomic.AddInt64(&success, 1)
			case errors.Is(err, ErrIdleID):
				atomic.AddInt64(&idle, 1)
			default:
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if success != 1 || idle != n-1 {
		t.Fatalf("success=%d idle=%d", success, idle)
	}
	cnt := c.Counters()
	if cnt.Delivered != 1 || cnt.Idle != n-1 {
		t.Fatalf("counters = %+v", cnt)
	}
	if c.InFlight() != 0 {
		t.Fatalf("in flight = %d, want 0", c.InFlight())
	}
}

func TestConcurrentMixedOpsStaysConsistent(t *testing.T) {
	c, _ := newTestCorrelator(t, 8)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if id, err := c.Request(50 * time.Millisecond); err == nil {
						c.Lookup(id)
						_ = c.Deliver(id)
					}
				}
			}
		}()
	}
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					c.InFlight()
					c.Counters()
				}
			}
		}()
	}

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	cnt := c.Counters()
	if cnt.Delivered+cnt.Idle+cnt.Stale+cnt.Unknown < cnt.Delivered {
		t.Fatal("counter self-consistency failed")
	}
}
