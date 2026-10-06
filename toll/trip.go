package toll

import (
	"slices"
	"time"
)

// record is one gantry passage record.
type record struct {
	Gantry string
	TS     time.Time
	Seq    int64 // global arrival sequence, breaks ties deterministically
}

// classChange is one vehicle-class change event.
type classChange struct {
	Effective time.Time
	Class     VehicleClass
	Seq       int64
}

// trip is one entry-to-exit journey of a vehicle.
type trip struct {
	ID        string
	VehicleID string
	Seq       int // per-vehicle creation order
	Entry     record
	Exit      record
	HasExit   bool
	State     TripState
	Path      []string
	Fee       Money
	Collected Money
	Pending   Money
	Refunded  Money
	Adjs      []Adjustment
	Month     string
}

// contains reports whether ts falls in the trip's [entry, exit] interval
// (inclusive on both ends).
func (t *trip) contains(ts time.Time) bool {
	if t.HasExit {
		return !ts.Before(t.Entry.TS) && !ts.After(t.Exit.TS)
	}
	return !ts.Before(t.Entry.TS)
}

// monthStat aggregates settled trips of one vehicle in one natural month.
type monthStat struct {
	Collected        Money
	Pending          Money
	Refunded         Money
	UnrefundedExcess Money
}

// vehicle holds all per-vehicle state.
type vehicle struct {
	ID       string
	classes  []classChange // sorted by (Effective, Seq); [0] is registration
	trips    []*trip       // all trips in creation order
	open     *trip         // open or failed trip, nil when none
	buckets  map[string][]record
	kept     map[string][]record
	dups     []record
	orphans  []record
	expired  []record
	months   map[string]*monthStat
}

func newVehicle(id string, class VehicleClass) *vehicle {
	return &vehicle{
		ID:      id,
		classes: []classChange{{Class: class, Seq: -1}},
		buckets: map[string][]record{},
		kept:    map[string][]record{},
		months:  map[string]*monthStat{},
	}
}

// classAt returns the vehicle class effective at t: the latest change with
// Effective <= t (a change applies exactly at its effective time).
func (v *vehicle) classAt(t time.Time) VehicleClass {
	cls := v.classes[0].Class
	for _, c := range v.classes {
		if !c.Effective.After(t) {
			cls = c.Class
		}
	}
	return cls
}

func (v *vehicle) addClassChange(c classChange) {
	v.classes = append(v.classes, c)
	slices.SortStableFunc(v.classes, func(a, b classChange) int {
		if d := a.Effective.Compare(b.Effective); d != 0 {
			return d
		}
		return cmpInt64(a.Seq, b.Seq)
	})
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// recomputeKept re-derives the kept (non-duplicate) records of one gantry
// bucket: process records by (TS, Seq) ascending, keep a record only when
// no already-kept record lies strictly closer than the dedup window. An
// interval exactly equal to the window keeps both records. Returns the
// records newly kept and the records newly suppressed (displaced earlier
// records become duplicates).
func (v *vehicle) recomputeKept(gantry string, window time.Duration) (added, removed []record) {
	all := slices.Clone(v.buckets[gantry])
	slices.SortStableFunc(all, func(a, b record) int {
		if d := a.TS.Compare(b.TS); d != 0 {
			return d
		}
		return cmpInt64(a.Seq, b.Seq)
	})
	kept := []record{}
	for _, r := range all {
		dup := false
		for _, k := range kept {
			d := r.TS.Sub(k.TS)
			if d < 0 {
				d = -d
			}
			if d < window {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, r)
		}
	}
	old := v.kept[gantry]
	if recordsEqual(old, kept) {
		return nil, nil
	}
	v.kept[gantry] = kept
	for _, r := range kept {
		if !recordIn(old, r) {
			added = append(added, r)
		}
	}
	for _, r := range old {
		if !recordIn(kept, r) {
			removed = append(removed, r)
		}
	}
	return added, removed
}

func recordIn(rs []record, r record) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}

func recordsEqual(a, b []record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isKept reports whether r survives dedup in its gantry bucket.
func (v *vehicle) isKept(r record) bool {
	for _, k := range v.kept[r.Gantry] {
		if k == r {
			return true
		}
	}
	return false
}

// waypoints builds the inference waypoints of a trip with an exit record:
// entry, then kept intermediate records with entry.TS <= ts <= exit.TS
// ordered by (TS, gantry, Seq), then exit.
func (v *vehicle) waypoints(t *trip) []Waypoint {
	wps := []Waypoint{{Gantry: t.Entry.Gantry, TS: t.Entry.TS}}
	mid := []record{}
	for _, rs := range v.kept {
		for _, r := range rs {
			if !r.TS.Before(t.Entry.TS) && !r.TS.After(t.Exit.TS) {
				mid = append(mid, r)
			}
		}
	}
	slices.SortStableFunc(mid, func(a, b record) int {
		if d := a.TS.Compare(b.TS); d != 0 {
			return d
		}
		if a.Gantry != b.Gantry {
			return cmpString(a.Gantry, b.Gantry)
		}
		return cmpInt64(a.Seq, b.Seq)
	})
	for _, r := range mid {
		wps = append(wps, Waypoint{Gantry: r.Gantry, TS: r.TS})
	}
	wps = append(wps, Waypoint{Gantry: t.Exit.Gantry, TS: t.Exit.TS})
	return wps
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
