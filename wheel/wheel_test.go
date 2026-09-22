package wheel

import "testing"

func TestSlotIndex(t *testing.T) {
	tests := []struct {
		slots int
		tick  int64
		exp   int64
		want  int
	}{
		{4, 1, 0, 0}, {4, 1, 3, 3}, {4, 1, 4, 0}, {4, 1, 7, 3},
		{4, 4, 4, 1}, {4, 4, 15, 3}, {4, 4, 16, 0},
		{4, 16, 16, 1}, {4, 16, 63, 3},
	}
	for _, tt := range tests {
		w := New(tt.slots, tt.tick, 0)
		if got := w.SlotIndex(tt.exp); got != tt.want {
			t.Errorf("slots=%d tick=%d exp=%d: SlotIndex = %d, want %d",
				tt.slots, tt.tick, tt.exp, got, tt.want)
		}
	}
}

func TestAdvanceTo(t *testing.T) {
	tests := []struct {
		name        string
		now         int64
		wantFlush   []uint64
		wantWrapped bool
		wantCT      int64
		wantCursor  int
	}{
		{"before boundary", 3, nil, false, 0, 0},
		{"reach boundary", 4, nil, false, 4, 1},
		{"flush inserted", 8, []uint64{1}, false, 8, 2},
		{"full revolution wraps", 16, nil, true, 16, 0},
	}
	w := New(4, 4, 0)
	w.InsertAt(w.SlotIndex(8), 1, "a")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flushed, wrapped := w.AdvanceTo(tt.now)
			if wrapped != tt.wantWrapped {
				t.Fatalf("wrapped = %v, want %v", wrapped, tt.wantWrapped)
			}
			if w.CurrentTime != tt.wantCT || w.Cursor() != tt.wantCursor {
				t.Fatalf("currentTime=%d cursor=%d, want %d/%d",
					w.CurrentTime, w.Cursor(), tt.wantCT, tt.wantCursor)
			}
			if len(flushed) != len(tt.wantFlush) {
				t.Fatalf("flushed %d entries, want %d", len(flushed), len(tt.wantFlush))
			}
			for i, e := range flushed {
				if e.Handle != tt.wantFlush[i] {
					t.Fatalf("flushed[%d] = %d, want %d", i, e.Handle, tt.wantFlush[i])
				}
			}
		})
	}
}

func TestAlignment(t *testing.T) {
	tests := []struct{ tick, start, want int64 }{
		{1, 0, 0}, {1, 5, 5}, {4, 5, 4}, {16, 100, 96},
	}
	for _, tt := range tests {
		w := New(4, tt.tick, tt.start)
		if w.CurrentTime != tt.want {
			t.Errorf("tick=%d start=%d: currentTime = %d, want %d",
				tt.tick, tt.start, w.CurrentTime, tt.want)
		}
	}
}
