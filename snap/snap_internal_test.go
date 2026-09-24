package snap

import "testing"

// TestCompletionProbedConstant proves alignment completion is decided by the
// blocked-channel counter in O(1): after barriers block channels 0..m-2 one
// by one, the final Bar(m-1,1) triggers completion while the number of
// channels inspected by that decision stays below a constant independent of
// m. The counter is unexported and is read only inside package snap.
func TestCompletionProbedConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run("m", func(t *testing.T) {
			e, err := New(m)
			if err != nil {
				t.Fatalf("New(%d): %v", m, err)
			}
			bars := make([]Event, m-1)
			for ch := range bars {
				bars[ch] = Bar(ch, 1)
			}
			if err := e.Apply(bars); err != nil {
				t.Fatalf("blocking barriers: %v", err)
			}
			if e.nblock != m-1 {
				t.Fatalf("nblock=%d want %d", e.nblock, m-1)
			}
			if err := e.Apply([]Event{Bar(m-1, 1)}); err != nil {
				t.Fatalf("completing barrier: %v", err)
			}
			if e.probed > 1 {
				t.Fatalf("m=%d completion inspected %d channels, want <= 1", m, e.probed)
			}
			if v, ok := e.Snapshot(1); !ok || v != 0 {
				t.Fatalf("m=%d snap[1]=%d,%v want 0,true", m, v, ok)
			}
		})
	}
}

// TestCompletionProbedResetsEachDecision checks every barrier event records a
// fresh constant-size decision, including non-completing ones.
func TestCompletionProbedResetsEachDecision(t *testing.T) {
	e, _ := New(3)
	for _, ch := range []int{0, 1} {
		if err := e.Apply([]Event{Bar(ch, 1)}); err != nil {
			t.Fatal(err)
		}
		if e.probed != 0 {
			t.Fatalf("non-completing decision probed=%d want 0", e.probed)
		}
	}
	if err := e.Apply([]Event{Bar(2, 1)}); err != nil || e.probed > 1 {
		t.Fatalf("completion probed=%d err=%v", e.probed, err)
	}
}
