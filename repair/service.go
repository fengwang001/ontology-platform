package repair

import (
	"strings"
	"sync"
)

type Service struct {
	mu          sync.Mutex
	config      Config
	tickets     map[int64]*ticket
	contractors map[int64]*contractor
	queue       *priorityQueue
	response    *timerHeap
	completion  *timerHeap
	events      []event
	nextTicket  int64
	nextWorker  int64
	lastNow     int
	clockSeen   bool
}

func New(config Config) (*Service, error) {
	if config.RejectLimit <= 0 {
		return nil, ErrInvalidArgument
	}
	for _, limit := range config.Limits {
		if limit.Response < 0 || limit.Completion < 0 {
			return nil, ErrInvalidArgument
		}
	}
	return &Service{
		config:      config,
		tickets:     make(map[int64]*ticket),
		contractors: make(map[int64]*contractor),
		queue:       newPriorityQueue(),
		response:    &timerHeap{},
		completion:  &timerHeap{},
		nextTicket:  1,
		nextWorker:  1,
	}, nil
}

func (s *Service) RegisterContractor(now int, trades, buildings []string, capacity int, acceptsUrgent bool) (Contractor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || capacity <= 0 || len(trades) == 0 || len(buildings) == 0 || hasBlank(trades) || hasBlank(buildings) {
		return Contractor{}, ErrInvalidArgument
	}
	if err := s.begin(now); err != nil {
		return Contractor{}, err
	}
	worker := &contractor{
		id:            s.nextWorker,
		trades:        stringSet(trades),
		buildings:     stringSet(buildings),
		capacity:      capacity,
		acceptsUrgent: acceptsUrgent,
		registration:  int(s.nextWorker),
		active:        make(map[int64]*ticket),
	}
	s.contractors[worker.id] = worker
	s.nextWorker++
	return snapshotContractor(worker), nil
}

func (s *Service) Submit(now int, tenantID int64, trade, building string, level Level) (Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || tenantID <= 0 || strings.TrimSpace(trade) == "" || strings.TrimSpace(building) == "" || level < Level1 || level > Urgent {
		return Ticket{}, ErrInvalidArgument
	}
	if err := s.begin(now); err != nil {
		return Ticket{}, err
	}
	work := &ticket{
		id:             s.nextTicket,
		tenantID:       tenantID,
		trade:          trade,
		building:       building,
		level:          level,
		submittedAt:    now,
		levelStartedAt: now,
		status:         StatusQueued,
		rejectedBy:     make(map[int64]bool),
	}
	s.tickets[work.id] = work
	s.nextTicket++
	s.queue.push(work)
	return s.snapshotTicket(work), nil
}

func (s *Service) DispatchNext(now int) (Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return Ticket{}, ErrInvalidArgument
	}
	if s.clockSeen && now < s.lastNow {
		return Ticket{}, ErrClockBack
	}
	snapshot := s.snapshotDueState(now)
	if err := s.begin(now); err != nil {
		snapshot.restore(s)
		return Ticket{}, err
	}
	skipped := make([]*ticket, 0)
	for s.queue.len() > 0 {
		work := s.queue.popAt(0)
		if s.assign(work, now) {
			for _, candidate := range skipped {
				s.queue.push(candidate)
			}
			return s.snapshotTicket(work), nil
		}
		skipped = append(skipped, work)
	}
	for _, work := range skipped {
		s.queue.push(work)
	}
	snapshot.restore(s)
	return Ticket{}, ErrNoCandidate
}

func (s *Service) DispatchAll(now int) ([]Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return nil, ErrInvalidArgument
	}
	if err := s.begin(now); err != nil {
		return nil, err
	}
	pending := make([]*ticket, 0, s.queue.len())
	for s.queue.len() > 0 {
		pending = append(pending, s.queue.pop())
	}
	assigned := make([]Ticket, 0)
	for _, work := range pending {
		if s.assign(work, now) {
			assigned = append(assigned, s.snapshotTicket(work))
		} else {
			s.queue.push(work)
		}
	}
	return assigned, nil
}

func (s *Service) Confirm(now int, ticketID, contractorID int64) (Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || ticketID <= 0 || contractorID <= 0 {
		return Ticket{}, ErrInvalidArgument
	}
	if s.clockSeen && now < s.lastNow {
		return Ticket{}, ErrClockBack
	}
	work, worker, err := s.parties(ticketID, contractorID)
	if err != nil {
		return Ticket{}, err
	}
	if work.status != StatusAssigned || work.assignee != contractorID || now > work.responseDue {
		return Ticket{}, ErrInvalidState
	}
	if err := s.begin(now); err != nil {
		return Ticket{}, err
	}
	if work.status != StatusAssigned {
		return Ticket{}, ErrInvalidState
	}
	work.status = StatusConfirmed
	work.confirmedAt = now
	work.completeDue = now + s.config.Limits[work.level].Completion
	s.response.remove(work.id)
	s.completion.push(timerEntry{ticketID: work.id, due: work.completeDue + 1})
	s.addEvent(now, work, EventConfirmed, worker.id, 0, 0)
	return s.snapshotTicket(work), nil
}

func (s *Service) Reject(now int, ticketID, contractorID int64) (Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || ticketID <= 0 || contractorID <= 0 {
		return Ticket{}, ErrInvalidArgument
	}
	if s.clockSeen && now < s.lastNow {
		return Ticket{}, ErrClockBack
	}
	work, worker, err := s.parties(ticketID, contractorID)
	if err != nil {
		return Ticket{}, err
	}
	if work.status != StatusAssigned || work.assignee != contractorID || now > work.responseDue {
		return Ticket{}, ErrInvalidState
	}
	if err := s.begin(now); err != nil {
		return Ticket{}, err
	}
	s.requeueRejected(work, now, worker.id, EventRejected)
	return s.snapshotTicket(work), nil
}

func (s *Service) Complete(now int, ticketID, contractorID int64) (Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || ticketID <= 0 || contractorID <= 0 {
		return Ticket{}, ErrInvalidArgument
	}
	if s.clockSeen && now < s.lastNow {
		return Ticket{}, ErrClockBack
	}
	work, worker, err := s.parties(ticketID, contractorID)
	if err != nil {
		return Ticket{}, err
	}
	if err := s.begin(now); err != nil {
		return Ticket{}, err
	}
	if work.status != StatusConfirmed && work.status != StatusOverdue {
		return Ticket{}, ErrInvalidState
	}
	if work.assignee != contractorID {
		return Ticket{}, ErrPermission
	}
	work.status = StatusCompleted
	work.completedAt = now
	s.completion.remove(work.id)
	delete(worker.active, work.id)
	worker.lastCompletedAt = now
	s.addEvent(now, work, EventCompleted, worker.id, 0, 0)
	return s.snapshotTicket(work), nil
}

func (s *Service) Cancel(now int, ticketID, tenantID int64) (Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || ticketID <= 0 || tenantID <= 0 {
		return Ticket{}, ErrInvalidArgument
	}
	if s.clockSeen && now < s.lastNow {
		return Ticket{}, ErrClockBack
	}
	work, exists := s.tickets[ticketID]
	if !exists {
		return Ticket{}, ErrNotFound
	}
	if work.status == StatusConfirmed || work.status == StatusOverdue || work.status == StatusCompleted || work.status == StatusCanceled {
		return Ticket{}, ErrInvalidState
	}
	if work.tenantID != tenantID {
		return Ticket{}, ErrPermission
	}
	if err := s.begin(now); err != nil {
		return Ticket{}, err
	}
	worker := s.contractors[work.assignee]
	if work.status == StatusAssigned {
		if worker != nil {
			delete(worker.active, work.id)
		}
		s.response.remove(work.id)
	}
	if work.status == StatusQueued {
		s.queue.remove(work)
	}
	work.status = StatusCanceled
	work.assignee = 0
	work.dispatchedAt = 0
	work.responseDue = 0
	s.addEvent(now, work, EventCanceled, 0, 0, 0)
	return s.snapshotTicket(work), nil
}

func (s *Service) DeactivateContractor(now int, contractorID int64) (Contractor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || contractorID <= 0 {
		return Contractor{}, ErrInvalidArgument
	}
	if s.clockSeen && now < s.lastNow {
		return Contractor{}, ErrClockBack
	}
	worker, exists := s.contractors[contractorID]
	if !exists {
		return Contractor{}, ErrNotFound
	}
	if worker.inactive {
		return Contractor{}, ErrInvalidState
	}
	if err := s.begin(now); err != nil {
		return Contractor{}, err
	}
	worker.inactive = true
	requeued := make([]*ticket, 0)
	for _, work := range worker.active {
		if work.status == StatusAssigned {
			requeued = append(requeued, work)
		}
	}
	for _, work := range requeued {
		delete(worker.active, work.id)
		s.response.remove(work.id)
		work.status = StatusQueued
		work.assignee = 0
		work.dispatchedAt = 0
		work.responseDue = 0
		s.addEvent(now, work, EventRequeued, worker.id, 0, 0)
		s.queue.push(work)
	}
	return snapshotContractor(worker), nil
}

func (s *Service) TicketAt(now int, ticketID int64) (Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || ticketID <= 0 {
		return Ticket{}, ErrInvalidArgument
	}
	if s.clockSeen && now < s.lastNow {
		return Ticket{}, ErrClockBack
	}
	work, exists := s.tickets[ticketID]
	if !exists {
		return Ticket{}, ErrNotFound
	}
	if err := s.begin(now); err != nil {
		return Ticket{}, err
	}
	return s.snapshotTicket(work), nil
}

func (s *Service) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Event, len(s.events))
	for i := range s.events {
		result[i] = snapshotEvent(s.events[i])
	}
	return result
}

func (s *Service) parties(ticketID, contractorID int64) (*ticket, *contractor, error) {
	work, workExists := s.tickets[ticketID]
	worker, workerExists := s.contractors[contractorID]
	if !workExists || !workerExists {
		return nil, nil, ErrNotFound
	}
	return work, worker, nil
}

type dueSnapshot struct {
	tickets    map[int64]ticket
	active     map[int64]map[int64]bool
	queued     map[int64]bool
	response   []timerEntry
	completion []timerEntry
	events     []event
	lastNow    int
	clockSeen  bool
}

func (s *Service) snapshotDueState(now int) dueSnapshot {
	state := dueSnapshot{
		tickets:    make(map[int64]ticket),
		active:     make(map[int64]map[int64]bool),
		queued:     make(map[int64]bool),
		response:   append([]timerEntry(nil), s.response.entries...),
		completion: append([]timerEntry(nil), s.completion.entries...),
		events:     append([]event(nil), s.events...),
		lastNow:    s.lastNow,
		clockSeen:  s.clockSeen,
	}
	for _, entry := range s.response.entries {
		if entry.due <= now {
			work := s.tickets[entry.ticketID]
			if work != nil {
				copied := *work
				copied.rejectedBy = make(map[int64]bool, len(work.rejectedBy))
				for rejectedID := range work.rejectedBy {
					copied.rejectedBy[rejectedID] = true
				}
				state.tickets[work.id] = copied
			}
		}
	}
	for _, entry := range s.completion.entries {
		if entry.due <= now {
			work := s.tickets[entry.ticketID]
			if work != nil {
				copied := *work
				copied.rejectedBy = make(map[int64]bool, len(work.rejectedBy))
				for rejectedID := range work.rejectedBy {
					copied.rejectedBy[rejectedID] = true
				}
				state.tickets[work.id] = copied
			}
		}
	}
	for _, work := range s.queue.tickets {
		state.queued[work.id] = true
		if _, exists := state.tickets[work.id]; !exists {
			copied := *work
			copied.rejectedBy = make(map[int64]bool, len(work.rejectedBy))
			for rejectedID := range work.rejectedBy {
				copied.rejectedBy[rejectedID] = true
			}
			state.tickets[work.id] = copied
		}
	}
	for id := range state.tickets {
		work := s.tickets[id]
		if worker := s.contractors[work.assignee]; worker != nil {
			state.active[worker.id] = make(map[int64]bool, len(worker.active))
			for activeID := range worker.active {
				state.active[worker.id][activeID] = true
			}
		}
	}
	return state
}

func (state dueSnapshot) restore(s *Service) {
	for id, saved := range state.tickets {
		work := s.tickets[id]
		*work = saved
	}
	for workerID, active := range state.active {
		worker := s.contractors[workerID]
		worker.active = make(map[int64]*ticket, len(active))
		for ticketID := range active {
			worker.active[ticketID] = s.tickets[ticketID]
		}
	}
	s.queue = newPriorityQueue()
	for ticketID := range state.queued {
		s.queue.push(s.tickets[ticketID])
	}
	s.response = &timerHeap{entries: append([]timerEntry(nil), state.response...)}
	s.completion = &timerHeap{entries: append([]timerEntry(nil), state.completion...)}
	s.events = append([]event(nil), state.events...)
	s.lastNow = state.lastNow
	s.clockSeen = state.clockSeen
}

func (s *Service) begin(now int) error {
	if s.clockSeen && now < s.lastNow {
		return ErrClockBack
	}
	s.clockSeen = true
	s.lastNow = now
	s.advance(now)
	return nil
}

func (s *Service) addEvent(now int, work *ticket, kind EventKind, contractorID int64, fromLevel, toLevel Level) {
	s.events = append(s.events, event{
		at:           now,
		ticketID:     work.id,
		contractorID: contractorID,
		kind:         kind,
		fromLevel:    fromLevel,
		toLevel:      toLevel,
	})
}

func (s *Service) advance(now int) {
	for {
		entry, ok := s.response.peek()
		if !ok || entry.due > now {
			break
		}
		s.response.pop()
		work := s.tickets[entry.ticketID]
		if work == nil || work.status != StatusAssigned || entry.due != work.responseDue+1 {
			continue
		}
		s.addEvent(entry.due, work, EventResponseOverdue, work.assignee, 0, 0)
		s.requeueRejected(work, entry.due, work.assignee, EventRejected)
	}
	for {
		entry, ok := s.completion.peek()
		if !ok || entry.due > now {
			return
		}
		s.completion.pop()
		work := s.tickets[entry.ticketID]
		if work == nil || work.status != StatusConfirmed || entry.due != work.completeDue+1 {
			continue
		}
		work.status = StatusOverdue
		s.addEvent(entry.due, work, EventCompletionOverdue, work.assignee, 0, 0)
	}
}

func (s *Service) requeueRejected(work *ticket, now int, workerID int64, kind EventKind) {
	if worker := s.contractors[workerID]; worker != nil {
		delete(worker.active, work.id)
	}
	work.status = StatusQueued
	work.assignee = 0
	work.dispatchedAt = 0
	work.responseDue = 0
	if !work.rejectedBy[workerID] {
		work.rejectedBy[workerID] = true
		work.rejections++
	}
	s.addEvent(now, work, kind, workerID, 0, 0)
	fromLevel := work.level
	if work.level != Urgent && work.rejections >= s.config.RejectLimit {
		work.level++
		work.levelStartedAt = now
		work.rejections = 0
		s.addEvent(now, work, EventUpgraded, workerID, fromLevel, work.level)
	}
	s.queue.push(work)
}

func (s *Service) assign(work *ticket, now int) bool {
	worker := s.chooseAvailable(work)
	var victim *ticket
	preempted := false
	if worker == nil && work.level == Urgent {
		choice := s.choosePreemption(work)
		if choice.contractor != nil {
			worker = choice.contractor
			victim = choice.victim
			preempted = true
		}
	}
	if worker == nil {
		return false
	}
	if preempted {
		delete(worker.active, victim.id)
		s.response.remove(victim.id)
		victim.status = StatusQueued
		victim.assignee = 0
		victim.dispatchedAt = 0
		victim.responseDue = 0
		s.addEvent(now, victim, EventPreempted, worker.id, 0, 0)
		s.queue.push(victim)
	}
	work.status = StatusAssigned
	work.assignee = worker.id
	work.dispatchedAt = now
	work.responseDue = now + s.config.Limits[work.level].Response
	worker.active[work.id] = work
	s.response.push(timerEntry{ticketID: work.id, due: work.responseDue + 1})
	s.addEvent(now, work, EventAssigned, worker.id, 0, 0)
	return true
}

func (s *Service) chooseAvailable(target *ticket) *contractor {
	return chooseCandidate(target, s.contractors)
}

func (s *Service) choosePreemption(target *ticket) preemption {
	return choosePreemption(target, s.contractors)
}
