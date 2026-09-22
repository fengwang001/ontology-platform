package ontology

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// TestRebalanceConcurrentInserts forces the interleaving that corrupts
// the sequence when Rebalance races with Insert:
//
//  1. Rebalance reads n = len(entries) under the read lock, then
//     computes spreadKeys(n) with no lock held.
//  2. An insert lands in that window, appending an entry with an
//     old-generation key.
//  3. Rebalance takes the write lock and rekeys only the first n
//     entries; the appended tail keeps its old key.
//
// To make the corruption *observable* the inserters only ever insert at
// the front of the sequence. Front inserts always produce keys smaller
// than every existing key, so the largest key in the sequence stays
// equal to the previous rebalance's spread maximum encodeFixed(m).
// After a racy rebalance at size n >= m, entry n-1 gets the new spread
// maximum encodeFixed(n) >= encodeFixed(m) while entry n keeps the old
// maximum: keys are no longer strictly increasing (and two key
// generations are mixed), which SelfCheck reports.
//
// The check runs after every Rebalance in the same goroutine because
// the next Rebalance heals the corruption by rekeying everything.
// Entry count is capped below 35^2 so spread width stays 2 and
// encodeFixed is monotonic in n. No sleeps are used: workers insert
// back to back, so with 6 of them the unlocked window inside Rebalance
// (an O(n) key computation plus a contended write-lock acquisition) is
// hit with overwhelming probability across 500 rounds.
func TestRebalanceConcurrentInserts(t *testing.T) {
	s := NewSequence(nil, 64)
	fillSequence(t, s, 200)
	s.Rebalance() // start from one clean, uniform key generation

	const (
		workers    = 6
		rounds     = 500
		maxEntries = 1000 // stay below 35^2 so spread width stays 2
	)

	var inserts atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				if s.Len() >= maxEntries {
					runtime.Gosched() // let rebalances catch up; no sleep
					continue
				}
				// Front insert: the value is unique, the key lands
				// below every existing key. Errors (e.g. a full
				// front gap) resolve themselves after the next
				// rebalance, so retrying is enough.
				v := fmt.Sprintf("w%d-%06d", id, i)
				if _, err := s.Insert("", firstKey(s), v); err == nil {
					inserts.Add(1)
				} else {
					runtime.Gosched()
				}
			}
		}(w)
	}

	var firstErr error
	for round := 0; round < rounds; round++ {
		s.Rebalance()
		if err := s.SelfCheck(); err != nil {
			firstErr = fmt.Errorf("round %d: %w", round, err)
			break
		}
	}
	close(stop)
	wg.Wait()

	if firstErr != nil {
		t.Fatalf("SelfCheck failed while rebalancing concurrently with inserts: %v", firstErr)
	}
	if inserts.Load() == 0 {
		t.Fatal("no concurrent inserts happened; stress was vacuous")
	}
	t.Logf("survived %d rebalance rounds with %d concurrent inserts", rounds, inserts.Load())

	// A final quiescent rebalance must leave one uniform generation.
	s.Rebalance()
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck after final rebalance: %v", err)
	}
	snap := s.Snapshot()
	for i := 1; i < len(snap); i++ {
		if len(snap[i].Key) != len(snap[0].Key) {
			t.Fatalf("mixed key generations after final rebalance: %q vs %q",
				snap[0].Key, snap[i].Key)
		}
	}
}
