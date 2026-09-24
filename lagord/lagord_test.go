package lagord

import "testing"

func ceilLog2(n int) int {
	l := 0
	for 1<<l < n {
		l++
	}
	return l
}

// TestLocateComparisonsSublinear pins binary-search locating: the number of
// rows compared to find the position must not grow linearly with m.
func TestLocateComparisonsSublinear(t *testing.T) {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		s := &Seq{}
		for i := 0; i < m; i++ {
			s.Insert(Row{ID: int64(2 * i), Sort: int64(2 * i)})
		}
		mid := Row{ID: 100000001, Sort: int64(m - 1)} // lands in the middle
		s.Insert(mid)
		bound := 2*ceilLog2(m+1) + 4
		if s.cmp > bound {
			t.Fatalf("m=%d insert: cmp=%d > bound %d", m, s.cmp, bound)
		}
		i, ok := s.Locate(mid.Sort, mid.ID) // locate again for the delete
		if !ok {
			t.Fatalf("m=%d: inserted row not found", m)
		}
		if s.cmp > bound {
			t.Fatalf("m=%d delete-locate: cmp=%d > bound %d", m, s.cmp, bound)
		}
		if got := s.RemoveAt(i); got != mid {
			t.Fatalf("m=%d: removed %+v, want %+v", m, got, mid)
		}
	}
}

// TestOrderedRegardlessOfInsertOrder checks (Sort, ID) ordering with ties.
func TestOrderedRegardlessOfInsertOrder(t *testing.T) {
	s := &Seq{}
	for _, r := range []Row{{5, 20, 6}, {3, 20, 7}, {1, 10, 5}, {2, 30, 8}, {6, 5, 5}} {
		s.Insert(r)
	}
	want := []int64{6, 1, 3, 5, 2}
	if s.Len() != len(want) {
		t.Fatalf("len=%d", s.Len())
	}
	for i, id := range want {
		if got := s.At(i).ID; got != id {
			t.Fatalf("At(%d).ID=%d, want %d", i, got, id)
		}
	}
}
