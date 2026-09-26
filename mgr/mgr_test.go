package mgr

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/lease"
)

// TestRejectionLeavesNoTrace pins invariant 4: every rejection kind is a
// distinct sentinel and changes nothing; the manager stays usable.
func TestRejectionLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name string
		call func(m *Manager) error
		want error
	}{
		{"empty name acquire", func(m *Manager) error { _, e := m.Acquire("", "o", 0); return e }, ErrEmptyName},
		{"empty name renew", func(m *Manager) error { return m.Renew("", 1, 0) }, ErrEmptyName},
		{"never acquired", func(m *Manager) error { return m.Renew("ghost", 1, 0) }, ErrNotAcquired},
		{"stale token", func(m *Manager) error { return m.Renew("x", 9, 5) }, lease.ErrStaleToken},
		{"expired", func(m *Manager) error { return m.Renew("x", 1, 10) }, lease.ErrExpired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(10)
			if _, err := m.Acquire("x", "o", 0); err != nil {
				t.Fatal(err)
			}
			if err := c.call(m); !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
			// No trace: x is still token 1 / expiry 10 and remains usable.
			if got := m.ExpiredAll(10); len(got) != 1 || got[0] != "x" {
				t.Fatalf("state changed after refusal: ExpiredAll=%v", got)
			}
			if err := m.Renew("x", 1, 9); err != nil {
				t.Fatalf("lease unusable after refusal: %v", err)
			}
		})
	}
}

// TestExpiredAllScanCount proves heap-directed location: with m leases and
// only a constant number expired, scanned must stay constant as m grows
// across tiers. scanned is read here inside the package only; no exported
// path to it exists.
func TestExpiredAllScanCount(t *testing.T) {
	const TTL = 1_000_000
	const k = 2 // n0 (renewed, live expiry TTL+1) and n1 (TTL+1) are dead
	for _, size := range []int{100, 1000, 10000} {
		m := New(TTL)
		perm := rand.Perm(size) // random arrival order
		for _, i := range perm {
			name := fmt.Sprintf("n%d", i)
			if _, err := m.Acquire(name, "o", i); err != nil {
				t.Fatal(err)
			}
			if i%2 == 0 && i < size-2 { // one stale entry per even name
				if err := m.Renew(name, 1, i+1); err != nil {
					t.Fatal(err)
				}
			}
		}
		// At now TTL+1 the top region is: stale n0 entry (TTL, skipped),
		// live n0 and n1 entries (TTL+1, both expired); TTL+2 breaks.
		got := m.ExpiredAll(TTL + 1)
		if len(got) != k || got[0] != "n0" || got[1] != "n1" {
			t.Fatalf("size %d: ExpiredAll=%v", size, got)
		}
		// Exactly three entries touched regardless of m: 1 stale + k live.
		if m.scanned != 1+k {
			t.Fatalf("size %d: scanned %d, want %d (must not grow with m)", size, m.scanned, 1+k)
		}
	}
}

// TestConcurrentRenew: M goroutines acquire/renew distinct names while
// readers run; final expiry is correct for every name and no reader ever
// observes expiry decrease. No sleep: completion is synchronized explicitly.
func TestConcurrentRenew(t *testing.T) {
	const M, TTL = 64, 10
	m := New(TTL)
	var wg sync.WaitGroup
	for g := 0; g < M; g++ {
		name := fmt.Sprintf("c%d", g)
		if _, err := m.Acquire(name, "o", 0); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for now := 1; now <= 9; now++ {
				if err := m.Renew(name, 1, now); err != nil {
					t.Errorf("renew %s: %v", name, err)
					return
				}
			}
		}()
	}
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	for r := 0; r < 8; r++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			seen := map[string]bool{}
			for {
				select {
				case <-stop:
					return
				default:
				}
				for g := 0; g < M; g++ {
					name := fmt.Sprintf("c%d", g)
					if !m.Expired(name, 18) {
						seen[name] = true // observed expiry > 18
					} else if seen[name] {
						t.Errorf("expiry of %s observed decreasing", name)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	rwg.Wait()
	for g := 0; g < M; g++ {
		name := fmt.Sprintf("c%d", g)
		if m.Expired(name, 18) || !m.Expired(name, 19) {
			t.Fatalf("%s: final expiry wrong (want 19)", name)
		}
	}
}
