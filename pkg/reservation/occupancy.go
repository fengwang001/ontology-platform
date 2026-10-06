package reservation

// occupancyIndex keeps all occupancy in difference-event treaps:
// per feeder (holding + confirmed reservations) and a global one split into
// an all-state view and a confirmed-only view (capacity decrease checks count
// confirmed reservations exclusively).
//
// A validation over [Start, End) only touches event times inside that window,
// so its cost is independent of reservations disjoint from the window.
type occupancyIndex struct {
	feeder    map[int]*eventTreap
	global    *eventTreap
	confirmed *eventTreap
}

func newOccupancyIndex() *occupancyIndex {
	return &occupancyIndex{
		feeder:    map[int]*eventTreap{},
		global:    newEventTreap(),
		confirmed: newEventTreap(),
	}
}

func (o *occupancyIndex) feederTreap(feeder int) *eventTreap {
	t := o.feeder[feeder]
	if t == nil {
		t = newEventTreap()
		o.feeder[feeder] = t
	}
	return t
}

func eventAddFor(r *Reservation) (start, end, power int) {
	return r.Start, r.End, r.Power
}

// add indexes r. Only occupying reservations (holding, or confirmed not yet
// completed at the settled clock) must be added.
func (o *occupancyIndex) add(r *Reservation) {
	start, end, power := eventAddFor(r)
	o.feederTreap(r.Feeder).add(start, power)
	o.feederTreap(r.Feeder).add(end, -power)
	o.global.add(start, power)
	o.global.add(end, -power)
	if r.State == StateConfirmed {
		o.confirmed.add(start, power)
		o.confirmed.add(end, -power)
	}
}

func (o *occupancyIndex) remove(r *Reservation) {
	start, end, power := eventAddFor(r)
	if t := o.feeder[r.Feeder]; t != nil {
		t.add(start, -power)
		t.add(end, power)
	}
	o.global.add(start, -power)
	o.global.add(end, power)
	if r.State == StateConfirmed {
		o.confirmed.add(start, -power)
		o.confirmed.add(end, power)
	}
}

// candidateTimes collects, in ascending order, the times within iv at which
// occupancy or capacity can change: event times of the two involved treaps and
// capacity effective times.
func candidateTimes(iv Interval, feeder, global *eventTreap, cap *capacityTable) []int {
	times := make([]int, 0, 8)
	feeder.keysIn(iv.Start, iv.End, &times)
	global.keysIn(iv.Start, iv.End, &times)
	times = cap.effectiveTimesIn(iv.Start, iv.End, times)
	sortInts(times)
	return times
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func uniqueSorted(a []int) []int {
	if len(a) < 2 {
		return a
	}
	w := 1
	for i := 1; i < len(a); i++ {
		if a[i] != a[w-1] {
			a[w] = a[i]
			w++
		}
	}
	return a[:w]
}

// check validates placing `power` over iv on feeder, with reservation `exclude`
// removed from all sums first. It returns the first failing time and level;
// when both levels fail at the same time the feeder level wins.
//
// Self-exclusion is handled by shifting prefix sums: at each candidate time t
// the reservation's own contribution (power when iv0 contains t) is subtracted.
func (o *occupancyIndex) check(iv Interval, power, feeder, feederLimit int, exclude *Reservation, cap *capacityTable) *CapacityError {
	ft := o.feeder[feeder]
	times := candidateTimes(iv, ft, o.global, cap)
	// iv.Start itself must always be evaluated (no event may sit exactly there).
	if len(times) == 0 || times[0] != iv.Start {
		times = append([]int{iv.Start}, times...)
	}
	for _, t := range uniqueSorted(times) {
		if t >= iv.End {
			break
		}
		usedFeeder := 0
		if ft != nil {
			usedFeeder = ft.prefixBefore(t + 1)
		}
		usedGlobal := o.global.prefixBefore(t + 1)
		if exclude != nil && exclude.Start <= t && t < exclude.End {
			usedFeeder -= exclude.Power
			usedGlobal -= exclude.Power
		}
		if usedFeeder+power > feederLimit {
			return &CapacityError{Time: t, Level: LevelFeeder}
		}
		if usedGlobal+power > cap.at(t) {
			return &CapacityError{Time: t, Level: LevelTransformer}
		}
	}
	return nil
}

// checkConfirmedRange returns the first time >= iv.Start at which confirmed
// reservations exceed the capacity table within iv, or -1. Only capacity
// effective times and confirmed event times need inspection.
func (o *occupancyIndex) checkConfirmedRange(iv Interval, cap *capacityTable) int {
	var times []int
	o.confirmed.keysIn(iv.Start, iv.End, &times)
	times = cap.effectiveTimesIn(iv.Start, iv.End, times)
	sortInts(times)
	if len(times) == 0 || times[0] != iv.Start {
		times = append([]int{iv.Start}, times...)
	}
	for _, t := range uniqueSorted(times) {
		if t >= iv.End {
			break
		}
		if o.confirmed.prefixBefore(t+1) > cap.at(t) {
			return t
		}
	}
	return -1
}

// holdsAt returns the holding reservations occupying tm. Linear in the number
// of existing reservations; used only by capacity-decrease cancellation whose
// input size is bounded by the live reservation set.
func (o *occupancyIndex) holdsAt(tm int, all map[int64]*Reservation) []*Reservation {
	var out []*Reservation
	for _, r := range all {
		if r.State == StateHolding && r.Start <= tm && tm < r.End {
			out = append(out, r)
		}
	}
	return out
}
