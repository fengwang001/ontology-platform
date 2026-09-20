package ontology

import (
	"errors"
	"fmt"
	"testing"
)

// TestInsertRebalanceInsertCycle exercises the full lifecycle: insert at
// the front of the sequence until the key length limit forces a
// rebalance, rebalance, then keep inserting.
func TestInsertRebalanceInsertCycle(t *testing.T) {
	const maxLen = 8
	s := NewSequence(nil, maxLen)
	inserted := 0
	for {
		_, err := s.Insert("", firstKey(s), fmt.Sprintf("v%03d", inserted))
		if errors.Is(err, ErrNeedsRebalance) {
			break
		}
		if err != nil {
			t.Fatalf("insert %d: %v", inserted, err)
		}
		inserted++
		if inserted > 1000 {
			t.Fatal("never hit the length limit")
		}
	}
	if inserted < 20 {
		t.Fatalf("only %d inserts before rebalance, want dozens", inserted)
	}
	t.Logf("inserted %d keys before ErrNeedsRebalance", inserted)
	length, remaining := s.LongestKey()
	if remaining != 0 {
		t.Fatalf("longest key %d, remaining %d; limit should be exhausted", length, remaining)
	}
	wantOrder := values(s.Snapshot())
	s.Rebalance()
	if got := values(s.Snapshot()); fmt.Sprint(got) != fmt.Sprint(wantOrder) {
		t.Fatalf("rebalance changed relative order:\n got %v\nwant %v", got, wantOrder)
	}
	if length, _ := s.LongestKey(); length >= maxLen {
		t.Fatalf("longest key still %d after rebalance", length)
	}
	// Inserting at the same spot works again right after rebalancing.
	for i := 0; i < 10; i++ {
		if _, err := s.Insert("", firstKey(s), fmt.Sprintf("post-%02d", i)); err != nil {
			t.Fatalf("post-rebalance insert %d: %v", i, err)
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck after full cycle: %v", err)
	}
}

// firstKey returns the key of the first entry, or "" when empty.
func firstKey(s *Sequence) string {
	snap := s.Snapshot()
	if len(snap) == 0 {
		return ""
	}
	return snap[0].Key
}
