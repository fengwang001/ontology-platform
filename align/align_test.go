package align

import "testing"

// probeBound is the m-independent ceiling on buffered events examined while
// locating the minimum TS. The heap root is the minimum: each drain decision
// examines one candidate, regardless of buffer size m. A linear scan would
// examine m candidates and fail this at m=100 already.
const probeBound = 2

// TestHeapComparisonBound buffers m distinct-TS events all above the current
// W (B unseen -> W=-inf), then feeds the first B event: it advances W to 1
// and exactly one event (B 1, the heap minimum) must emit, while the m
// buffered A events remain. Candidate examinations must not grow with m.
func TestHeapComparisonBound(t *testing.T) {
	sizes := []int{100, 316, 1000, 3162, 10000}
	first := -1
	for _, m := range sizes {
		al := New()
		for i := 0; i < m; i++ { // distinct TS, all buffered: B unseen, W=-inf
			if out, late := al.Feed(Event{Stream: 'A', TS: int64(2 + i)}); late || len(out) != 0 {
				t.Fatalf("m=%d: setup unexpected out=%v late=%v", m, out, late)
			}
		}
		out, _ := al.Feed(Event{Stream: 'B', TS: 1}) // W=1: only B 1 releasable
		if len(out) != 1 || out[0] != (Event{Stream: 'B', TS: 1}) {
			t.Fatalf("m=%d: want exactly [B 1], got %v", m, out)
		}
		if al.cmpProbe > probeBound {
			t.Fatalf("m=%d: located min after examining %d buffered events, want <= %d", m, al.cmpProbe, probeBound)
		}
		if first < 0 {
			first = al.cmpProbe
		} else if al.cmpProbe != first {
			t.Fatalf("examinations grew with m: m=100 gave %d, m=%d gave %d", first, m, al.cmpProbe)
		}
		if got := al.buf.Len(); got != m {
			t.Fatalf("m=%d: %d A events must stay buffered, got %d", m, m, got)
		}
	}
}
