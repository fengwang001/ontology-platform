package standby

import "sort"

// 变更与查询操作。统一拒绝次序：
// 参数非法 > 时钟回退 > 航班不存在 > 航班已取消 > 条目不存在 >
// 条目状态不符 > 重复登记 > 队列已满。只返回最靠前的一类。

func validFlightName(name string) bool { return name != "" }
func validCabin(cabin string) bool     { return cabin != "" }

// CreateFlight 创建航班舱位。重复创建报参数非法。
func (s *System) CreateFlight(name, cabin string, capacity, queueLimit int, confirmationDelay, now int64) error {
	if !validFlightName(name) || !validCabin(cabin) || capacity <= 0 ||
		queueLimit < 0 || confirmationDelay < 0 {
		return ErrInvalid
	}
	s.lock()
	defer s.unlock()
	if now < s.now {
		return ErrClockRewind
	}
	key := flightKeyOf(name, cabin)
	if _, ok := s.flights[key]; ok {
		return ErrInvalid
	}
	s.flights[key] = newFlight(capacity, queueLimit, confirmationDelay)
	s.now = now
	return nil
}

// Register 登记候补条目。
func (s *System) Register(flightName, cabin, passenger string, party int, prio Priority, now int64) (EntryID, error) {
	if !validFlightName(flightName) || !validCabin(cabin) || passenger == "" ||
		party < 1 || party > 9 || !prio.Valid() || now < 0 {
		return 0, ErrInvalid
	}
	s.lock()
	defer s.unlock()
	if now < s.now {
		return 0, ErrClockRewind
	}
	f, ok := s.flights[flightKeyOf(flightName, cabin)]
	if !ok {
		return 0, ErrFlightNotFound
	}
	if f.canceled {
		return 0, ErrFlightCanceled
	}
	// 以下检查可能被试探性结算改变：先结算，再判定，拒绝则回滚。
	t := s.begin()
	f.settleExpirations(t, now)
	if f.active[passenger] != nil {
		t.rollback()
		return 0, ErrDuplicate
	}
	if f.waitingN >= f.queueLimit {
		t.rollback()
		return 0, ErrQueueFull
	}
	s.nextID++
	t.idTaken = true
	e := &Entry{
		ID:         s.nextID,
		Flight:     flightName,
		Cabin:      cabin,
		Passenger:  passenger,
		Party:      party,
		Priority:   prio,
		Registered: now,
		State:      StateWaiting,
	}
	leaf := &trieLeaf{entry: e}
	leaf.keyHi, leaf.keyLo = waitKey(e)
	f.waiting[prio].insert(leaf)
	e.waitLeaf = leaf
	f.active[passenger] = e
	s.allEntries[e.ID] = e
	f.entries[e.ID] = e
	f.waitingN++
	// 登记本身也是一次“进入候补”的时点：若当前已存在空座，立即按规则兑现。
	f.fulfill(t, now)
	s.now = now
	return e.ID, nil
}

// lookupEntry 在全局索引中定位条目并做存在性/状态前置校验（结算前）。
func (s *System) lookupEntry(id EntryID) (*Entry, *flight, error) {
	e, ok := s.allEntries[id]
	if !ok {
		return nil, nil, ErrEntryNotFound
	}
	return e, s.flights[flightKeyOf(e.Flight, e.Cabin)], nil
}

// Withdraw 撤回条目。候补中：直接撤回；待确认：视为释放并引发兑现。
func (s *System) Withdraw(id EntryID, now int64) error {
	if id == 0 || now < 0 {
		return ErrInvalid
	}
	s.lock()
	defer s.unlock()
	if now < s.now {
		return ErrClockRewind
	}
	e, f, err := s.lookupEntry(id)
	if err != nil {
		return err
	}
	if f.canceled {
		return ErrFlightCanceled
	}
	if e.State != StateWaiting && e.State != StatePending {
		return ErrEntryState
	}
	t := s.begin()
	f.settleExpirations(t, now)
	if e.State != StateWaiting && e.State != StatePending {
		t.rollback()
		return ErrEntryState
	}
	switch e.State {
	case StateWaiting:
		leaf := e.waitLeaf
		prio := e.Priority
		f.waiting[prio].delete(leaf)
		f.waitingN--
		_, hadActive := f.active[e.Passenger]
		delete(f.active, e.Passenger)
		e.waitLeaf = nil
		e.State = StateWithdrawn
		t.undo = append(t.undo, func() {
			e.State = StateWaiting
			e.waitLeaf = leaf
			f.waitingN++
			f.waiting[prio].insert(leaf)
			if hadActive {
				f.active[e.Passenger] = e
			}
		})
	case StatePending:
		f.removePending(t, e)
		e.State = StateWithdrawn
		t.undo = append(t.undo, func() { e.State = StatePending })
		f.fulfill(t, now)
	}
	s.now = now
	return nil
}

// Confirm 在 当前时刻 < 期限 时确认待确认条目。
func (s *System) Confirm(id EntryID, now int64) error {
	if id == 0 || now < 0 {
		return ErrInvalid
	}
	s.lock()
	defer s.unlock()
	if now < s.now {
		return ErrClockRewind
	}
	e, f, err := s.lookupEntry(id)
	if err != nil {
		return err
	}
	if f.canceled {
		return ErrFlightCanceled
	}
	if e.State != StatePending {
		return ErrEntryState
	}
	t := s.begin()
	f.settleExpirations(t, now)
	if e.State != StatePending {
		t.rollback()
		return ErrEntryState
	}
	if now >= e.Deadline {
		// 理论上不会到达：到期条目已在结算中变为过期。保留以保证语义显式。
		t.rollback()
		return ErrEntryState
	}
	f.removePending(t, e)
	e.State = StateConfirmed
	f.confirmed += e.Party
	t.undo = append(t.undo, func() {
		e.State = StatePending
		f.confirmed -= e.Party
	})
	s.now = now
	return nil
}

// ChangePriority 调整候补条目等级；保留原登记时刻，不引发兑现。
func (s *System) ChangePriority(id EntryID, prio Priority, now int64) error {
	if id == 0 || now < 0 || !prio.Valid() {
		return ErrInvalid
	}
	s.lock()
	defer s.unlock()
	if now < s.now {
		return ErrClockRewind
	}
	e, f, err := s.lookupEntry(id)
	if err != nil {
		return err
	}
	if f.canceled {
		return ErrFlightCanceled
	}
	if e.State != StateWaiting {
		return ErrEntryState
	}
	t := s.begin()
	f.settleExpirations(t, now)
	if e.State != StateWaiting {
		t.rollback()
		return ErrEntryState
	}
	oldPrio := e.Priority
	if oldPrio != prio {
		leaf := e.waitLeaf
		f.waiting[oldPrio].delete(leaf)
		f.waiting[prio].insert(leaf)
		e.Priority = prio
		t.undo = append(t.undo, func() {
			f.waiting[prio].delete(leaf)
			f.waiting[oldPrio].insert(leaf)
			e.Priority = oldPrio
		})
	}
	s.now = now
	return nil
}

// CancelConfirmed 取消已确认旅客：释放座位并立即兑现。
func (s *System) CancelConfirmed(id EntryID, now int64) error {
	if id == 0 || now < 0 {
		return ErrInvalid
	}
	s.lock()
	defer s.unlock()
	if now < s.now {
		return ErrClockRewind
	}
	e, f, err := s.lookupEntry(id)
	if err != nil {
		return err
	}
	if f.canceled {
		return ErrFlightCanceled
	}
	if e.State != StateConfirmed {
		return ErrEntryState
	}
	t := s.begin()
	f.settleExpirations(t, now)
	if e.State != StateConfirmed {
		t.rollback()
		return ErrEntryState
	}
	f.confirmed -= e.Party
	e.State = StateWithdrawn
	t.undo = append(t.undo, func() {
		f.confirmed += e.Party
		e.State = StateConfirmed
	})
	f.fulfill(t, now)
	s.now = now
	return nil
}

// SetCapacity 调整容量。仅上调（或跨过零点的恢复）触发兑现。
func (s *System) SetCapacity(flightName, cabin string, capacity int, now int64) error {
	if !validFlightName(flightName) || !validCabin(cabin) || capacity <= 0 || now < 0 {
		return ErrInvalid
	}
	s.lock()
	defer s.unlock()
	if now < s.now {
		return ErrClockRewind
	}
	f, ok := s.flights[flightKeyOf(flightName, cabin)]
	if !ok {
		return ErrFlightNotFound
	}
	if f.canceled {
		return ErrFlightCanceled
	}
	t := s.begin()
	f.settleExpirations(t, now)
	old := f.capacity
	f.capacity = capacity
	t.undo = append(t.undo, func() { f.capacity = old })
	// 下调不兑现；上调后有空余才兑现。负空余跨过零点时一次兑现即可。
	if capacity > old || f.freeSeats() > 0 {
		f.fulfill(t, now)
	}
	s.now = now
	return nil
}

// CancelFlight 取消航班：候补中/待确认条目全部作废，已确认条目标记作废。
func (s *System) CancelFlight(flightName, cabin string, now int64) error {
	if !validFlightName(flightName) || !validCabin(cabin) || now < 0 {
		return ErrInvalid
	}
	s.lock()
	defer s.unlock()
	if now < s.now {
		return ErrClockRewind
	}
	f, ok := s.flights[flightKeyOf(flightName, cabin)]
	if !ok {
		return ErrFlightNotFound
	}
	if f.canceled {
		return ErrFlightCanceled
	}
	t := s.begin()
	f.settleExpirations(t, now)
	oldConfirmed := f.confirmed
	f.canceled = true
	f.confirmed = 0
	t.undo = append(t.undo, func() { f.canceled = false; f.confirmed = oldConfirmed })
	// 待确认条目作废。
	for !f.pending.empty() {
		e := f.pending.first().entry
		f.removePending(t, e)
		e.State = StateVoided
		t.undo = append(t.undo, func(en *Entry) func() {
			return func() { en.State = StatePending }
		}(e))
	}
	// 候补条目作废。
	for p := int(PrioLow); p <= int(PrioHigh); p++ {
		for {
			leaf := f.waiting[p].first()
			if leaf == nil {
				break
			}
			e := leaf.entry
			f.waiting[p].delete(leaf)
			f.waitingN--
			_, hadActive := f.active[e.Passenger]
			delete(f.active, e.Passenger)
			e.waitLeaf = nil
			e.State = StateVoided
			t.undo = append(t.undo, func(en *Entry, l *trieLeaf, prio int, active bool) func() {
				return func() {
					en.State = StateWaiting
					en.waitLeaf = l
					f.waitingN++
					f.waiting[prio].insert(l)
					if active {
						f.active[en.Passenger] = en
					}
				}
			}(e, leaf, p, hadActive))
		}
	}
	// 其余已结束条目（已撤回/已过期/已确认）一并转入作废状态；
	// 六态的可区分性体现在取消时刻之前的查询与各状态来源上。
	for _, e := range f.entries {
		if e.State != StateVoided {
			old := e.State
			e.State = StateVoided
			t.undo = append(t.undo, func(en *Entry, st EntryState) func() {
				return func() { en.State = st }
			}(e, old))
		}
	}
	s.now = now
	return nil
}

// Snapshot 查询：先按时序结算过期，读取后撤销结算，不推进时钟。
func (s *System) Snapshot(flightName, cabin string, now int64) (FlightSnapshot, error) {
	if !validFlightName(flightName) || !validCabin(cabin) || now < 0 {
		return FlightSnapshot{}, ErrInvalid
	}
	s.lock()
	defer s.unlock()
	if now < s.now {
		return FlightSnapshot{}, ErrClockRewind
	}
	f, ok := s.flights[flightKeyOf(flightName, cabin)]
	if !ok {
		return FlightSnapshot{}, ErrFlightNotFound
	}
	if !f.canceled {
		t := s.begin()
		f.settleExpirations(t, now)
		snap := buildSnapshot(f, flightName, cabin)
		t.rollback()
		return snap, nil
	}
	return buildSnapshot(f, flightName, cabin), nil
}

func buildSnapshot(f *flight, name, cabin string) FlightSnapshot {
	infos := make([]EntryInfo, 0, len(f.entries))
	for _, e := range f.entries {
		infos = append(infos, e.Info())
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
	return FlightSnapshot{
		Flight:            name,
		Cabin:             cabin,
		Capacity:          f.capacity,
		Confirmed:         f.confirmed,
		Pending:           f.pendingN,
		WaitingCount:      f.waitingN,
		Free:              f.freeSeats(),
		Canceled:          f.canceled,
		QueueLimit:        f.queueLimit,
		ConfirmationDelay: f.delay,
		Entries:           infos,
	}
}
