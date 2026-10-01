package elevator

import (
	"errors"
	"sync"
)

type Direction int

const (
	Idle Direction = iota
	Up
	Down
)

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

type CallErrorReason int

const (
	InvalidCallID CallErrorReason = iota
	InvalidFloor
	SameFloor
	DuplicateCallID
)

func (r CallErrorReason) String() string {
	switch r {
	case InvalidCallID:
		return "id must be at least 1"
	case InvalidFloor:
		return "from and to must be between 1 and F"
	case SameFloor:
		return "from and to must differ"
	case DuplicateCallID:
		return "passenger id already exists"
	default:
		return "invalid call"
	}
}

type QueryErrorReason int

const (
	PassengerNotFound QueryErrorReason = iota
)

func (r QueryErrorReason) String() string {
	switch r {
	case PassengerNotFound:
		return "passenger not found"
	default:
		return "passenger query failed"
	}
}

type CallError struct {
	Reason CallErrorReason
}

func (e *CallError) Error() string { return e.Reason.String() }

type QueryError struct {
	Reason QueryErrorReason
}

func (e *QueryError) Error() string { return e.Reason.String() }

type PassengerStatus int

const (
	Waiting PassengerStatus = iota
	InCar
	Delivered
)

type PassengerInfo struct {
	Status      PassengerStatus
	BoardTick   int
	DeliverTick int
}

type TickResult struct {
	BeforeFloor  int
	Exited       []int
	Boarded      []int
	Direction    Direction
	CurrentFloor int
}

type passenger struct {
	id          int
	from        int
	to          int
	status      PassengerStatus
	boardTick   int
	deliverTick int
}

type Elevator struct {
	mu         sync.Mutex
	floors     int
	capacity   int
	floor      int
	direction  Direction
	tick       int
	waiting    []*passenger
	car        []*passenger
	passengers map[int]*passenger
}

func NewElevator(floors int, capacity int) (*Elevator, error) {
	if floors < 2 || capacity < 1 {
		return nil, errors.New("floors must be at least 2 and capacity must be at least 1")
	}

	return &Elevator{
		floors:     floors,
		capacity:   capacity,
		floor:      1,
		direction:  Idle,
		passengers: make(map[int]*passenger),
	}, nil
}

func (e *Elevator) Call(id, from, to int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if id < 1 {
		return &CallError{Reason: InvalidCallID}
	}
	if from < 1 || from > e.floors || to < 1 || to > e.floors {
		return &CallError{Reason: InvalidFloor}
	}
	if from == to {
		return &CallError{Reason: SameFloor}
	}
	if _, exists := e.passengers[id]; exists {
		return &CallError{Reason: DuplicateCallID}
	}

	p := &passenger{
		id:     id,
		from:   from,
		to:     to,
		status: Waiting,
	}
	e.passengers[id] = p
	e.waiting = append(e.waiting, p)
	return nil
}

func (e *Elevator) Tick() TickResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.tick++
	before := e.floor

	exited := make([]int, 0)
	remainingCar := e.car[:0]
	for _, p := range e.car {
		if p.to == e.floor {
			p.status = Delivered
			p.deliverTick = e.tick
			exited = append(exited, p.id)
		} else {
			remainingCar = append(remainingCar, p)
		}
	}
	e.car = remainingCar

	boarded := make([]int, 0)
	remainingWaiting := e.waiting[:0]
	for _, p := range e.waiting {
		if p.from == e.floor && len(e.car) < e.capacity {
			p.status = InCar
			p.boardTick = e.tick
			e.car = append(e.car, p)
			boarded = append(boarded, p.id)
		} else {
			remainingWaiting = append(remainingWaiting, p)
		}
	}
	e.waiting = remainingWaiting

	targets := make(map[int]struct{}, len(e.car)+len(e.waiting))
	for _, p := range e.car {
		if p.to != e.floor {
			targets[p.to] = struct{}{}
		}
	}
	for _, p := range e.waiting {
		if p.from != e.floor {
			targets[p.from] = struct{}{}
		}
	}

	if len(targets) == 0 {
		e.direction = Idle
	} else {
		keepUp := e.direction == Up && hasFloorAbove(targets, e.floor)
		keepDown := e.direction == Down && hasFloorBelow(targets, e.floor)
		switch {
		case keepUp:
			e.direction = Up
		case keepDown:
			e.direction = Down
		default:
			e.direction = nearestDirection(targets, e.floor)
		}

		switch e.direction {
		case Up:
			e.floor++
		case Down:
			e.floor--
		}
	}

	return TickResult{
		BeforeFloor:  before,
		Exited:       exited,
		Boarded:      boarded,
		Direction:    e.direction,
		CurrentFloor: e.floor,
	}
}

func (e *Elevator) Passenger(id int) (PassengerInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	p, exists := e.passengers[id]
	if !exists {
		return PassengerInfo{}, &QueryError{Reason: PassengerNotFound}
	}

	return PassengerInfo{
		Status:      p.status,
		BoardTick:   p.boardTick,
		DeliverTick: p.deliverTick,
	}, nil
}

func hasFloorAbove(targets map[int]struct{}, floor int) bool {
	for target := range targets {
		if target > floor {
			return true
		}
	}
	return false
}

func hasFloorBelow(targets map[int]struct{}, floor int) bool {
	for target := range targets {
		if target < floor {
			return true
		}
	}
	return false
}

func nearestDirection(targets map[int]struct{}, floor int) Direction {
	nearest := 0
	nearestDistance := 0
	for target := range targets {
		distance := abs(target - floor)
		if nearest == 0 || distance < nearestDistance || (distance == nearestDistance && target > floor) {
			nearest = target
			nearestDistance = distance
		}
	}
	if nearest > floor {
		return Up
	}
	return Down
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
