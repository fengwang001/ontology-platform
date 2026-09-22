package cascade

import (
	"testing"

	"ontology/wheel"
)

// wheels builds 3 levels with S=4: ticks 1,4,16; intervals 4,16,64.
func wheels() []*wheel.Wheel {
	return []*wheel.Wheel{
		wheel.New(4, 1, 0),
		wheel.New(4, 4, 0),
		wheel.New(4, 16, 0),
	}
}

func TestPlace(t *testing.T) {
	tests := []struct {
		exp       int64
		wantLevel int
		wantSlot  int
		wantOK    bool
	}{
		{1, 0, 1, true},
		{3, 0, 3, true},
		{4, 1, 1, true},
		{15, 1, 3, true},
		{16, 2, 1, true},
		{63, 2, 3, true},
		{64, 0, 0, false},
	}
	for _, tt := range tests {
		l, idx, ok := Place(wheels(), tt.exp)
		if l != tt.wantLevel || idx != tt.wantSlot || ok != tt.wantOK {
			t.Errorf("exp=%d: got (%d,%d,%v), want (%d,%d,%v)",
				tt.exp, l, idx, ok, tt.wantLevel, tt.wantSlot, tt.wantOK)
		}
	}
}

func TestPlaceAfterAdvance(t *testing.T) {
	ws := wheels()
	ws[0].AdvanceTo(4)
	ws[1].AdvanceTo(4)
	tests := []struct {
		exp       int64
		wantLevel int
		wantSlot  int
	}{
		{5, 0, 1},
		{7, 0, 3},
		{8, 1, 2},
		{19, 1, 0},
	}
	for _, tt := range tests {
		l, idx, ok := Place(ws, tt.exp)
		if !ok || l != tt.wantLevel || idx != tt.wantSlot {
			t.Errorf("exp=%d: got (%d,%d), want (%d,%d)", tt.exp, l, idx, tt.wantLevel, tt.wantSlot)
		}
	}
}

func TestDue(t *testing.T) {
	tests := []struct {
		now, deadline int64
		want          bool
	}{
		{0, 0, true}, {0, 1, false}, {1, 1, true}, {2, 1, true},
	}
	for _, tt := range tests {
		if got := Due(tt.now, tt.deadline); got != tt.want {
			t.Errorf("Due(%d,%d) = %v, want %v", tt.now, tt.deadline, got, tt.want)
		}
	}
}
