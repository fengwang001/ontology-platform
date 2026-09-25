package col

import "testing"

// Get must inspect a small constant number of slots regardless of how many
// rows the store holds (direct slotOf indexing, never a scan).
func TestGetChecksIndependentOfM(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		s := &Store{}
		for i := 0; i < m; i++ {
			s.Insert(i, int64(i), int64(-i), int64(2*i))
		}
		if _, _, _, err := s.Get(m - 1); err != nil {
			t.Fatalf("m=%d: Get: %v", m, err)
		}
		if got := s.lastGetChecks; got > 2 {
			t.Fatalf("m=%d: Get inspected %d slots, grows with m", m, got)
		}
		if _, _, _, err := s.Get(m + 9); err != ErrNotFound {
			t.Fatalf("m=%d: Get(unknown) err=%v", m, err)
		}
		if got := s.lastGetChecks; got > 2 {
			t.Fatalf("m=%d: Get(unknown) inspected %d slots", m, got)
		}
	}
}

// Compact must rebuild slotOf so every live rowID keeps its values and
// every dead rowID stays unmapped.
func TestCompactRebuildsSlots(t *testing.T) {
	cases := []struct {
		name string
		del  []int
	}{
		{"none", nil},
		{"head", []int{0}},
		{"middle", []int{1, 3}},
		{"all-but-last", []int{0, 1, 2, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Store{}
			for i := 0; i < 5; i++ {
				s.Insert(i, int64(10*i), int64(10*i+1), int64(10*i+2))
			}
			dead := map[int]bool{}
			for _, id := range tc.del {
				if err := s.Delete(id); err != nil {
					t.Fatal(err)
				}
				dead[id] = true
			}
			s.Compact()
			for id := 0; id < 5; id++ {
				a, b, c, err := s.Get(id)
				if dead[id] {
					if err != ErrDeleted || s.slotOf[id] != -1 {
						t.Fatalf("id %d: err=%v slot=%d", id, err, s.slotOf[id])
					}
					continue
				}
				if err != nil || a != int64(10*id) || b != int64(10*id+1) || c != int64(10*id+2) {
					t.Fatalf("id %d: got (%d,%d,%d,%v)", id, a, b, c, err)
				}
			}
		})
	}
}

// Rejected operations must fail with the right sentinel and change nothing.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s := &Store{}
	s.Insert(0, 1, 2, 3)
	s.Delete(0)
	s.Insert(1, 4, 5, 6)
	beforeA, beforeAlive, beforeSlot := s.a, s.alive, s.slotOf
	if err := s.Update(9, 0, 1); err != ErrNotFound {
		t.Fatalf("Update(unknown): %v", err)
	}
	if err := s.Update(0, 1, 1); err != ErrDeleted {
		t.Fatalf("Update(deleted): %v", err)
	}
	if err := s.Update(1, 7, 1); err != ErrBadCol {
		t.Fatalf("Update(badcol): %v", err)
	}
	if err := s.Delete(0); err != ErrDeleted {
		t.Fatalf("Delete(deleted): %v", err)
	}
	if _, _, _, err := s.Get(0); err != ErrDeleted {
		t.Fatalf("Get(deleted): %v", err)
	}
	for i := range s.a {
		if s.a[i] != beforeA[i] || s.alive[i] != beforeAlive[i] {
			t.Fatalf("slot %d mutated by rejected op", i)
		}
	}
	for i := range s.slotOf {
		if s.slotOf[i] != beforeSlot[i] {
			t.Fatalf("slotOf[%d] mutated by rejected op", i)
		}
	}
}
