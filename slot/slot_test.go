package slot

import "testing"

func TestSlotOps(t *testing.T) {
	s := New()
	a, b, c := 1, 2, 3
	s.Insert(Entry{H: b, Seq: 2, Deadline: 10})
	s.Insert(Entry{H: a, Seq: 1, Deadline: 9})
	s.Insert(Entry{H: c, Seq: 3, Deadline: 11})
	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3", s.Len())
	}
	if !s.Remove(a) || s.Remove(a) {
		t.Fatal("Remove should succeed once and be idempotent-false afterwards")
	}
	got := s.TakeAll()
	if s.Len() != 0 || len(got) != 2 {
		t.Fatalf("TakeAll: len=%d remaining=%d", len(got), s.Len())
	}
	if got[0].Seq != 2 || got[1].Seq != 3 {
		t.Fatalf("TakeAll not sorted by Seq: %v", got)
	}
}

func TestTakeAllOrder(t *testing.T) {
	cases := [][]uint64{{1, 2, 3}, {3, 2, 1}, {2, 1, 3}, {5, 4, 3, 2, 1}}
	for _, seqs := range cases {
		s := New()
		for i, q := range seqs {
			s.Insert(Entry{H: i, Seq: q})
		}
		got := s.TakeAll()
		for i := 1; i < len(got); i++ {
			if got[i-1].Seq > got[i].Seq {
				t.Fatalf("input %v: not sorted: %v", seqs, got)
			}
		}
	}
}
