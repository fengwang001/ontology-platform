package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentInserts: many goroutines insert records whose keys are
// equal after normalization; exactly one must succeed, every loser must
// get a *ConflictError pointing at the winner, and the index must stay
// consistent (checked via CheckIndex). Run with -race.
func TestConcurrentInserts(t *testing.T) {
	s := NewStore(
		Normalize{TrimSpace: true, CaseFold: true},
		Constraint{Name: "uq_name", Cols: []string{"name"}},
	)
	const n = 64
	variants := []string{"Alice", "ALICE", " alice ", "aLiCe"}
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pk := fmt.Sprintf("pk-%03d", i)
			errs[i] = s.Insert(pk, rec("name", variants[i%len(variants)]))
		}(i)
	}
	wg.Wait()
	var winner string
	wins := 0
	for i, err := range errs {
		if err == nil {
			wins++
			winner = fmt.Sprintf("pk-%03d", i)
			continue
		}
		ce, ok := err.(*ConflictError)
		if !ok {
			t.Fatalf("loser got %v (%T), want *ConflictError", err, err)
		}
		if ce.Constraint != "uq_name" {
			t.Fatalf("loser constraint=%q, want uq_name", ce.Constraint)
		}
		if ce.ExistingPK == "" {
			t.Fatal("loser error must name the winning record")
		}
	}
	if wins != 1 {
		t.Fatalf("winners=%d, want exactly 1", wins)
	}
	for _, err := range errs {
		if ce, ok := err.(*ConflictError); ok && ce.ExistingPK != winner {
			t.Fatalf("loser points at %q, want winner %q", ce.ExistingPK, winner)
		}
	}
	if s.Len() != 1 {
		t.Fatalf("Len=%d, want 1", s.Len())
	}
	mustNoErr(t, s.CheckIndex())
}

// TestConcurrentBatches: concurrent batches racing on the same
// normalized key; exactly one batch commits.
func TestConcurrentBatches(t *testing.T) {
	s := NewStore(
		Normalize{CaseFold: true},
		Constraint{Name: "uq_name", Cols: []string{"name"}},
	)
	const n = 16
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.ApplyBatch([]Op{
				InsertOp(fmt.Sprintf("b-%d", i), rec("name", "Alice")),
			})
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("committed batches=%d, want 1", wins)
	}
	mustNoErr(t, s.CheckIndex())
}
