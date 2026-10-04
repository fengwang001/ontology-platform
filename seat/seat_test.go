package seat

import "testing"

func TestTable(t *testing.T) {
	tb := NewTable(2, 3)
	if tb.Teams() != 2 || tb.SeatsPerTeam() != 3 {
		t.Fatalf("size = %d,%d", tb.Teams(), tb.SeatsPerTeam())
	}
	cells := tb.Snapshot()
	if len(cells) != 6 {
		t.Fatalf("cells = %d", len(cells))
	}
	for i, c := range cells {
		wantTeam := i / 3
		wantIdx := i % 3
		if c.Team != wantTeam || c.Index != wantIdx || c.Status != Empty {
			t.Fatalf("cell %d = %+v", i, c)
		}
	}
	tests := []struct {
		name string
		run  func() bool
		want bool
	}{
		{"place ok", func() bool { return tb.Place(0, 1, "a", Reserved) }, true},
		{"place busy", func() bool { return tb.Place(0, 1, "b", Seated) }, false},
		{"place bad team", func() bool { return tb.Place(9, 0, "c", Seated) }, false},
		{"set status", func() bool { return tb.SetStatus(0, 1, Seated) }, true},
		{"set empty cell", func() bool { return tb.SetStatus(1, 1, Seated) }, false},
		{"release", func() bool { return tb.Release(0, 1) }, true},
		{"release empty", func() bool { return tb.Release(0, 1) }, false},
	}
	for _, tc := range tests {
		if got := tc.run(); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
	if got := tb.FreeIndices(0); len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("free = %v", got)
	}
	if got := tb.FreeIndices(9); got != nil {
		t.Fatalf("bad team free = %v", got)
	}
}
