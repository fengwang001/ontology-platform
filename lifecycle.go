package broadphase

// Remove deletes an object and exits all pairs containing it.
func (bp *Broadphase) Remove(id int64) (Events, error) {
	if id <= 0 {
		return Events{}, ErrInvalidArgument
	}

	bp.mu.Lock()
	defer bp.mu.Unlock()

	removed, exists := bp.objects[id]
	if !exists {
		return Events{}, ErrObjectNotFound
	}

	events := emptyEvents()
	for pair := range bp.fat {
		if pair.A != id && pair.B != id {
			continue
		}
		events.FatExit = append(events.FatExit, pair)
		if _, touching := bp.contact[pair]; touching {
			events.ContactExit = append(events.ContactExit, pair)
		}
	}

	for _, pair := range events.FatExit {
		bp.removeFatPair(pair)
		delete(bp.contact, pair)
	}

	bp.xTree.erase(endpointKey{x: removed.fat.LX, kind: endpointLo, id: id})
	bp.xTree.erase(endpointKey{x: removed.fat.HX, kind: endpointHi, id: id})
	bp.yTree.erase(endpointKey{x: removed.fat.LY, kind: endpointLo, id: id})
	bp.yTree.erase(endpointKey{x: removed.fat.HY, kind: endpointHi, id: id})
	bp.xIntervals.erase(removed.fat.LX, id)
	delete(bp.objects, id)
	bp.count--
	bp.crossed = 0
	bp.checks = 0
	bp.contactChecks = 0
	sortEventPairs(events)
	return events, nil
}

// SetFilter changes both filter bits without changing either box.
func (bp *Broadphase) SetFilter(id int64, layer, mask int) (Events, error) {
	if id <= 0 || !validFilter(layer, mask) {
		return Events{}, ErrInvalidArgument
	}

	bp.mu.Lock()
	defer bp.mu.Unlock()

	changed, exists := bp.objects[id]
	if !exists {
		return Events{}, ErrObjectNotFound
	}

	events := emptyEvents()
	changed.layer = uint16(layer)
	changed.mask = uint16(mask)

	for _, other := range bp.objects {
		if other.id == id {
			continue
		}
		pair := makePair(id, other.id)
		_, wasFat := bp.fat[pair]
		_, wasContact := bp.contact[pair]
		collidable := canCollide(changed, other)
		fatNow := collidable && intersectsBox(changed.fat, other.fat)
		contactNow := fatNow && intersectsBox(changed.tight, other.tight)

		if fatNow && !wasFat {
			bp.addFatPair(pair)
			events.FatEnter = append(events.FatEnter, pair)
		} else if !fatNow && wasFat {
			bp.removeFatPair(pair)
			events.FatExit = append(events.FatExit, pair)
		}
		if contactNow && !wasContact {
			addPair(bp.contact, pair)
			events.ContactEnter = append(events.ContactEnter, pair)
		} else if !contactNow && wasContact {
			deletePair(bp.contact, pair)
			events.ContactExit = append(events.ContactExit, pair)
		}
	}

	bp.crossed = 0
	bp.checks = 0
	bp.contactChecks = 0
	sortEventPairs(events)
	return events, nil
}
