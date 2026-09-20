package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// Concurrent inserts of the same normalized key: exactly one
// succeeds, every loser gets a *ConflictError pointing at the
// winner, and the index stays consistent.
func TestConcurrentInsertsExactlyOneWins(t *testing.T) {
	s := New(NormOptions{TrimSpace: true, CaseFold: true}, nameConstraint)
	const n = 64
	var wg sync.WaitGroup
	results := make([]error, n)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = fmt.Sprintf("rec-%d", i)
	}
	// Variants that all normalize to "alice".
	variants := []string{"Alice", "ALICE", "  alice", "aLiCe\t"}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := variants[i%len(variants)]
			results[i] = s.Insert(ids[i], map[string]Value{"name": Str(v)})
		}(i)
	}
	wg.Wait()

	winner := ""
	wins := 0
	for i, err := range results {
		if err == nil {
			wins++
			winner = ids[i]
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one insert should succeed, got %d", wins)
	}
	for i, err := range results {
		if err == nil {
			continue
		}
		ce, ok := err.(*ConflictError)
		if !ok {
			t.Fatalf("loser %d got %T, want *ConflictError", i, err)
		}
		if ce.ExistingID != winner {
			t.Errorf("loser %d: conflict points to %q, want winner %q", i, ce.ExistingID, winner)
		}
		if ce.Key != "alice" {
			t.Errorf("loser %d: Key = %q, want %q", i, ce.Key, "alice")
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Errorf("SelfCheck after concurrent inserts: %v", err)
	}
}

// Concurrent batches mixing deletes and inserts keep the index
// consistent.
func TestConcurrentBatchesStayConsistent(t *testing.T) {
	c := Constraint{Name: "uniq_ab", Columns: []string{"a", "b"}, NullsEqual: true}
	s := New(NormOptions{CaseFold: true}, c)
	const n = 32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("rec-%d", i)
			props := map[string]Value{"a": Str("Key"), "b": Null()}
			_ = s.Apply(InsertOp(id, props))
			_ = s.Apply(DeleteOp(id), InsertOp(id+"-b", props))
		}(i)
	}
	wg.Wait()
	if err := s.SelfCheck(); err != nil {
		t.Errorf("SelfCheck after concurrent batches: %v", err)
	}
}
