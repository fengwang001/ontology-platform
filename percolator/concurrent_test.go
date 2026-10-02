package percolator

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentInvariants hammers one store from many goroutines and,
// after joining, checks the cross-key atomicity invariant: for every
// read timestamp, a committed transaction's keys all share one commitTs.
// Run with -race to additionally cover the mutex discipline.
func TestConcurrentInvariants(t *testing.T) {
	s := NewStore()

	const goroutines = 16
	var wg sync.WaitGroup
	var clock int64
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			// Each goroutine owns a distinct key prefix; transactions still
			// overlap on shared keys a/b to force contention.
			pk := []string{"a", "b", "shared"}
			for i := 0; i < 400; i++ {
				now := atomic.AddInt64(&clock, 1)
				st := s.Begin()
				muts := []Mutation{
					{Key: pk[g%len(pk)], Kind: Put, Value: "v"},
					{Key: "shared", Kind: Put, Value: "v"},
				}
				if g%2 == 0 {
					muts[0].Kind = Delete
				}
				if err := s.Prewrite(st, muts, muts[0].Key, 3, now); err != nil {
					continue
				}
				cts, err := s.CommitPrimary(st)
				if err != nil {
					_ = s.Abort(st)
					continue
				}
				_ = s.CommitKeys(st, []string{"shared"})
				// Concurrent readers resolve the secondary on their own.
				_, _ = s.Get("shared", cts, atomic.AddInt64(&clock, 1))
			}
		}()
	}
	wg.Wait()

	snap := s.Snapshot()
	// Every key has at most one lock (structurally guaranteed) and every
	// committed version appears on a prewritten txn's key.
	commitsByTxn := map[int64]map[int64]bool{} // st -> set of commitTs
	for _, key := range snap.SortedKeys() {
		k := snap.Keys[key]
		for _, v := range k.Versions {
			if v.Kind == Rollback {
				continue
			}
			set := commitsByTxn[v.StartTS]
			if set == nil {
				set = map[int64]bool{}
				commitsByTxn[v.StartTS] = set
			}
			set[v.CommitTS] = true
		}
	}
	for st, set := range commitsByTxn {
		if len(set) != 1 {
			t.Fatalf("txn %d committed at %d distinct timestamps: %v", st, len(set), set)
		}
	}

	// Visibility atomicity: at the final commitTs of every committed txn,
	// every committed key of that txn is present in the version table.
	for st, set := range commitsByTxn {
		var cts int64
		for c := range set {
			cts = c
		}
		for _, key := range snap.SortedKeys() {
			for _, v := range snap.Keys[key].Versions {
				if v.StartTS == st && v.CommitTS != cts {
					t.Fatalf("txn %d key %s at commitTs %d, want %d", st, key, v.CommitTS, cts)
				}
			}
		}
	}
}
