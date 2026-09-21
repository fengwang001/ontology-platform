package iproute

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentLookup hammers Lookup from many goroutines while other
// goroutines add and delete routes. Run with -race to catch data races.
func TestConcurrentLookup(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "0.0.0.0/0", "gw")
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	mustAdd(t, tb, "10.1.0.0/16", "hop-b")
	const workers = 8
	const iterations = 500
	var wg sync.WaitGroup
	errs := make(chan error, workers*2)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				ip := fmt.Sprintf("10.%d.%d.%d", id, i%256, (i*7)%256)
				next, matched, err := tb.Lookup(ip)
				if err != nil {
					errs <- fmt.Errorf("Lookup(%q): %w", ip, err)
					return
				}
				if next == "" || matched == "" {
					errs <- fmt.Errorf("Lookup(%q): empty result", ip)
					return
				}
			}
		}(w)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				cidr := fmt.Sprintf("172.16.%d.0/24", (id*iterations+i)%256)
				if err := tb.Add(cidr, "hop-w"); err == nil {
					_ = tb.Delete(cidr)
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestConcurrentLookupConsistency verifies that concurrent readers all
// observe the same longest-prefix result for a stable table.
func TestConcurrentLookupConsistency(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "0.0.0.0/0", "gw")
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	mustAdd(t, tb, "10.1.0.0/16", "hop-b")
	mustAdd(t, tb, "10.1.2.0/24", "hop-c")
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				next, matched, err := tb.Lookup("10.1.2.3")
				if err != nil || next != "hop-c" || matched != "10.1.2.0/24" {
					t.Errorf("got (%q, %q, %v), want (hop-c, 10.1.2.0/24, nil)",
						next, matched, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
