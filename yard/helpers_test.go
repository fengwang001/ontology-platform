package yard

func (s *Scheduler) LookedCountForTest() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.looking
}

func (s *Scheduler) DispatchForTest(now int64) []Assignment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dispatch(now)
}

func (s *Scheduler) SeedFreeReefersForTest(dockIDs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, dockID := range dockIDs {
		id := []byte(dockID)
		if err := s.docks.AddDock(id, toDockKind(Reefer), 0); err == nil {
			s.maxNow = 0
		}
	}
}

func (s *Scheduler) SeedDryWaiterForTest(truck string, seq int64, now int64) {
	s.seedWaiter(Dry, truck, seq, now)
}

func (s *Scheduler) SeedReeferWaiterForTest(truck string, seq int64, now int64) {
	s.seedWaiter(Reefer, truck, seq, now)
}

func (s *Scheduler) seedWaiter(kind Kind, truck string, seq, now int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := &vehicle{
		truck:   []byte(truck),
		kind:    kind,
		state:   stateWaiting,
		seq:     seq,
		checkin: now,
	}
	s.vehicles[truck] = current
	s.standby[kind] = append(s.standby[kind], current)
	if kind == Reefer {
		s.waitingReefer++
	}
}
