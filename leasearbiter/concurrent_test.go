package leasearbiter

import (
	"sync"
	"testing"
)

// TestConcurrent exercises all operations in parallel under the race
// detector. Equivalent-serializability is checked structurally by the
// differential tests on a fixed order; here we only assert safe locking.
func TestConcurrent(t *testing.T) {
	a := mustNew(t, 7, 1000, 50, 5000, 0)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for k := int64(0); k < 200; k++ {
				now := base + k
				_, _ = a.Ack(1+int(now%6), now, now)
				_, _ = a.Read(now)
				_, _ = a.Tick(now)
				_, _ = a.ChallengerVotes(now)
			}
		}(int64(g * 200))
	}
	wg.Wait()
}
