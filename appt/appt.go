package appt

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRollback   = errors.New("clock rollback")
	ErrState           = errors.New("state conflict")
	ErrConflict        = errors.New("conflict")
	ErrCapacity        = errors.New("capacity exhausted")
)

type Kind uint8

const (
	Dry Kind = iota
	Reefer
)

type Appointment struct {
	Truck []byte
	Kind  Kind
	Start int64
}

type Booker struct {
	mu       sync.RWMutex
	window   int64
	early    int64
	late     int64
	maxSeats int64
	maxNow   int64
	records  map[string]*record
	counts   map[slot]int64
}

type record struct {
	truck []byte
	kind  Kind
	start int64
	state reservationState
}

type reservationState uint8

const (
	reservationActive reservationState = iota
	reservationConsumed
	reservationVoid
)

type slot struct {
	start int64
	kind  Kind
}

func NewBooker(windowLength, earlyTolerance, lateGrace, maxSeats int64) (*Booker, error) {
	if !bounded(windowLength, 1, 100000) ||
		!bounded(earlyTolerance, 1, 100000) ||
		!bounded(lateGrace, 1, 100000) ||
		!bounded(maxSeats, 1, 1000) {
		return nil, ErrInvalidArgument
	}
	return &Booker{
		window:   windowLength,
		early:    earlyTolerance,
		late:     lateGrace,
		maxSeats: maxSeats,
		records:  make(map[string]*record),
		counts:   make(map[slot]int64),
	}, nil
}

func bounded(value, low, high int64) bool {
	return value >= low && value <= high
}

func validTruck(truck []byte) bool {
	return len(truck) >= 1 && len(truck) <= 32
}

func validKind(kind Kind) bool {
	return kind == Dry || kind == Reefer
}

func (b *Booker) Book(truck []byte, kind Kind, start, now int64) error {
	if !validTruck(truck) || !validKind(kind) || !bounded(now, 0, 1000000000) ||
		start < 0 || start%b.window != 0 || start < now {
		return ErrInvalidArgument
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.maxNow {
		return ErrClockRollback
	}
	key := string(truck)
	if existing := b.records[key]; existing != nil {
		return ErrConflict
	}
	slotKey := slot{start: start, kind: kind}
	if b.counts[slotKey] >= b.maxSeats {
		return ErrCapacity
	}

	b.counts[slotKey]++
	b.records[key] = &record{
		truck: append([]byte(nil), truck...),
		kind:  kind,
		start: start,
		state: reservationActive,
	}
	b.maxNow = now
	return nil
}

func (b *Booker) Advance(now int64) error {
	if !bounded(now, 0, 1000000000) {
		return ErrInvalidArgument
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.maxNow {
		return ErrClockRollback
	}
	b.maxNow = now
	return nil
}

func (b *Booker) UseAtCheckIn(truck []byte, kind Kind, now int64) (Appointment, bool, bool, error) {
	if !validTruck(truck) || !validKind(kind) || !bounded(now, 0, 1000000000) {
		return Appointment{}, false, false, ErrInvalidArgument
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.maxNow {
		return Appointment{}, false, false, ErrClockRollback
	}
	current := b.records[string(truck)]
	if current == nil {
		return Appointment{}, false, false, nil
	}
	if current.kind != kind {
		return Appointment{}, false, false, ErrState
	}

	appointment := Appointment{
		Truck: current.truck,
		Kind:  current.kind,
		Start: current.start,
	}
	if current.state != reservationActive {
		return appointment, true, false, nil
	}

	onTime := current.start-now <= b.early && now-current.start <= b.late
	if onTime {
		current.state = reservationConsumed
	} else {
		current.state = reservationVoid
	}
	return Appointment{
		Truck: current.truck,
		Kind:  current.kind,
		Start: current.start,
	}, true, onTime, nil
}
