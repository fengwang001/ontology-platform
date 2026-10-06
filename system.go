package waitlist

import "sync"

type System struct {
	mu           sync.RWMutex
	config       Config
	flights      map[FlightKey]*flight
	entryFlights map[int64]FlightKey
	nextID       int64
	lastTime     int64
}

func NewSystem(config Config) (*System, error) {
	if config.MaxQueueEntries <= 0 || config.ConfirmationWindow <= 0 {
		return nil, errorf(ErrInvalidArgument, "max queue entries and confirmation window must be positive")
	}
	return &System{
		config:       config,
		flights:      make(map[FlightKey]*flight),
		entryFlights: make(map[int64]FlightKey),
		nextID:       1,
		lastTime:     -1,
	}, nil
}

func validKey(key FlightKey) bool {
	return key.FlightID != "" && key.Cabin != ""
}

func (s *System) AddFlight(now int64, key FlightKey, capacity, confirmed int) error {
	if now < 0 || !validKey(key) || capacity <= 0 || confirmed < 0 || confirmed > capacity {
		return errorf(ErrInvalidArgument, "invalid flight parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastTime {
		return errorf(ErrClockRollback, "time %d is before %d", now, s.lastTime)
	}
	if _, exists := s.flights[key]; exists {
		return errorf(ErrInvalidArgument, "flight already exists")
	}
	s.flights[key] = newFlight(key, capacity, confirmed, now)
	s.lastTime = now
	return nil
}

func (s *System) flightForUse(now int64, key FlightKey) (*flight, error) {
	f := s.flights[key]
	if now < s.lastTime {
		return nil, errorf(ErrClockRollback, "time %d is before %d", now, s.lastTime)
	}
	if f == nil {
		return nil, errorf(ErrFlightNotFound, "flight %s/%s does not exist", key.FlightID, key.Cabin)
	}
	if f.canceled {
		return nil, errorf(ErrFlightCanceled, "flight %s/%s is canceled", key.FlightID, key.Cabin)
	}
	return f, nil
}

func (s *System) entryForUse(now int64, entryID int64) (*flight, *Entry, error) {
	key, ok := s.entryFlights[entryID]
	f := s.flights[key]
	if now < s.lastTime {
		return nil, nil, errorf(ErrClockRollback, "time %d is before %d", now, s.lastTime)
	}
	if !ok || f == nil {
		return nil, nil, errorf(ErrEntryNotFound, "entry %d does not exist", entryID)
	}
	if f.canceled {
		return nil, nil, errorf(ErrFlightCanceled, "flight %s/%s is canceled", key.FlightID, key.Cabin)
	}
	entry := f.entries[entryID]
	if entry == nil {
		return nil, nil, errorf(ErrEntryNotFound, "entry %d does not exist", entryID)
	}
	return f, entry, nil
}

func (s *System) Register(now int64, key FlightKey, passenger string, partySize int, priority Priority) (int64, error) {
	if now < 0 || !validKey(key) || passenger == "" || partySize < 1 || partySize > 9 || !priority.Valid() {
		return 0, errorf(ErrInvalidArgument, "invalid registration parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.flightForUse(now, key)
	if err != nil {
		return 0, err
	}
	if _, quickDuplicate := f.passengerOpen[passenger]; quickDuplicate || f.waitingCount >= s.config.MaxQueueEntries {
		duplicate, waitingCount := f.previewOpenAndWaiting(now, s.config.ConfirmationWindow, passenger)
		if duplicate {
			return 0, errorf(ErrDuplicateEntry, "passenger %s already has an open entry", passenger)
		}
		if waitingCount >= s.config.MaxQueueEntries {
			return 0, errorf(ErrQueueFull, "waiting queue has %d entries", waitingCount)
		}
	}
	f.settle(now, s.config.ConfirmationWindow)
	f.commitSettlement()
	id := s.nextID
	s.nextID++
	s.entryFlights[id] = key

	entry := &Entry{ID: id, Flight: key, Passenger: passenger, PartySize: partySize, Priority: priority, RegisteredAt: now}
	f.entries[id] = entry
	f.insertWaiting(entry)
	f.waitingCount++
	s.lastTime = now
	return id, nil
}

func (s *System) Withdraw(now int64, entryID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || entryID <= 0 {
		return errorf(ErrInvalidArgument, "invalid time or entry id")
	}
	f, entry, err := s.entryForUse(now, entryID)
	if err != nil {
		return err
	}
	if f.pendingDue(entry, now) {
		return statusError(entry.Status, "entry has reached its confirmation deadline")
	}
	if entry.Status != StatusWaiting && entry.Status != StatusPending {
		return statusError(entry.Status, "entry is %s and cannot be withdrawn", entry.Status)
	}
	f.settle(now, s.config.ConfirmationWindow)
	f.commitSettlement()
	if entry.Status == StatusWaiting {
		f.waiting.remove(entry.ID, entry.Priority)
		f.waitingCount--
	} else if entry.Status == StatusPending {
		f.pending.remove(entry.ID)
		f.pendingCount -= entry.PartySize
		f.fulfill(now, s.config.ConfirmationWindow)
	}
	entry.Status = StatusWithdrawn
	f.removeOpen(entry)
	s.lastTime = now
	return nil
}

func (s *System) ChangePriority(now int64, entryID int64, priority Priority) error {
	if !priority.Valid() {
		return errorf(ErrInvalidArgument, "invalid priority")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || entryID <= 0 {
		return errorf(ErrInvalidArgument, "invalid time or entry id")
	}
	f, entry, err := s.entryForUse(now, entryID)
	if err != nil {
		return err
	}
	if f.pendingDue(entry, now) {
		return statusError(entry.Status, "entry has reached its confirmation deadline")
	}
	if entry.Status != StatusWaiting {
		return statusError(entry.Status, "entry is %s, not waiting", entry.Status)
	}
	f.settle(now, s.config.ConfirmationWindow)
	f.commitSettlement()
	if entry.Status == StatusWaiting && entry.Priority != priority {
		f.waiting.remove(entry.ID, entry.Priority)
		entry.Priority = priority
		f.waiting.insertOrdered(entry.ID, entry.RegisteredAt, priority)
	} else {
		entry.Priority = priority
	}
	s.lastTime = now
	return nil
}

func (s *System) Confirm(now int64, entryID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || entryID <= 0 {
		return errorf(ErrInvalidArgument, "invalid time or entry id")
	}
	f, entry, err := s.entryForUse(now, entryID)
	if err != nil {
		return err
	}
	if f.pendingDue(entry, now) {
		return statusError(entry.Status, "entry has reached its confirmation deadline")
	}
	if entry.Status != StatusPending {
		return statusError(entry.Status, "entry is %s, not pending confirmation", entry.Status)
	}
	f.settle(now, s.config.ConfirmationWindow)
	f.commitSettlement()
	f.pending.remove(entry.ID)
	f.pendingCount -= entry.PartySize
	f.confirmed += entry.PartySize
	entry.Status = StatusConfirmed
	f.removeOpen(entry)
	s.lastTime = now
	return nil
}

func (s *System) CancelConfirmed(now int64, entryID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || entryID <= 0 {
		return errorf(ErrInvalidArgument, "invalid time or entry id")
	}
	f, entry, err := s.entryForUse(now, entryID)
	if err != nil {
		return err
	}
	f.settle(now, s.config.ConfirmationWindow)
	f.commitSettlement()
	if entry.Status != StatusConfirmed {
		return statusError(entry.Status, "entry is %s, not confirmed", entry.Status)
	}
	f.confirmed -= entry.PartySize
	entry.Status = StatusWithdrawn
	f.fulfill(now, s.config.ConfirmationWindow)
	s.lastTime = now
	return nil
}

func (s *System) AdjustCapacity(now int64, key FlightKey, capacity int) error {
	if now < 0 || !validKey(key) || capacity <= 0 {
		return errorf(ErrInvalidArgument, "invalid capacity adjustment")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.flightForUse(now, key)
	if err != nil {
		return err
	}
	f.settle(now, s.config.ConfirmationWindow)
	f.commitSettlement()
	increased := capacity > f.capacity
	f.capacity = capacity
	if increased {
		f.fulfill(now, s.config.ConfirmationWindow)
	}
	s.lastTime = now
	return nil
}

func (s *System) CancelFlight(now int64, key FlightKey) error {
	if now < 0 || !validKey(key) {
		return errorf(ErrInvalidArgument, "invalid cancellation parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.flightForUse(now, key)
	if err != nil {
		return err
	}
	f.settle(now, s.config.ConfirmationWindow)
	f.commitSettlement()
	f.canceled = true
	for priority := PriorityLow; priority <= PriorityHigh; priority++ {
		for _, id := range f.waiting.ordered(priority) {
			f.entries[id].Status = StatusVoid
			f.removeOpen(f.entries[id])
		}
	}
	f.waiting = newWaitingLists(f.entries)
	for _, entry := range f.entries {
		if entry.Status == StatusPending {
			entry.Status = StatusVoid
			f.removeOpen(entry)
		}
	}
	f.pending = newPendingSet()
	f.waitingCount = 0
	f.pendingCount = 0
	s.lastTime = now
	return nil
}

func (s *System) Snapshot(now int64, key FlightKey) (*FlightState, error) {
	if now < 0 || !validKey(key) {
		return nil, errorf(ErrInvalidArgument, "invalid snapshot parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flights[key]
	if f == nil {
		return nil, errorf(ErrFlightNotFound, "flight %s/%s does not exist", key.FlightID, key.Cabin)
	}
	if now < s.lastTime {
		return nil, errorf(ErrClockRollback, "time %d is before %d", now, s.lastTime)
	}
	if !f.canceled {
		f.settle(now, s.config.ConfirmationWindow)
		f.commitSettlement()
		s.lastTime = now
	}
	state := &FlightState{
		Key:            f.key,
		Capacity:       f.capacity,
		Confirmed:      f.confirmed,
		PendingCount:   f.pendingCount,
		Canceled:       f.canceled,
		LastAcceptedAt: s.lastTime,
		Entries:        make(map[int64]Entry, len(f.entries)),
	}
	waiting := make([]int64, 0, f.waitingCount)
	for priority := int(PriorityHigh); priority >= int(PriorityLow); priority-- {
		waiting = append(waiting, f.waiting.ordered(Priority(priority))...)
	}
	state.WaitingOrder = waiting
	for id, entry := range f.entries {
		state.Entries[id] = *entry
	}
	return state, nil
}
