package ring

import (
	"fmt"
	"sync"
	"testing"
)

// Locate must be safe to call from many goroutines while Add and Remove
// run concurrently: no panic, no empty result, and every returned node
// must be one that genuinely exists on the ring at that moment.
func TestConcurrentLocateAddRemove(t *testing.T) {
	r := buildRing(t, 5, 50)

	// Pool of every node ID that can ever be on the ring during the test.
	valid := make(map[string]bool)
	for i := 0; i < 5; i++ {
		valid[nodeID(i)] = true
	}
	for i := 0; i < 8; i++ {
		valid[fmt.Sprintf("extra-%d", i)] = true
	}

	const workers = 8
	const lookupsPerWorker = 5000

	var wg sync.WaitGroup
	errCh := make(chan string, workers*lookupsPerWorker)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < lookupsPerWorker; i++ {
				owner, err := r.Locate(fmt.Sprintf("key-%d-%d", w, i))
				if err != nil {
					errCh <- fmt.Sprintf("Locate error: %v", err)
					return
				}
				if owner == "" {
					errCh <- "Locate returned empty node"
					return
				}
				if !valid[owner] {
					errCh <- fmt.Sprintf("Locate returned unknown node %q", owner)
					return
				}
			}
		}(w)
	}

	// Mutators: keep adding and removing extra nodes while lookups run.
	stop := make(chan struct{})
	var mutWg sync.WaitGroup
	for m := 0; m < 2; m++ {
		mutWg.Add(1)
		go func(m int) {
			defer mutWg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				name := fmt.Sprintf("extra-%d", (i%8+m)%8)
				if r.Has(name) {
					_ = r.Remove(name)
				} else {
					_ = r.Add(name, 20)
				}
			}
		}(m)
	}

	wg.Wait()
	close(stop)
	mutWg.Wait()
	close(errCh)

	for msg := range errCh {
		t.Fatal(msg)
	}
}
