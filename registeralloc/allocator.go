package registeralloc

import (
	"errors"
	"sync"
)

var (
	ErrInvalidRegisterCount = errors.New("registeralloc: register count must be at least 1")
	ErrDuplicateInterval    = errors.New("registeralloc: interval already exists")
	ErrInvalidRange         = errors.New("registeralloc: start must be strictly less than end")
	ErrNonMonotonicStart    = errors.New("registeralloc: start must not be less than the previous successful start")
	ErrIntervalNotFound     = errors.New("registeralloc: interval not found")
)

// PlacementKind identifies where an interval was placed.
type PlacementKind int

const (
	// InRegister means the interval occupies a physical register.
	InRegister PlacementKind = iota
	// Spilled means the interval occupies a sequentially assigned spill slot.
	Spilled
)

// Placement is the immutable result recorded for an interval.
type Placement struct {
	Kind      PlacementKind
	Register  int
	SpillSlot int
}

type interval struct {
	start     int
	end       int
	placement Placement
}

// Allocator performs deterministic linear-scan allocation over K registers.
// Its methods are safe for concurrent use.
type Allocator struct {
	mu        sync.RWMutex
	intervals map[int]*interval
	active    map[int]*interval
	free      map[int]struct{}

	lastStart    int
	hasLastStart bool
	spillCount   int
}

// New creates an allocator with physical registers 0 through K-1.
func New(K int) (*Allocator, error) {
	if K < 1 {
		return nil, ErrInvalidRegisterCount
	}

	free := make(map[int]struct{}, K)
	for register := 0; register < K; register++ {
		free[register] = struct{}{}
	}

	return &Allocator{
		intervals: make(map[int]*interval),
		active:    make(map[int]*interval),
		free:      free,
	}, nil
}

// Add inserts the half-open interval [start, end).
// Successful calls must provide starts in nondecreasing order.
func (a *Allocator) Add(id, start, end int) (Placement, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.intervals[id]; exists {
		return Placement{}, ErrDuplicateInterval
	}
	if start >= end {
		return Placement{}, ErrInvalidRange
	}
	if a.hasLastStart && start < a.lastStart {
		return Placement{}, ErrNonMonotonicStart
	}

	for activeID, active := range a.active {
		if active.end <= start {
			a.free[active.placement.Register] = struct{}{}
			delete(a.active, activeID)
		}
	}

	next := &interval{
		start: start,
		end:   end,
	}

	if register, found := smallestFreeRegister(a.free); found {
		delete(a.free, register)
		next.placement = registerPlacement(register)
	} else {
		victimID := id
		victimEnd := end

		for activeID, active := range a.active {
			if active.end > victimEnd {
				victimID = activeID
				victimEnd = active.end
				continue
			}
			if active.end == victimEnd && victimID != id && activeID < victimID {
				victimID = activeID
			}
		}

		if victimID == id {
			next.placement = spillPlacement(a.spillCount)
			a.spillCount++
		} else {
			victim := a.active[victimID]
			register := victim.placement.Register
			victim.placement = spillPlacement(a.spillCount)
			a.spillCount++
			delete(a.active, victimID)

			next.placement = registerPlacement(register)
		}
	}

	a.intervals[id] = next
	if next.placement.Kind == InRegister {
		a.active[id] = next
	}
	a.lastStart = start
	a.hasLastStart = true

	return next.placement, nil
}

// Query returns the current register or spill slot assigned to an interval.
func (a *Allocator) Query(id int) (Placement, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	current, exists := a.intervals[id]
	if !exists {
		return Placement{}, ErrIntervalNotFound
	}
	return current.placement, nil
}

func smallestFreeRegister(free map[int]struct{}) (int, bool) {
	register := -1
	for candidate := range free {
		if register == -1 || candidate < register {
			register = candidate
		}
	}
	if register == -1 {
		return 0, false
	}
	return register, true
}

func registerPlacement(register int) Placement {
	return Placement{Kind: InRegister, Register: register}
}

func spillPlacement(slot int) Placement {
	return Placement{Kind: Spilled, SpillSlot: slot}
}
