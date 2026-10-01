package elevator

// Package elevator implements a deterministic single-car scan scheduler.

import (
	"errors"
	"sync"
)

// Direction is the movement direction selected for the next discrete step.
type Direction int

const (
	// Idle means there is no remaining target and the car does not move.
	Idle Direction = iota
	// Up means the car moves one floor upward on this tick.
	Up
	// Down means the car moves one floor downward on this tick.
	Down
)

// PassengerStatus identifies whether a passenger is waiting, in the car, or delivered.
type PassengerStatus int

const (
	// Waiting means an accepted call has not boarded yet.
	Waiting PassengerStatus = iota
	// InCar means the passenger has boarded and has not alighted yet.
	InCar
	// Delivered means the passenger alighted at the destination floor.
	Delivered
)

// PassengerInfo is an immutable snapshot returned by Passenger.
type PassengerInfo struct {
	ID          int
	From        int
	To          int
	Status      PassengerStatus
	BoardedTick int
	ExitedTick  int
}

// TickResult is the complete observable record of one Tick.
type TickResult struct {
	BeforeFloor int
	Alighted    []int
	Boarded     []int
	Direction   Direction
	AfterFloor  int
}

// Elevator is a thread-safe single elevator with fixed floors and capacity.
type Elevator struct {
	mu         sync.Mutex
	floors     int
	capacity   int
	floor      int
	direction  Direction
	step       int
	passengers map[int]*passenger
	waiting    []int
	car        []int
}

type passenger struct {
	id          int
	from        int
	to          int
	status      PassengerStatus
	boardedTick int
	exitedTick  int
}

var (
	// ErrInvalidFloors is returned when fewer than two floors are configured.
	ErrInvalidFloors = errors.New("floors must be at least 2")
	// ErrInvalidCapacity is returned when capacity is below one.
	ErrInvalidCapacity = errors.New("capacity must be at least 1")
	// ErrInvalidID is returned for passenger IDs below one.
	ErrInvalidID = errors.New("passenger id must be at least 1")
	// ErrInvalidFloor is returned when a floor is outside 1..floors.
	ErrInvalidFloor = errors.New("from and to must be between 1 and the number of floors")
	// ErrSameFloor is returned when origin and destination are equal.
	ErrSameFloor = errors.New("from and to must be different")
	// ErrDuplicateCall is returned for an ID that was accepted earlier.
	ErrDuplicateCall = errors.New("passenger id already exists")
	// ErrPassengerMissing is returned by Passenger for an unknown ID.
	ErrPassengerMissing = errors.New("passenger does not exist")
)

// New creates an empty car on floor 1 with Idle direction.
func New(floors, capacity int) (*Elevator, error) {
	if floors < 2 {
		return nil, ErrInvalidFloors
	}
	if capacity < 1 {
		return nil, ErrInvalidCapacity
	}
	return &Elevator{
		floors:     floors,
		capacity:   capacity,
		floor:      1,
		direction:  Idle,
		passengers: make(map[int]*passenger),
	}, nil
}

// Call validates and accepts a passenger request, preserving acceptance order.
func (e *Elevator) Call(id, from, to int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if id < 1 {
		return ErrInvalidID
	}
	if from < 1 || from > e.floors || to < 1 || to > e.floors {
		return ErrInvalidFloor
	}
	if from == to {
		return ErrSameFloor
	}
	if _, exists := e.passengers[id]; exists {
		return ErrDuplicateCall
	}

	e.passengers[id] = &passenger{
		id:     id,
		from:   from,
		to:     to,
		status: Waiting,
	}
	e.waiting = append(e.waiting, id)
	return nil
}

// Tick performs unload, boarding, direction selection, and one-floor movement.
func (e *Elevator) Tick() TickResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.step++
	before := e.floor

	alighted := make([]int, 0)
	remainingCar := make([]int, 0, len(e.car))
	for _, id := range e.car {
		p := e.passengers[id]
		if p.to == e.floor {
			p.status = Delivered
			p.exitedTick = e.step
			alighted = append(alighted, id)
		} else {
			remainingCar = append(remainingCar, id)
		}
	}
	e.car = remainingCar

	boarded := make([]int, 0)
	remainingWaiting := make([]int, 0, len(e.waiting))
	for _, id := range e.waiting {
		p := e.passengers[id]
		if p.from == e.floor && len(e.car) < e.capacity {
			p.status = InCar
			p.boardedTick = e.step
			e.car = append(e.car, id)
			boarded = append(boarded, id)
		} else {
			remainingWaiting = append(remainingWaiting, id)
		}
	}
	e.waiting = remainingWaiting

	targets := e.targets()
	if len(targets) == 0 {
		e.direction = Idle
	} else {
		e.direction = e.chooseDirection(targets)
		switch e.direction {
		case Up:
			e.floor++
		case Down:
			e.floor--
		}
	}

	return TickResult{
		BeforeFloor: before,
		Alighted:    alighted,
		Boarded:     boarded,
		Direction:   e.direction,
		AfterFloor:  e.floor,
	}
}

// Passenger returns a snapshot of one passenger's lifecycle state.
func (e *Elevator) Passenger(id int) (PassengerInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	p, exists := e.passengers[id]
	if !exists {
		return PassengerInfo{}, ErrPassengerMissing
	}
	return PassengerInfo{
		ID:          p.id,
		From:        p.from,
		To:          p.to,
		Status:      p.status,
		BoardedTick: p.boardedTick,
		ExitedTick:  p.exitedTick,
	}, nil
}

func (e *Elevator) targets() map[int]struct{} {
	targets := make(map[int]struct{})
	for _, id := range e.car {
		target := e.passengers[id].to
		if target != e.floor {
			targets[target] = struct{}{}
		}
	}
	for _, id := range e.waiting {
		target := e.passengers[id].from
		if target != e.floor {
			targets[target] = struct{}{}
		}
	}
	return targets
}

func (e *Elevator) chooseDirection(targets map[int]struct{}) Direction {
	if e.direction == Up {
		for target := range targets {
			if target > e.floor {
				return Up
			}
		}
	}
	if e.direction == Down {
		for target := range targets {
			if target < e.floor {
				return Down
			}
		}
	}

	best := 0
	bestDistance := 0
	for target := range targets {
		distance := target - e.floor
		if distance < 0 {
			distance = -distance
		}
		if best == 0 || distance < bestDistance || (distance == bestDistance && target > e.floor) {
			best = target
			bestDistance = distance
		}
	}
	if best > e.floor {
		return Up
	}
	return Down
}

// String returns the direction's textual name.
func (d Direction) String() string {
	switch d {
	case Up:
		return "Up"
	case Down:
		return "Down"
	default:
		return "Idle"
	}
}
