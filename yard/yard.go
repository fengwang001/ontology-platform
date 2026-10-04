package yard

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/appt"
	"ontology/dock"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRollback   = errors.New("clock rollback")
	ErrNotFound        = errors.New("not found")
	ErrState           = errors.New("state conflict")
	ErrConflict        = errors.New("conflict")
	ErrCapacity        = errors.New("capacity exhausted")
)

type Kind uint8

const (
	Dry Kind = iota
	Reefer
)

type Assignment struct {
	Truck []byte
	Dock  []byte
}

type Scheduler struct {
	mu            sync.Mutex
	booker        *appt.Booker
	docks         *dock.Pool
	maxNow        int64
	early         int64
	late          int64
	waitMax       int64
	window        int64
	maxSeats      int64
	nextSeq       int64
	looking       int
	waitingReefer int
	vehicles      map[string]*vehicle
	onTime        [2]waitingHeap
	promoted      [2]vehicleQueue
	standby       [2]vehicleQueue
}

type vehicleState uint8

const (
	stateUnknown vehicleState = iota
	stateWaiting
	stateAssigned
	stateDeparted
)

type vehicle struct {
	truck   []byte
	kind    Kind
	state   vehicleState
	seq     int64
	checkin int64
	start   int64
}

type waitingItem struct {
	truck []byte
	start int64
	seq   int64
}

type waitingHeap []waitingItem

func (h waitingHeap) Len() int { return len(h) }

func (h waitingHeap) Less(i, j int) bool {
	if h[i].start != h[j].start {
		return h[i].start < h[j].start
	}
	return h[i].seq < h[j].seq
}

func (h waitingHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *waitingHeap) Push(value any) {
	*h = append(*h, value.(waitingItem))
}

func (h *waitingHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

type vehicleQueue []*vehicle

func NewScheduler(windowLength, earlyTolerance, lateGrace, standbyWait, maxSeats int64) (*Scheduler, error) {
	booker, err := appt.NewBooker(windowLength, earlyTolerance, lateGrace, maxSeats)
	if err != nil {
		return nil, mapError(err)
	}
	return &Scheduler{
		booker:   booker,
		docks:    dock.NewPool(),
		early:    earlyTolerance,
		late:     lateGrace,
		waitMax:  standbyWait,
		window:   windowLength,
		maxSeats: maxSeats,
		vehicles: make(map[string]*vehicle),
	}, nil
}

func (s *Scheduler) Book(truck []byte, kind Kind, start, now int64) ([]Assignment, error) {
	if !validID(truck) || !validKind(kind) || !bounded(now, 0, 1_000_000_000) {
		return nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	if err := mapError(s.booker.Book(truck, toApptKind(kind), start, now)); err != nil {
		return nil, err
	}
	if err := mapError(s.booker.Advance(now)); err != nil {
		return nil, err
	}
	s.maxNow = now
	return nil, nil
}

func (s *Scheduler) AddDock(dockID []byte, kind Kind, now int64) ([]Assignment, error) {
	if !validID(dockID) || !validKind(kind) || !bounded(now, 0, 1_000_000_000) {
		return nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	if err := mapError(s.docks.AddDock(dockID, toDockKind(kind), now)); err != nil {
		return nil, err
	}
	if err := mapError(s.booker.Advance(now)); err != nil {
		return nil, err
	}
	s.maxNow = now
	return s.dispatch(now), nil
}

func (s *Scheduler) CheckIn(truck []byte, kind Kind, now int64) ([]Assignment, error) {
	if !validID(truck) || !validKind(kind) || !bounded(now, 0, 1_000_000_000) {
		return nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	if s.vehicles[string(truck)] != nil {
		return nil, ErrState
	}

	if err := mapError(s.booker.Advance(now)); err != nil {
		return nil, err
	}
	appointment, hasAppointment, onTime, err := s.booker.UseAtCheckIn(truck, toApptKind(kind), now)
	if err != nil {
		return nil, mapError(err)
	}
	current := &vehicle{
		truck:   append([]byte(nil), truck...),
		kind:    kind,
		state:   stateWaiting,
		seq:     s.nextSeq,
		checkin: now,
	}
	s.nextSeq++
	if hasAppointment && onTime {
		current.start = appointment.Start
		heap.Push(&s.onTime[kind], waitingItem{
			truck: current.truck,
			start: current.start,
			seq:   current.seq,
		})
	} else {
		s.standby[kind] = append(s.standby[kind], current)
	}
	if kind == Reefer {
		s.waitingReefer++
	}
	s.vehicles[string(truck)] = current
	s.maxNow = now
	return s.dispatch(now), nil
}

func (s *Scheduler) Depart(truck []byte, now int64) ([]Assignment, error) {
	if !validID(truck) || !bounded(now, 0, 1_000_000_000) {
		return nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	current := s.vehicles[string(truck)]
	if current == nil {
		return nil, ErrNotFound
	}
	if current.state != stateAssigned {
		return nil, ErrState
	}
	if _, _, err := s.docks.Release(truck, now); err != nil {
		return nil, mapError(err)
	}
	if err := mapError(s.booker.Advance(now)); err != nil {
		return nil, err
	}
	current.state = stateDeparted
	s.maxNow = now
	return s.dispatch(now), nil
}

func (s *Scheduler) dispatch(now int64) []Assignment {
	s.looking = 0
	result := make([]Assignment, 0)
	for {
		s.promoteStandby(now)
		candidate, ok := s.nextAssignment()
		if !ok {
			break
		}
		result = append(result, candidate)
	}
	return result
}

func (s *Scheduler) nextAssignment() (Assignment, bool) {
	freeDry := s.docks.HasFree(toDockKind(Dry))
	freeReefer := s.docks.HasFree(toDockKind(Reefer))

	switch {
	case !freeDry && !freeReefer:
		return Assignment{}, false
	case freeDry && freeReefer:
		return s.assignBestForResources(true, true)
	case freeDry:
		return s.assignBestForResources(true, false)
	default:
		return s.assignBestForResources(false, true)
	}
}

func (s *Scheduler) assignBestForResources(freeDry, freeReefer bool) (Assignment, bool) {
	for tier := 0; tier < 3; tier++ {
		if truck, kind, borrow, ok := s.bestInTier(tier, freeDry, freeReefer); ok {
			return s.assign(truck, kind, borrow)
		}
	}
	return Assignment{}, false
}

func (s *Scheduler) bestInTier(tier int, freeDry, freeReefer bool) ([]byte, Kind, bool, bool) {
	allowDry := freeDry || (freeReefer && s.waitingReefer == 0)
	allowReefer := freeReefer
	if tier == 1 {
		var dryTruck []byte
		var reeferTruck []byte
		dryOK := false
		reeferOK := false
		if allowDry {
			dryTruck, dryOK = s.peekOnTime(Dry)
		}
		if allowReefer {
			reeferTruck, reeferOK = s.peekOnTime(Reefer)
		}
		if dryOK || reeferOK {
			if !reeferOK {
				return dryTruck, Dry, !freeDry, true
			}
			if !dryOK {
				return reeferTruck, Reefer, false, true
			}
			dryItem := s.onTime[Dry][0]
			reeferItem := s.onTime[Reefer][0]
			if dryItem.start < reeferItem.start ||
				(dryItem.start == reeferItem.start && dryItem.seq < reeferItem.seq) {
				return dryTruck, Dry, !freeDry, true
			}
			return reeferTruck, Reefer, false, true
		}
		return nil, Dry, false, false
	}

	queues := &s.promoted
	if tier == 2 {
		queues = &s.standby
	}
	var dryTruck []byte
	var reeferTruck []byte
	dryOK := false
	reeferOK := false
	if allowDry {
		dryTruck, dryOK = s.peekQueue(&queues[Dry])
	}
	if allowReefer {
		reeferTruck, reeferOK = s.peekQueue(&queues[Reefer])
	}
	if dryOK || reeferOK {
		if !reeferOK {
			return dryTruck, Dry, !freeDry, true
		}
		if !dryOK {
			return reeferTruck, Reefer, false, true
		}
		if queues[Dry][0].seq < queues[Reefer][0].seq {
			return dryTruck, Dry, !freeDry, true
		}
		return reeferTruck, Reefer, false, true
	}
	return nil, Dry, false, false
}

func (s *Scheduler) peekOnTime(kind Kind) ([]byte, bool) {
	for s.onTime[kind].Len() > 0 {
		item := s.onTime[kind][0]
		current := s.vehicles[string(item.truck)]
		if current != nil && current.state == stateWaiting {
			s.looking++
			return current.truck, true
		}
		heap.Pop(&s.onTime[kind])
	}
	return nil, false
}

func (s *Scheduler) peekQueue(queue *vehicleQueue) ([]byte, bool) {
	for len(*queue) > 0 {
		current := (*queue)[0]
		if current.state == stateWaiting {
			s.looking++
			return current.truck, true
		}
		*queue = (*queue)[1:]
	}
	return nil, false
}

func (s *Scheduler) promoteStandby(now int64) {
	for kind := range s.standby {
		for len(s.standby[kind]) > 0 {
			current := s.standby[kind][0]
			if current.state != stateWaiting {
				s.standby[kind] = s.standby[kind][1:]
				continue
			}
			if now-current.checkin < s.waitMax {
				break
			}
			s.standby[kind] = s.standby[kind][1:]
			s.promoted[kind] = append(s.promoted[kind], current)
		}
	}
}

func (s *Scheduler) assign(truck []byte, kind Kind, borrow bool) (Assignment, bool) {
	dockAssignment, ok := s.docks.Assign(truck, toDockKind(kind), borrow)
	if !ok {
		return Assignment{}, false
	}
	current := s.vehicles[string(truck)]
	current.state = stateAssigned
	if kind == Reefer {
		s.waitingReefer--
	}
	return Assignment{
		Truck: append([]byte(nil), truck...),
		Dock:  append([]byte(nil), dockAssignment.Dock...),
	}, true
}

func mapError(err error) error {
	if errors.Is(err, appt.ErrInvalidArgument) || errors.Is(err, dock.ErrInvalidArgument) {
		return ErrInvalidArgument
	}
	if errors.Is(err, appt.ErrClockRollback) || errors.Is(err, dock.ErrClockRollback) {
		return ErrClockRollback
	}
	if errors.Is(err, appt.ErrState) || errors.Is(err, dock.ErrState) {
		return ErrState
	}
	if errors.Is(err, appt.ErrConflict) || errors.Is(err, dock.ErrConflict) {
		return ErrConflict
	}
	if errors.Is(err, appt.ErrCapacity) {
		return ErrCapacity
	}
	return err
}

func (s *Scheduler) checkClock(now int64) error {
	if now < s.maxNow {
		return ErrClockRollback
	}
	return nil
}

func validID(value []byte) bool {
	return len(value) >= 1 && len(value) <= 32
}

func validKind(kind Kind) bool {
	return kind == Dry || kind == Reefer
}

func bounded(value, low, high int64) bool {
	return value >= low && value <= high
}

func toApptKind(kind Kind) appt.Kind {
	if kind == Reefer {
		return appt.Reefer
	}
	return appt.Dry
}

func toDockKind(kind Kind) dock.Kind {
	if kind == Reefer {
		return dock.Reefer
	}
	return dock.Dry
}
