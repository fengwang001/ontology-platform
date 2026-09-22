package slot

import "testing"

func TestSlotOps(t *testing.T) {
	tests := []struct {
		name      string
		inserts   []uint64
		removes   []uint64
		wantLen   int
		wantOrder []uint64 // handles expected from TakeAll, in order
	}{
		{"empty", nil, nil, 0, nil},
		{"insert only", []uint64{1, 2, 3}, nil, 3, []uint64{1, 2, 3}},
		{"remove head", []uint64{1, 2, 3}, []uint64{1}, 2, []uint64{3, 2}},
		{"remove tail", []uint64{1, 2, 3}, []uint64{3}, 2, []uint64{1, 2}},
		{"remove missing", []uint64{1, 2}, []uint64{9}, 2, []uint64{1, 2}},
		{"remove all", []uint64{1, 2}, []uint64{1, 2}, 0, nil},
		{"duplicate ignored", []uint64{1, 1, 2}, nil, 2, []uint64{1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s Slot
			for _, h := range tt.inserts {
				s.Insert(h, int(h))
			}
			for _, h := range tt.removes {
				s.Remove(h)
			}
			if s.Len() != tt.wantLen {
				t.Fatalf("Len = %d, want %d", s.Len(), tt.wantLen)
			}
			got := s.TakeAll()
			if len(got) != len(tt.wantOrder) {
				t.Fatalf("TakeAll len = %d, want %d", len(got), len(tt.wantOrder))
			}
			for i, e := range got {
				if e.Handle != tt.wantOrder[i] {
					t.Fatalf("TakeAll[%d] = %d, want %d", i, e.Handle, tt.wantOrder[i])
				}
			}
			if s.Len() != 0 {
				t.Fatalf("slot not empty after TakeAll")
			}
		})
	}
}

func TestSlotRemoveReturnsValue(t *testing.T) {
	var s Slot
	s.Insert(7, "x")
	v, ok := s.Remove(7)
	if !ok || v != "x" {
		t.Fatalf("Remove = %v,%v", v, ok)
	}
	if _, ok := s.Remove(7); ok {
		t.Fatalf("second Remove should miss")
	}
}
