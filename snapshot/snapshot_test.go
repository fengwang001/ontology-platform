package snapshot

import (
	"testing"

	"ontology/txid"
)

func set(ids ...txid.TxID) map[txid.TxID]struct{} {
	m := map[txid.TxID]struct{}{}
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

// Half-open boundary: commit == point is NOT visible.
func TestVisibilityHalfOpen(t *testing.T) {
	r := NewRegistry(0)
	s, err := r.Open(5, set(3))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(s)
	cases := []struct {
		commit txid.TxID
		want   bool
	}{
		{1, true},
		{2, true},
		{3, false}, // active at snapshot time
		{4, true},
		{5, false}, // exactly at the point: not visible
		{6, false}, // after the point
	}
	for _, c := range cases {
		if got := s.CommittedVisible(c.commit); got != c.want {
			t.Errorf("commit %d: got %v want %v", c.commit, got, c.want)
		}
	}
}

func TestSnapshotFrozen(t *testing.T) {
	r := NewRegistry(0)
	active := set(3)
	s, _ := r.Open(5, active)
	active[9] = struct{}{} // mutating the caller map must not affect snapshot
	if s.Active(9) {
		t.Fatal("snapshot active set aliases caller map")
	}
	if r.Count() != 1 {
		t.Fatal("count")
	}
	r.Close(s)
	if r.Count() != 0 {
		t.Fatal("close failed")
	}
}

func TestSnapshotLimit(t *testing.T) {
	r := NewRegistry(1)
	s1, err := r.Open(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(s1)
	if _, err := r.Open(2, nil); err != ErrSnapshotLimit {
		t.Fatalf("got %v want ErrSnapshotLimit", err)
	}
}

func TestHorizon(t *testing.T) {
	r := NewRegistry(0)
	s1, _ := r.Open(10, set(2))
	s2, _ := r.Open(7, set(5))
	defer r.Close(s1)
	defer r.Close(s2)
	h, union := r.Horizon(set(9))
	if h != 7 {
		t.Fatalf("horizon %d want 7", h)
	}
	for _, id := range []txid.TxID{2, 5, 9} {
		if _, ok := union[id]; !ok {
			t.Errorf("union missing %d", id)
		}
	}
}
