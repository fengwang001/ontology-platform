package surge

// TierEvents 返回某区域的档位变化事件（按发生顺序）。
func (s *System) TierEvents(areaID string) ([]TierEvent, *Error) {
	if areaID == "" {
		return nil, errf(KindInvalidParam, "empty area id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.ledger.areas[areaID]
	if a == nil {
		return nil, errf(KindAreaNotFound, "area not found: %s", areaID)
	}
	src := s.events[areaID]
	out := make([]TierEvent, 0, len(src))
	for _, ev := range src {
		out = append(out, *ev)
	}
	return out, nil
}

// CurrentTier 返回某区域当前档位。
func (s *System) CurrentTier(areaID string) (int, *Error) {
	if areaID == "" {
		return 0, errf(KindInvalidParam, "empty area id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.ledger.areas[areaID]
	if a == nil {
		return 0, errf(KindAreaNotFound, "area not found: %s", areaID)
	}
	return a.currentTier, nil
}

// RiderSubsidy 返回骑手累计补贴与账目条目。
func (s *System) RiderSubsidy(riderID string) (total int64, entries []SubsidyEntry, e *Error) {
	if riderID == "" {
		return 0, nil, errf(KindInvalidParam, "empty rider id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.ledger.riders[riderID]
	if r == nil {
		return 0, nil, errf(KindRiderNotFound, "rider not found: %s", riderID)
	}
	src := s.ledger.entries[riderID]
	out := make([]SubsidyEntry, 0, len(src))
	var sum int64
	for _, en := range src {
		sum += en.Amount
		out = append(out, *en)
	}
	return sum, out, nil
}
