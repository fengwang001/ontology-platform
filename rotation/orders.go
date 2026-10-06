package rotation

import "sort"

// order is one curtailment directive on a region.
// Windows are [Start, End), aligned to slot boundaries.
// Rescheduling a window is expressed as cancel + issue.
type order struct {
	id     int
	region string
	start  int64
	end    int64
	// changes maps an effective slot boundary to the level in force from
	// that boundary. Effective level at slot start s is the value of the
	// largest key <= s (the entry at key==start is the issued level).
	changes map[int64]int
	// canceled is true after a cancel that is at least conceptually applied.
	// An order canceled entirely before its window start is dropped instead
	// ("视同从未存在") and never reaches this map.
	canceled bool
	// cancelAt is the slot boundary from which the cancel is in force.
	cancelAt int64
}

func (o *order) levelAt(slotStart int64) int {
	if slotStart < o.start || slotStart >= o.end {
		return 0
	}
	if o.canceled && slotStart >= o.cancelAt {
		return 0
	}
	best := 0
	var bestKey int64
	for k, v := range o.changes {
		if k <= slotStart && k > bestKey {
			bestKey, best = k, v
		}
	}
	return best
}

// orderBook holds the orders of one region.
//
// Performance contract: active contains exactly the not-yet-canceled and
// window-not-ended orders for slots no earlier than the watermark boundary
// (canceled-before-start orders are never inserted). Effective-level lookup
// for a slot therefore scans only live orders, so its cost does not grow with
// the number of canceled or already ended orders.
type orderBook struct {
	region *Region
	byID   map[int]*order
	active map[int]*order
}

func newOrderBook(r *Region) *orderBook {
	return &orderBook{region: r, byID: map[int]*order{}, active: map[int]*order{}}
}

// add inserts a newly issued order. The caller has validated level/window.
func (b *orderBook) add(id int, start, end int64, level int) *order {
	o := &order{
		id: id, region: b.region.ID, start: start, end: end,
		changes: map[int64]int{start: level},
	}
	b.byID[id] = o
	b.active[id] = o
	return o
}

// changeLevel records a level change effective from slot start effFrom.
func (b *orderBook) changeLevel(o *order, effFrom int64, level int) {
	if !o.canceled {
		o.changes[effFrom] = level
	} else {
		// Change effective at/after the cancel boundary: keep the order in
		// the canceled state and record the entry so the timeline stays
		// exactly replayable (a later cancel-before-start cannot happen).
		o.changes[effFrom] = level
	}
}

// cancel marks an order canceled from effFrom. A cancel whose effective
// boundary is at or before the order start drops the order entirely; the
// caller guarantees such an order has never influenced a frozen slot.
func (b *orderBook) cancel(o *order, effFrom int64, dropEntirely bool) {
	if dropEntirely {
		delete(b.byID, o.id)
		delete(b.active, o.id)
		return
	}
	o.canceled = true
	o.cancelAt = effFrom
}

// effectiveLevel returns the maximum level over all active orders covering
// the slot that starts at slotStart.
func (b *orderBook) effectiveLevel(slotStart int64) int {
	best := 0
	for _, o := range b.active {
		if v := o.levelAt(slotStart); v > best {
			best = v
		}
	}
	return best
}

// prune removes orders whose window ended strictly before the boundary that is
// about to open, and orders canceled from a boundary no later than it.
// Called while crossing boundaries so ended/canceled orders never accumulate.
func (b *orderBook) prune(openingBoundary int64) {
	for id, o := range b.active {
		if o.end <= openingBoundary {
			delete(b.active, id)
			continue
		}
		// The order is dropped once its last affected slot is behind us:
		// either its window ended before openingBoundary, or its cancel was
		// effective strictly before the opening slot. A cancel effective at
		// openingBoundary still shapes that slot and stays one more round.
		if o.canceled && o.cancelAt < openingBoundary {
			delete(b.active, id)
		}
	}
}

// sortedActiveIDs is a small helper for deterministic diagnostics/logs.
func (b *orderBook) sortedActiveIDs() []int {
	ids := make([]int, 0, len(b.active))
	for id := range b.active {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}
