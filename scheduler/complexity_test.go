package scheduler

import "testing"

// TestTouchedIndependentOfN pins the complexity constraint: Advance(1)
// with all timers far in the future touches a constant number of slots and
// timers, independent of how many timers are registered.
func TestTouchedIndependentOfN(t *testing.T) {
	tests := []struct {
		n       int
		wantSlo int64
		wantTim int64
	}{
		{1000, 1, 0},
		{100000, 1, 0},
	}
	var baseSlots, baseTimers int64
	for i, tt := range tests {
		s, _ := newRecorderScheduler(Config{})
		for j := 0; j < tt.n; j++ {
			if _, err := s.Add(1000000, nil); err != nil { // far future: top level
				t.Fatalf("Add: %v", err)
			}
		}
		if _, err := s.Advance(1); err != nil {
			t.Fatalf("Advance: %v", err)
		}
		t.Logf("N=%d: touchedSlots=%d touchedTimers=%d", tt.n, s.touchedSlots, s.touchedTimers)
		if s.touchedSlots != tt.wantSlo || s.touchedTimers != tt.wantTim {
			t.Fatalf("N=%d: touched (%d,%d), want (%d,%d)",
				tt.n, s.touchedSlots, s.touchedTimers, tt.wantSlo, tt.wantTim)
		}
		if i == 0 {
			baseSlots, baseTimers = s.touchedSlots, s.touchedTimers
			continue
		}
		// Growth between the two tiers must be far below 100x.
		if s.touchedSlots > baseSlots*100 || s.touchedTimers > baseTimers*100+100 {
			t.Fatalf("touched grew with N: (%d,%d) -> (%d,%d)",
				baseSlots, baseTimers, s.touchedSlots, s.touchedTimers)
		}
	}
}
