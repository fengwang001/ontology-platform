package ontology

import (
	"container/heap"
	"sync"
)

const MinutesPerDay int64 = 1440

type Config struct {
	AdvanceMin      int64
	MaxAdvanceMin   int64
	CheckInLeadMin  int64
	MaxWorkDays     int64
	ComplaintLimit  int
	ReapplyWaitDays int64
	RefundDays      int64
	Penalty         int64
}

func (c Config) valid() bool {
	return c.AdvanceMin >= 0 && c.MaxAdvanceMin >= c.AdvanceMin &&
		c.CheckInLeadMin >= 0 && c.MaxWorkDays > 0 && c.ComplaintLimit > 0 &&
		c.ReapplyWaitDays >= 0 && c.RefundDays >= 0 && c.Penalty > 0
}

type MoveKind string

const (
	MoveIn  MoveKind = "in"
	MoveOut MoveKind = "out"
)

type BookingStatus string

const (
	BookingReserved BookingStatus = "reserved"
	BookingActive   BookingStatus = "active"
	BookingDone     BookingStatus = "done"
	BookingCanceled BookingStatus = "canceled"
	BookingNoShow   BookingStatus = "no_show"
)

type PermitStatus string

const (
	PermitApplied   PermitStatus = "applied"
	PermitApproved  PermitStatus = "approved"
	PermitActive    PermitStatus = "active"
	PermitSuspended PermitStatus = "suspended"
	PermitRejected  PermitStatus = "rejected"
	PermitRevoked   PermitStatus = "revoked"
	PermitExpired   PermitStatus = "expired"
	PermitRefunded  PermitStatus = "refunded"
)

type Service struct {
	mu              sync.Mutex
	cfg             Config
	clock           int64
	clockSet        bool
	households      map[string]struct{}
	elevators       map[string]struct{}
	bookings        map[string]*booking
	permits         map[string]*permit
	slots           map[slotKey]*booking
	dayBookings     map[householdDay]*booking
	elevatorActive  map[string]*booking
	householdPermit map[string]*permit
	holds           map[string]int
	ledgers         map[string][]LedgerEntry
	quietRanges     []MinuteRange
	holidays        map[int64]struct{}
	bookingEnds     endHeap
	permitEnds      endHeap
	nextSeq         int64
}

func NewService(cfg Config) (*Service, error) {
	if !cfg.valid() {
		return nil, illegal("invalid service configuration")
	}
	service := &Service{
		cfg:             cfg,
		households:      make(map[string]struct{}),
		elevators:       make(map[string]struct{}),
		bookings:        make(map[string]*booking),
		permits:         make(map[string]*permit),
		slots:           make(map[slotKey]*booking),
		dayBookings:     make(map[householdDay]*booking),
		elevatorActive:  make(map[string]*booking),
		householdPermit: make(map[string]*permit),
		holds:           make(map[string]int),
		ledgers:         make(map[string][]LedgerEntry),
		holidays:        make(map[int64]struct{}),
	}
	heap.Init(&service.bookingEnds)
	heap.Init(&service.permitEnds)
	return service, nil
}

func (s *Service) checkClock(now int64) error {
	if now < 0 {
		return illegal("now must be non-negative")
	}
	if s.clockSet && now < s.clock {
		return Error{ErrClockRewind, "now is before the previous accepted operation"}
	}
	return nil
}

func (s *Service) commitClock(now int64) {
	s.clock = now
	s.clockSet = true
}

func nonEmptyID(id string) bool { return id != "" }

func (s *Service) newID(prefix string) string {
	s.nextSeq++
	return prefix + "-" + itoa(s.nextSeq)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
