package pagealloc

import (
	"errors"
	"sync"
)

type GFP int

const (
	NORMAL GFP = iota
	ATOMIC
	EMERGENCY
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNoMemory        = errors.New("out of memory")
	ErrWouldReclaim    = errors.New("allocation would reclaim")
	ErrOverfree        = errors.New("free exceeds managed pages")
)

type Marks struct {
	Min    int64
	Low    int64
	High   int64
	Atomic int64
}

type Stats struct {
	Allocations []int64
	Fallbacks   int64
	Wakes       int64
}

type Allocator struct {
	mu       sync.RWMutex
	managed  []int64
	ratios   []int64
	free     []int64
	min      []int64
	low      []int64
	high     []int64
	atomic   []int64
	step     []int64
	cap      []int64
	boost    []int64
	reserve  [][]int64
	kswapd   []bool
	allocs   []int64
	fallback int64
	wakes    int64
	checks   int64
	totalMin int64
}

func New(zones int, managed []int64, ratios []int64, totalMin int64) (*Allocator, error) {
	if zones < 1 || zones > 4 || len(managed) != zones || len(ratios) != zones {
		return nil, ErrInvalidArgument
	}

	var totalManaged int64
	for _, pages := range managed {
		if pages < 1 || pages > 1_000_000_000 {
			return nil, ErrInvalidArgument
		}
		totalManaged += pages
	}
	for _, ratio := range ratios {
		if ratio < 1 || ratio > 1000 {
			return nil, ErrInvalidArgument
		}
	}
	if totalMin < 0 || totalMin > totalManaged {
		return nil, ErrInvalidArgument
	}

	a := &Allocator{
		managed:  append([]int64(nil), managed...),
		ratios:   append([]int64(nil), ratios...),
		free:     append([]int64(nil), managed...),
		reserve:  make([][]int64, zones),
		kswapd:   make([]bool, zones),
		allocs:   make([]int64, zones),
		totalMin: totalMin,
	}
	a.min = make([]int64, zones)
	a.low = make([]int64, zones)
	a.high = make([]int64, zones)
	a.atomic = make([]int64, zones)
	a.step = make([]int64, zones)
	a.cap = make([]int64, zones)
	a.boost = make([]int64, zones)
	for zone := range a.reserve {
		a.reserve[zone] = make([]int64, zones)
	}

	a.recalculateMarks()
	for zone := 0; zone < zones; zone++ {
		for preferred := zone + 1; preferred < zones; preferred++ {
			var retainedManaged int64
			for source := zone + 1; source <= preferred; source++ {
				retainedManaged += a.managed[source]
			}
			a.reserve[zone][preferred] = retainedManaged / a.ratios[zone]
		}
	}
	return a, nil
}

func (a *Allocator) recalculateMarks() {
	var totalManaged int64
	for _, pages := range a.managed {
		totalManaged += pages
	}
	for zone := range a.managed {
		minimum := a.totalMin * a.managed[zone] / totalManaged
		a.min[zone] = minimum
		a.low[zone] = minimum + minimum/4
		a.high[zone] = minimum + minimum/2
		a.atomic[zone] = minimum - minimum/2
		a.step[zone] = maxInt64(1, a.high[zone]/4)
		a.cap[zone] = a.high[zone] / 2
	}
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func (a *Allocator) Alloc(preferred int, pages int64, gfp GFP) (int, error) {
	if preferred < 0 || preferred >= len(a.managed) || pages < 1 || pages > 1_000_000_000 {
		return 0, ErrInvalidArgument
	}
	switch gfp {
	case NORMAL, ATOMIC, EMERGENCY:
	default:
		return 0, ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if gfp == EMERGENCY {
		for zone := preferred; zone >= 0; zone-- {
			a.checks++
			if a.free[zone] >= pages {
				a.completeAllocation(zone, preferred, pages, gfp)
				return zone, nil
			}
		}
		return 0, ErrNoMemory
	}

	for zone := preferred; zone >= 0; zone-- {
		a.checks++
		if a.okLocked(zone, pages, a.low[zone]+a.boost[zone], preferred) {
			a.completeAllocation(zone, preferred, pages, gfp)
			return zone, nil
		}
	}

	if !a.kswapd[preferred] {
		a.kswapd[preferred] = true
		a.wakes++
	}

	secondMark := a.min
	if gfp == ATOMIC {
		secondMark = a.atomic
	}
	for zone := preferred; zone >= 0; zone-- {
		a.checks++
		if a.okLocked(zone, pages, secondMark[zone], preferred) {
			a.completeAllocation(zone, preferred, pages, gfp)
			return zone, nil
		}
	}

	if gfp == ATOMIC {
		return 0, ErrNoMemory
	}
	return 0, ErrWouldReclaim
}

func (a *Allocator) Free(zone int, pages int64) error {
	if zone < 0 || zone >= len(a.managed) || pages < 1 || pages > 1_000_000_000 {
		return ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.free[zone] > a.managed[zone]-pages {
		return ErrOverfree
	}
	a.free[zone] += pages

	for preferred := range a.kswapd {
		if a.kswapd[preferred] && a.free[preferred] > a.high[preferred]+a.boost[preferred] {
			a.kswapd[preferred] = false
			a.boost[preferred] = 0
		}
	}
	return nil
}

func (a *Allocator) SetTotalMin(totalMin int64) error {
	var totalManaged int64
	a.mu.RLock()
	for _, pages := range a.managed {
		totalManaged += pages
	}
	a.mu.RUnlock()
	if totalMin < 0 || totalMin > totalManaged {
		return ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.totalMin = totalMin
	a.recalculateMarks()
	for zone := range a.boost {
		if a.boost[zone] > a.cap[zone] {
			a.boost[zone] = a.cap[zone]
		}
	}
	for zone := range a.kswapd {
		if a.kswapd[zone] && a.free[zone] > a.high[zone]+a.boost[zone] {
			a.kswapd[zone] = false
			a.boost[zone] = 0
		}
	}
	return nil
}

func (a *Allocator) FreePages(zone int) (int64, error) {
	if !a.validZone(zone) {
		return 0, ErrInvalidArgument
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.free[zone], nil
}

func (a *Allocator) Marks(zone int) (Marks, error) {
	if !a.validZone(zone) {
		return Marks{}, ErrInvalidArgument
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return Marks{Min: a.min[zone], Low: a.low[zone], High: a.high[zone], Atomic: a.atomic[zone]}, nil
}

func (a *Allocator) Boost(zone int) (int64, error) {
	if !a.validZone(zone) {
		return 0, ErrInvalidArgument
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.boost[zone], nil
}

func (a *Allocator) Reserve(zone, preferred int) (int64, error) {
	if !a.validZone(zone) || !a.validZone(preferred) {
		return 0, ErrInvalidArgument
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.reserve[zone][preferred], nil
}

func (a *Allocator) Kswapd(zone int) (bool, error) {
	if !a.validZone(zone) {
		return false, ErrInvalidArgument
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.kswapd[zone], nil
}

func (a *Allocator) Stats() Stats {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return Stats{Allocations: append([]int64(nil), a.allocs...), Fallbacks: a.fallback, Wakes: a.wakes}
}

func (a *Allocator) Checks() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.checks
}

func (a *Allocator) validZone(zone int) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return zone >= 0 && zone < len(a.managed)
}

func (a *Allocator) okLocked(zone int, pages int64, mark int64, preferred int) bool {
	return a.free[zone]-pages > mark+a.reserve[zone][preferred]
}

func (a *Allocator) completeAllocation(zone, preferred int, pages int64, gfp GFP) {
	a.free[zone] -= pages
	a.allocs[zone]++
	if zone != preferred {
		a.fallback++
		if gfp != EMERGENCY {
			a.boost[preferred] += a.step[preferred]
			if a.boost[preferred] > a.cap[preferred] {
				a.boost[preferred] = a.cap[preferred]
			}
		}
	}
}
