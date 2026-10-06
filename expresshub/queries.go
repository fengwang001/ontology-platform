package expresshub

func (s *System) OpenBag(destination string) (BagView, bool, error) {
	if destination == "" {
		return BagView{}, false, errorf(InvalidArgument, "destination is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	id := s.openByStation[destination]
	if id == 0 {
		return BagView{}, false, nil
	}
	return snapshotBag(s.bags[id]), true, nil
}

func (s *System) Bag(id int64) (BagView, bool, error) {
	if id <= 0 {
		return BagView{}, false, errorf(InvalidArgument, "bag id must be positive")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	target := s.bags[id]
	if target == nil {
		return BagView{}, false, nil
	}
	return snapshotBag(target), true, nil
}

func (s *System) Location(waybill string) (ParcelLocation, bool, error) {
	if waybill == "" {
		return ParcelLocation{}, false, errorf(InvalidArgument, "waybill is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.active[waybill]
	if !ok {
		return ParcelLocation{}, false, nil
	}
	status := LocationPendingInvestigation
	if !entry.pending {
		switch s.bags[entry.bagID].view.Status {
		case BagStatusOpen:
			status = LocationInOpenBag
		case BagStatusSealed:
			status = LocationInSealedBag
		case BagStatusDispatched, BagStatusVerified:
			status = LocationInDispatchedBag
		}
	}
	return ParcelLocation{
		Waybill:     entry.parcel.Waybill,
		BagID:       entry.bagID,
		Destination: entry.parcel.Destination,
		Status:      status,
	}, true, nil
}

func snapshotBag(target *bag) BagView {
	result := target.view
	result.Items = append([]Parcel(nil), target.view.Items...)
	return result
}
