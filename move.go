package broadphase

type moveCandidate struct {
	xFlips         int
	yFlips         int
	xOppositeFlips int
	yOppositeFlips int
	xAfter         bool
	yAfter         bool
}

func endpointRange(low, high endpointKey) (endpointKey, endpointKey) {
	if compareEndpoint(high, low) < 0 {
		low, high = high, low
	}
	return low, high
}

func collectEndpointFlips(tree *endpointTree, low, high endpointKey, movedID int64, movingKind uint8, candidates map[int64]*moveCandidate, axis string) int {
	crossed := 0
	tree.between(low, high, func(key endpointKey) bool {
		if key.id == movedID {
			return true
		}
		state := candidates[key.id]
		if state == nil {
			state = &moveCandidate{}
			candidates[key.id] = state
		}
		if axis == "x" {
			state.xFlips++
			if key.kind != movingKind {
				state.xOppositeFlips++
			}
		} else {
			state.yFlips++
			if key.kind != movingKind {
				state.yOppositeFlips++
			}
		}
		crossed++
		return true
	})
	return crossed
}

// Move updates the tight box and reports whether the inflated box was rebuilt.
func (bp *Broadphase) Move(id int64, box Box) (MoveResult, error) {
	if id <= 0 || !validTightBox(box) {
		return MoveResult{}, ErrInvalidArgument
	}

	bp.mu.Lock()
	defer bp.mu.Unlock()

	moved, exists := bp.objects[id]
	if !exists {
		return MoveResult{}, ErrObjectNotFound
	}

	events := emptyEvents()
	refatted := !boxContains(moved.fat, box)
	oldFat := moved.fat
	moved.tight = box

	if !refatted {
		bp.recomputeTightContacts(moved, &events)
		bp.crossed = 0
		bp.checks = 0
		bp.contactChecks = len(bp.neighbors[moved.id])
		sortEventPairs(events)
		return MoveResult{Events: events, Refatted: refatted}, nil
	}

	newFat := expandBox(box, bp.margin)
	xOldLo := endpointKey{x: oldFat.LX, kind: endpointLo, id: id}
	xOldHi := endpointKey{x: oldFat.HX, kind: endpointHi, id: id}
	xNewLo := endpointKey{x: newFat.LX, kind: endpointLo, id: id}
	xNewHi := endpointKey{x: newFat.HX, kind: endpointHi, id: id}
	yOldLo := endpointKey{x: oldFat.LY, kind: endpointLo, id: id}
	yOldHi := endpointKey{x: oldFat.HY, kind: endpointHi, id: id}
	yNewLo := endpointKey{x: newFat.LY, kind: endpointLo, id: id}
	yNewHi := endpointKey{x: newFat.HY, kind: endpointHi, id: id}

	candidates := make(map[int64]*moveCandidate)
	low, high := endpointRange(xOldLo, xNewLo)
	bp.crossed = collectEndpointFlips(&bp.xTree, low, high, id, endpointLo, candidates, "x")
	low, high = endpointRange(xOldHi, xNewHi)
	bp.crossed += collectEndpointFlips(&bp.xTree, low, high, id, endpointHi, candidates, "x")
	low, high = endpointRange(yOldLo, yNewLo)
	collectEndpointFlips(&bp.yTree, low, high, id, endpointLo, candidates, "y")
	low, high = endpointRange(yOldHi, yNewHi)
	collectEndpointFlips(&bp.yTree, low, high, id, endpointHi, candidates, "y")

	bp.checks = 0
	for otherID, state := range candidates {
		if state.xFlips == 0 && state.yFlips == 0 {
			continue
		}
		other := bp.objects[otherID]
		if canCollide(moved, other) {
			bp.checks += state.xOppositeFlips
		}
		state.xAfter = newFat.LX < other.fat.HX && other.fat.LX < newFat.HX
		state.yAfter = newFat.LY < other.fat.HY && other.fat.LY < newFat.HY
	}

	bp.xTree.erase(xOldLo)
	bp.xTree.erase(xOldHi)
	bp.yTree.erase(yOldLo)
	bp.yTree.erase(yOldHi)
	bp.xIntervals.erase(oldFat.LX, id)
	moved.fat = newFat
	bp.xTree.insert(xNewLo)
	bp.xTree.insert(xNewHi)
	bp.yTree.insert(yNewLo)
	bp.yTree.insert(yNewHi)
	bp.xIntervals.insert(newFat.LX, newFat.HX, id)

	changedPairs := make(map[Pair]bool)
	for otherID, state := range candidates {
		pair := makePair(id, otherID)
		other := bp.objects[otherID]
		overlaps := canCollide(moved, other) && state.xAfter && state.yAfter
		changedPairs[pair] = overlaps
	}
	for pair := range bp.fat {
		if pair.A != id && pair.B != id {
			continue
		}
		otherID := pair.B
		if otherID == id {
			otherID = pair.A
		}
		if _, considered := changedPairs[pair]; considered {
			continue
		}
		other := bp.objects[otherID]
		changedPairs[pair] = canCollide(moved, other) && intersectsBox(moved.fat, other.fat)
	}

	for pair, overlaps := range changedPairs {
		if overlaps {
			if bp.addFatPair(pair) {
				events.FatEnter = append(events.FatEnter, pair)
			}
		} else if bp.removeFatPair(pair) {
			events.FatExit = append(events.FatExit, pair)
			if deletePair(bp.contact, pair) {
				events.ContactExit = append(events.ContactExit, pair)
			}
		}
	}

	for pair := range changedPairs {
		if _, fatNow := bp.fat[pair]; !fatNow {
			continue
		}
		otherID := pair.B
		if otherID == id {
			otherID = pair.A
		}
		other := bp.objects[otherID]
		touching := canCollide(moved, other) && intersectsBox(moved.tight, other.tight)
		if touching {
			if addPair(bp.contact, pair) {
				events.ContactEnter = append(events.ContactEnter, pair)
			}
		} else if deletePair(bp.contact, pair) {
			events.ContactExit = append(events.ContactExit, pair)
		}
	}

	bp.contactChecks = len(bp.neighbors[id])
	sortEventPairs(events)
	return MoveResult{Events: events, Refatted: refatted}, nil
}

func (bp *Broadphase) recomputeTightContacts(moved *object, events *Events) {
	for otherID := range bp.neighbors[moved.id] {
		pair := makePair(moved.id, otherID)
		other := bp.objects[otherID]
		touching := canCollide(moved, other) && intersectsBox(moved.tight, other.tight)
		if touching {
			if addPair(bp.contact, pair) {
				events.ContactEnter = append(events.ContactEnter, pair)
			}
		} else if deletePair(bp.contact, pair) {
			events.ContactExit = append(events.ContactExit, pair)
		}
	}
}
