// Command demo exercises the idempotent write gateway end to end and
// prints one OK/FAIL line per guaranteed semantic. It takes no
// arguments, uses no network, and always exits with code 0.
package main

import (
	"fmt"
	"sync"
	"time"

	"ontology/gateway"
)

// clock is a manually advanced clock injected into every gateway.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock {
	return &clock{t: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

const ttl = time.Minute

// newGateway builds a gateway on a fresh fake clock.
func newGateway() (*gateway.Gateway, *clock) {
	c := newClock()
	return gateway.New(c.now, ttl), c
}

func ok(value string) gateway.ExecFunc {
	return func() (any, error) { return value, nil }
}

func main() {
	checks := []struct {
		name string
		fn   func() bool
	}{
		{"exactly-once execution", checkExactlyOnce},
		{"replay is distinguishable and equal", checkReplay},
		{"conflicting body rejected", checkConflict},
		{"failure retryable, success final", checkFailureRetry},
		{"concurrent duplicates join", checkConcurrentJoin},
		{"in-flight conflict fails fast", checkInFlightConflict},
		{"expired key re-executes", checkExpiryReexecutes},
		{"expiry spares in-flight records", checkExpirySparesInFlight},
		{"query of missing key is zero", checkQueryZero},
		{"slow key never blocks others", checkNoGlobalLock},
	}
	passed := 0
	for _, c := range checks {
		if c.fn() {
			fmt.Printf("OK   %s\n", c.name)
			passed++
		} else {
			fmt.Printf("FAIL %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d checks passed\n", passed, len(checks))
}
