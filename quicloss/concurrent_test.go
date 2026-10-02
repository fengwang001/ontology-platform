package quicloss

import (
	"sync"
	"sync/atomic"
	"testing"
)

// Hammer every method from many goroutines with a globally non-decreasing
// clock. This exercises linearization; run with -race.
func TestConcurrentCalls(t *testing.T) {
	c, err := New(25)
	if err != nil {
		t.Fatal(err)
	}
	var clock int64 = 0
	next := func() int64 { return atomic.AddInt64(&clock, 1) }

	var wg sync.WaitGroup
	var nextPN int64

	// Handshake phase workers (H is discarded halfway through).
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				pn := atomic.AddInt64(&nextPN, 1)
				_ = c.Send(next(), SpaceHandshake, pn, 100, true)
				c.Timer()
				_, _ = c.Detect(SpaceHandshake, next())
			}
		}()
	}
	// Application phase workers.
	var nextAPN int64
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				pn := atomic.AddInt64(&nextAPN, 1)
				if err := c.Send(next(), SpaceApp, pn, 100, true); err == nil && pn > 0 {
					_, _ = c.Ack(next(), SpaceApp, []int64{pn - 1}, 0)
				}
				c.Timer()
			}
		}()
	}
	// Timer/timeouts/confirmation worker.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if tm := c.Timer(); tm != nil {
				_, _ = c.OnTimeout(next())
			}
		}
		_ = c.HandshakeConfirmed(next())
	}()

	wg.Wait()
}
