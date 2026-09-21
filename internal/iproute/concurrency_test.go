package iproute

import (
	"sync"
	"testing"
)

// TestConcurrentLookup hammers Lookup from many goroutines while a
// writer adds and deletes routes. Run with -race.
func TestConcurrentLookup(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "0.0.0.0/0", "gw")
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	mustAdd(t, tb, "10.1.0.0/16", "hop-b")

	const readers = 8
	const iterations = 2000

	var wg sync.WaitGroup
	wg.Add(readers + 1)

	for r := 0; r < readers; r++ {
		go func(r int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				ip := "10.1.2.3"
				if i%2 == 1 {
					ip = "192.0.2.1"
				}
				next, matched, err := tb.Lookup(ip)
				if err != nil {
					t.Errorf("Lookup(%q): %v", ip, err)
					return
				}
				if next == "" || matched == "" {
					t.Errorf("Lookup(%q) returned empty result", ip)
					return
				}
			}
		}(r)
	}

	// Concurrent writer: add and delete a route repeatedly.
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			if err := tb.Add("172.16.0.0/12", "hop-c"); err != nil {
				t.Errorf("Add: %v", err)
				return
			}
			if err := tb.Delete("172.16.0.0/12"); err != nil {
				t.Errorf("Delete: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}
