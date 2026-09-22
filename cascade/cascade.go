// Package cascade provides the level math of the hierarchical wheel:
// where an expiration lands (level and slot) and when an element is due.
// Demotion is lazy: a higher-level slot is flushed only when the cursor
// arrives at it (see wheel.Wheel.AdvanceTo); the flushed elements are then
// re-placed through Place.
package cascade

import "ontology/wheel"

// Place returns the level and slot index where a timer expiring at exp
// belongs: the lowest level whose remaining range covers exp. ok is false
// when exp exceeds the top level's range.
//
// Invariant: the returned slot is strictly ahead of the level's cursor and
// no two "epochs" share a slot, because lower levels rejected exp, i.e.
// exp >= currentTime_{L-1} + interval_{L-1} >= currentTime_L + tick_L.
func Place(ws []*wheel.Wheel, exp int64) (level, slotIdx int, ok bool) {
	for l, w := range ws {
		if exp < w.CurrentTime+w.Interval() {
			return l, w.SlotIndex(exp), true
		}
	}
	return 0, 0, false
}

// Due reports whether a timer with the given deadline must fire once the
// injected clock has reached now: the first time now >= deadline.
func Due(now, deadline int64) bool { return deadline <= now }
