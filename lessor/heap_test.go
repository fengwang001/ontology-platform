package lessor

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// checkHeapInvariants verifies internal heap/map agreement:
//   - heap size equals lease count (no stale or missing entries),
//   - every lease's idx points at its heap slot,
//   - the array obeys min-heap order by (x, id).
func checkHeapInvariants(t *testing.T, l *Lessor) {
	t.Helper()
	if len(l.heap) != len(l.leases) {
		t.Fatalf("heap size %d != lease count %d", len(l.heap), len(l.leases))
	}
	for pos, ls := range l.heap {
		if ls.idx != pos {
			t.Fatalf("lease %d idx = %d, heap position = %d", ls.id, ls.idx, pos)
		}
		if l.leases[ls.id] != ls {
			t.Fatalf("lease %d heap entry not present in map", ls.id)
		}
		if pos > 0 {
			parent := (pos - 1) / 2
			if l.heapLess(ls, l.heap[parent]) {
				t.Fatalf("heap order violated at %d (id %d x %d) under parent %d (id %d x %d)",
					pos, ls.id, ls.x, parent, l.heap[parent].id, l.heap[parent].x)
			}
		}
	}
	// Every key maps to a live lease that in turn contains the key.
	for key, owner := range l.keyOwner {
		ls, ok := l.leases[owner]
		if !ok {
			t.Fatalf("key %q points at revoked lease %d", key, owner)
		}
		found := false
		for _, k := range ls.keys {
			if k == key {
				found = true
			}
		}
		if !found {
			t.Fatalf("key %q maps to lease %d which does not hold it", key, owner)
		}
	}
}

// TestTickPeekBudget proves a Tick inspects at most revocations+1 heap tops,
// and that the count is identical at the 100 and 10000 lease scales when most
// leases are unexpired.
func TestTickPeekBudget(t *testing.T) {
	for _, n := range []int{100, 10_000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			l, err := New(Config{MinTTL: 5, MaxTTL: 1_000_000, E: 0, R: 5, Kmax: 10})
			if err != nil {
				t.Fatal(err)
			}
			mustPromote(t, l, 0)
			// Three leases expire by time 50; the rest live far in the future.
			mustGrant(t, l, 1, 10, 0) // x = 10
			mustGrant(t, l, 2, 20, 0) // x = 20
			mustGrant(t, l, 3, 50, 0) // x = 50
			for id := int64(4); int(id) <= n; id++ {
				mustGrant(t, l, id, 100_000, 0)
			}
			checkHeapInvariants(t, l)

			before := l.TickPeeks()
			got, err := l.Tick(50)
			if err != nil {
				t.Fatal(err)
			}
			peeks := l.TickPeeks() - before
			if len(got) != 3 {
				t.Fatalf("revoked %d, want 3", len(got))
			}
			if peeks > int64(len(got))+1 {
				t.Fatalf("peeks %d > revocations %d + 1", peeks, len(got))
			}
			// Exact expectation: one peek per revoked lease plus the single
			// non-expired top that stops the scan.
			if peeks != 4 {
				t.Fatalf("peeks = %d, want 4 at scale %d", peeks, n)
			}
			checkHeapInvariants(t, l)
		})
	}

	// A Tick that revokes nothing because the top is live peeks exactly once.
	l, _ := New(Config{MinTTL: 5, MaxTTL: 1_000_000, E: 0, R: 5, Kmax: 10})
	mustPromote(t, l, 0)
	for id := int64(1); id <= 100; id++ {
		mustGrant(t, l, id, 100_000, 0)
	}
	before := l.TickPeeks()
	got, err := l.Tick(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || l.TickPeeks()-before != 1 {
		t.Fatalf("no-op tick: revs=%d peeks=%d", len(got), l.TickPeeks()-before)
	}
	checkHeapInvariants(t, l)
}

// TestHeapConsistencyAfterMutations exercises Renew (up/down sift), Revoke
// (arbitrary removal) and Promote (full rebuild), checking invariants each
// step and comparing revocation order at the end.
func TestHeapConsistencyAfterMutations(t *testing.T) {
	l, _ := New(Config{MinTTL: 1, MaxTTL: 10_000, E: 2, R: 100, Kmax: 10})
	mustPromote(t, l, 0)

	for id := int64(1); id <= 50; id++ {
		mustGrant(t, l, id, 100+id, 0)
	}
	checkHeapInvariants(t, l)

	// Renew pushes some expiries out (sift down) and one closer (sift up).
	var now int64
	for id := int64(1); id <= 50; id++ {
		if id%3 == 0 {
			now++
			if _, err := l.Renew(id, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := l.Renew(49, now+1); err != nil {
		t.Fatal(err)
	}
	now++
	checkHeapInvariants(t, l)

	// Arbitrary removals, including the heap top and interior nodes.
	for _, id := range []int64{1, 25, 50, 7, 33} {
		now++
		if _, err := l.Revoke(id, now); err != nil {
			t.Fatal(err)
		}
		checkHeapInvariants(t, l)
	}

	// Failover rebuilds the heap from sv/g.
	now++
	if err := l.Checkpoint(now); err != nil {
		t.Fatal(err)
	}
	now++
	if err := l.Demote(now); err != nil {
		t.Fatal(err)
	}
	now++
	if err := l.Promote(now); err != nil {
		t.Fatal(err)
	}
	checkHeapInvariants(t, l)

	got, err := l.Tick(1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].ID == got[i].ID {
			t.Fatalf("duplicate revocation id %d", got[i].ID)
		}
	}
	if len(got) != 45 {
		t.Fatalf("revoked %d leases, want 45", len(got))
	}
	checkHeapInvariants(t, l)
}

// TestConcurrentAccess hammers the lessor from many goroutines under -race;
// correctness under concurrency means no data race plus maintained ownership
// invariants once the dust settles.
func TestConcurrentAccess(t *testing.T) {
	l, err := New(Config{MinTTL: 1, MaxTTL: 100_000, E: 1, R: 4, Kmax: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Promote(0); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var clock atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				now := clock.Add(10)
				id := int64(1 + (g*400+i)%40)
				switch i % 7 {
				case 0:
					_ = l.Grant(id, 50, now)
				case 1:
					_, _ = l.Renew(id, now)
				case 2:
					_ = l.Attach(fmt.Sprintf("k%d", id%20), id, now)
				case 3:
					_, _ = l.Tick(now)
				case 4:
					_, _ = l.Revoke(id, now)
				case 5:
					_, _ = l.TTL(id, now)
				case 6:
					_ = l.Checkpoint(now)
				}
			}
		}(g)
	}
	wg.Wait()

	l.mu.Lock()
	defer l.mu.Unlock()
	checkHeapInvariants(t, l)
	if len(l.heap) != len(l.leases) {
		t.Fatalf("heap/lease desync after concurrency")
	}
}
