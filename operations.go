package broadphase

func addPair(set map[Pair]struct{}, pair Pair) bool {
	if _, exists := set[pair]; exists {
		return false
	}
	set[pair] = struct{}{}
	return true
}

func deletePair(set map[Pair]struct{}, pair Pair) bool {
	if _, exists := set[pair]; !exists {
		return false
	}
	delete(set, pair)
	return true
}

func (bp *Broadphase) addFatPair(pair Pair) bool {
	if !addPair(bp.fat, pair) {
		return false
	}
	if bp.neighbors[pair.A] == nil {
		bp.neighbors[pair.A] = make(map[int64]struct{})
	}
	if bp.neighbors[pair.B] == nil {
		bp.neighbors[pair.B] = make(map[int64]struct{})
	}
	bp.neighbors[pair.A][pair.B] = struct{}{}
	bp.neighbors[pair.B][pair.A] = struct{}{}
	return true
}

func (bp *Broadphase) removeFatPair(pair Pair) bool {
	if !deletePair(bp.fat, pair) {
		return false
	}
	delete(bp.neighbors[pair.A], pair.B)
	delete(bp.neighbors[pair.B], pair.A)
	return true
}

func emptyEvents() Events {
	return Events{
		FatEnter:     []Pair{},
		FatExit:      []Pair{},
		ContactEnter: []Pair{},
		ContactExit:  []Pair{},
	}
}

// Insert registers an id with a tight box. layer and mask default to 1.
func (bp *Broadphase) Insert(id int64, box Box, filters ...int) (Events, error) {
	layer, mask := 1, 1
	if len(filters) > 0 {
		layer = filters[0]
	}
	if len(filters) > 1 {
		mask = filters[1]
	}
	if id <= 0 || !validTightBox(box) || !validFilter(layer, mask) {
		return Events{}, ErrInvalidArgument
	}

	bp.mu.Lock()
	defer bp.mu.Unlock()

	if _, exists := bp.objects[id]; exists {
		return Events{}, ErrObjectExists
	}
	if bp.count == bp.capacity {
		return Events{}, ErrCapacityReached
	}

	events := emptyEvents()
	inserted := &object{
		id:    id,
		tight: box,
		fat:   expandBox(box, bp.margin),
		layer: uint16(layer),
		mask:  uint16(mask),
	}

	bp.xIntervals.overlapsX(inserted.fat, func(otherID int64) {
		current := bp.objects[otherID]
		pair := makePair(id, current.id)
		if canCollide(inserted, current) && intersectsBoxY(inserted.fat, current.fat) {
			bp.addFatPair(pair)
			events.FatEnter = append(events.FatEnter, pair)
			if intersectsBox(inserted.tight, current.tight) {
				addPair(bp.contact, pair)
				events.ContactEnter = append(events.ContactEnter, pair)
			}
		}
	})

	bp.objects[id] = inserted
	bp.count++
	bp.xTree.insert(endpointKey{x: inserted.fat.LX, kind: endpointLo, id: id})
	bp.xTree.insert(endpointKey{x: inserted.fat.HX, kind: endpointHi, id: id})
	bp.yTree.insert(endpointKey{x: inserted.fat.LY, kind: endpointLo, id: id})
	bp.yTree.insert(endpointKey{x: inserted.fat.HY, kind: endpointHi, id: id})
	bp.xIntervals.insert(inserted.fat.LX, inserted.fat.HX, id)
	bp.crossed = 0
	bp.checks = 0
	bp.contactChecks = 0
	sortEventPairs(events)
	return events, nil
}
