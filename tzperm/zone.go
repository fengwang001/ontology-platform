// Package tzperm provides attribute-level permission checks for time-valued
// ontology object properties.
//
// All normalization uses one fixed baseline: the object's region default
// timezone definition that is effective at the relevant instant. The
// querier timezone and the timezone annotated at data-entry time are never
// used as the normalization baseline.
package tzperm

import "sort"

// OffsetSec is an east-of-UTC offset expressed in seconds.
type OffsetSec int

// Transition marks an absolute instant (inclusive, Unix seconds, UTC) at
// which a zone starts observing a new offset.
type Transition struct {
	At     int64
	Offset OffsetSec
}

// ZoneRules is an immutable, self-contained timezone definition: a sorted
// list of absolute offset transitions. It deliberately models offsets
// directly instead of relying on a host tzdata database, so DST rules and
// historical definition versions are fully deterministic for tests.
//
// Transitions must be sorted by At with unique At values. The first
// transition's offset also applies to every earlier instant, so every zone
// must contain at least one transition.
type ZoneRules struct {
	Name        string
	Transitions []Transition
}

// WallStatus classifies how a local wall-clock reading maps to an instant.
type WallStatus int

const (
	// WallExact means the wall time exists exactly once in this zone.
	WallExact WallStatus = iota
	// WallGap means the wall time was skipped (spring-forward); it is
	// deterministically shifted forward by the gap length.
	WallGap
	// WallOverlap means the wall time occurs twice (fall-back); the earlier
	// instant is selected deterministically.
	WallOverlap
)

// OffsetAt returns the offset in effect at absolute instant t.
// Lookup is binary search: O(log m) where m is the number of offset
// transitions in this single zone definition.
func (z *ZoneRules) OffsetAt(t int64) OffsetSec {
	idx := sort.Search(len(z.Transitions), func(i int) bool {
		return z.Transitions[i].At > t
	})
	return z.Transitions[idx-1].Offset
}

// ToWall converts an absolute instant to zone-local wall-clock seconds.
func (z *ZoneRules) ToWall(t int64) int64 {
	return t + int64(z.OffsetAt(t))
}

// ResolveWall maps a zone-local wall-clock reading to an absolute instant.
//
// Every segment between two consecutive offset transitions maps instants to
// wall readings under one offset. A wall reading may be covered by zero
// segments (a skipped gap) or two segments (a repeated overlap). The
// resolution policy is fixed and independent of the caller:
//   - gap:     shift forward to the first valid instant after the jump;
//   - overlap: choose the earlier instant (the offset-before side);
//   - exact:   the unique candidate.
//
// The scan length is the number of offset transitions inside one zone
// definition, unrelated to how many region-default-zone versions have
// accumulated over time.
func (z *ZoneRules) ResolveWall(wall int64) (instant int64, status WallStatus) {
	type hit struct {
		instant int64
	}
	var hits []hit
	for i := range z.Transitions {
		offset := z.Transitions[i].Offset
		candidate := wall - int64(offset)
		segStart := z.Transitions[i].At
		var segEnd int64
		if i+1 < len(z.Transitions) {
			segEnd = z.Transitions[i+1].At
		} else {
			segEnd = 1<<63 - 1
		}
		if candidate >= segStart && candidate < segEnd {
			hits = append(hits, hit{instant: candidate})
		}
	}
	switch {
	case len(hits) == 1:
		return hits[0].instant, WallExact
	case len(hits) >= 2:
		return hits[0].instant, WallOverlap
	}

	// Gap: a spring-forward at instant T changes offset oldOff -> newOff
	// (newOff > oldOff), skipping readings in [T+oldOff, T+newOff). The
	// first valid instant representing the shifted reading is wall-newOff.
	for i := 1; i < len(z.Transitions); i++ {
		oldOff := z.Transitions[i-1].Offset
		newOff := z.Transitions[i].Offset
		if newOff > oldOff {
			at := z.Transitions[i].At
			if wall >= at+int64(oldOff) && wall < at+int64(newOff) {
				return wall - int64(newOff), WallGap
			}
		}
	}
	last := z.Transitions[len(z.Transitions)-1].Offset
	return wall - int64(last), WallExact
}

// SecondsOfDay returns the local second-of-day in [0,86400) of a wall-second
// value and the wall second at which that local day began. Local days are a
// plain floor division of the normalized wall timeline, so the same rule
// applies on 23-hour, 24-hour and 25-hour DST transition days.
func SecondsOfDay(wall int64) (sod int, dayStart int64) {
	dayStart = floorDiv(wall, 86400) * 86400
	return int(wall - dayStart), dayStart
}

func floorDiv(a, b int64) int64 {
	q := a / b
	r := a % b
	if r != 0 && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}
