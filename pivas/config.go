package pivas

import "sort"

func nonEmpty(s string) bool { return s != "" }

// commitClock 在操作成功后推进时钟。
func (s *System) commitClock(now int) { s.now = now }

func (s *System) SetTransport(now int, st Storage, seconds int) error {
	if st != Room && st != Cold {
		return errf(ErrInvalidParam, "unknown storage %d", st)
	}
	if seconds <= 0 {
		return errf(ErrInvalidParam, "transport seconds must be positive, got %d", seconds)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.transport[st] = seconds
	s.commitClock(now)
	return nil
}

func (s *System) SetDuration(now, n, seconds int) error {
	if n <= 0 || seconds <= 0 {
		return errf(ErrInvalidParam, "duration count and seconds must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.durations[n] = seconds
	s.commitClock(now)
	return nil
}

// DurationsOK 校验已登记的 1..maxN 时长对照完整且严格递增。
// 在洁净台容量所要求的最大医嘱数量登记齐全前受理医嘱会报无可行安排。
func (s *System) durationOK(n int) (int, bool) {
	d, ok := s.durations[n]
	return d, ok && d > 0
}

func (s *System) RegisterBench(now int, b Bench) error {
	if !nonEmpty(b.ID) || b.Capacity <= 0 || b.ClearGap <= 0 {
		return errf(ErrInvalidParam, "bench id non-empty, capacity and gap positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, exists := s.benches[b.ID]; exists {
		return errf(ErrInvalidParam, "bench %q already registered", b.ID)
	}
	st := &benchState{bench: b}
	s.benches[b.ID] = st
	s.benchIDs = append(s.benchIDs, b.ID)
	sort.SliceStable(s.benchIDs, func(i, j int) bool { return s.benchIDs[i] < s.benchIDs[j] })
	s.commitClock(now)
	return nil
}

func (s *System) RegisterDrug(now int, id string, d Drug) error {
	if !nonEmpty(id) || !nonEmpty(d.SolventClass) ||
		d.RoomStableSec <= 0 || d.ColdStableSec <= 0 {
		return errf(ErrInvalidParam, "drug id/solvent non-empty and stability positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.catalog.upsert(id, d)
	s.commitClock(now)
	return nil
}

func (s *System) AddIncompatibility(now int, a, b string) error {
	if !nonEmpty(a) || !nonEmpty(b) || a == b {
		return errf(ErrInvalidParam, "incompatibility ids must be non-empty and distinct")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.catalog.addBad(a, b)
	s.commitClock(now)
	return nil
}
