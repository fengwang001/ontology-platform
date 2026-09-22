package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestRebalanceConcurrentInserts forces the exact interleaving that used
// to corrupt the sequence: Rebalance reads the length under the read
// lock, drops the lock to compute fresh spread keys, and an Insert
// commits in that unlocked window. The stale positional key assignment
// then leaves pre-rebalance keys past the new maximum key, breaking
// strict monotonicity (and mixing key generations).
//
// How the interleaving is forced without sleeps or test hooks:
//
//   - The vulnerable window is the lock-free spreadKeys(n) computation.
//     A large n (20k entries) stretches it to ~1ms, while a front insert
//     into the same sequence costs only ~100us.
//   - A dedicated goroutine hammers front inserts for the whole round,
//     so several inserts land inside every window with overwhelming
//     probability; 25 rounds make a miss effectively impossible.
//   - Before each round the sequence holds one pure spread generation
//     (width-3 keys encodeFixed(1..n)). Any insert committed in the
//     window shifts entries up, so after the stale swap the entry at
//     position n still carries an old spread key <= encodeFixed(n),
//     i.e. <= the new key at position n-1. SelfCheck on the quiesced
//     state therefore deterministically reports non-increasing keys.
func TestRebalanceConcurrentInserts(t *testing.T) {
	const (
		maxLen = 64
		seed   = 20000
		rounds = 25
	)
	s := NewSequence(nil, maxLen)

	// Seed with back inserts (O(1) appends), rebalancing whenever the
	// key length limit is hit, then finish with one clean rebalance so
	// every key belongs to a single short spread generation.
	last := ""
	for i := 0; i < seed; i++ {
		key, err := s.Insert(last, "", fmt.Sprintf("seed-%06d", i))
		if errors.Is(err, ErrNeedsRebalance) {
			s.Rebalance()
			snap := s.Snapshot()
			last = snap[len(snap)-1].Key
			i--
			continue
		}
		if err != nil {
			t.Fatalf("seed insert %d: %v", i, err)
		}
		last = key
	}
	s.Rebalance()
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for round := 0; round < rounds; round++ {
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Snapshot()
				if len(snap) == 0 {
					return
				}
				v := fmt.Sprintf("r%03d-%04d", round, i)
				_, err := s.Insert("", snap[0].Key, v)
				if errors.Is(err, ErrNeedsRebalance) {
					return // front gap exhausted; next round rebalances
				}
				if err != nil {
					t.Errorf("round %d insert %s: %v", round, v, err)
					return
				}
			}
		}()
		s.Rebalance()
		close(stop)
		wg.Wait()
		// Quiesced state: corruption from a stale rebalance persists
		// until the next rebalance, so it is always visible here.
		if err := s.SelfCheck(); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	if length, _ := s.LongestKey(); length > maxLen {
		t.Fatalf("longest key %d exceeds max %d", length, maxLen)
	}
}
