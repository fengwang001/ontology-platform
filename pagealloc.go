package ontology

import (
	"errors"
	"sync"
)

var (
	ErrInvalidConfig   = errors.New("invalid allocator configuration")
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNoMemory        = errors.New("no memory available")
	ErrWouldReclaim    = errors.New("allocation would reclaim")
	ErrOverfree        = errors.New("free exceeds managed pages")
)

type GFP uint8

const (
	NORMAL GFP = iota
	ATOMIC
	EMERGENCY
)

type Marks struct {
	Min  int64
	Low  int64
	High int64
}

type Stats struct {
	Allocations []int64
	Fallbacks   int64
	Wakes       int64
}

type PageAllocator struct {
	mu       sync.Mutex
	zones    int
	managed  []int64
	ratios   []int64
	totalM   int64
	totalMin int64
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
}

func NewPageAllocator(zones int, managed []int64, ratios []int64, totalMin int64) (*PageAllocator, error) {
	if zones < 1 || zones > 4 || len(managed) != zones || len(ratios) != zones || totalMin < 0 {
		return nil, ErrInvalidConfig
	}

	total := int64(0)
	for _, pages := range managed {
		if pages < 1 || pages > 1_000_000_000 {
			return nil, ErrInvalidConfig
		}
		total += pages
	}
	if totalMin > total {
		return nil, ErrInvalidConfig
	}
	for _, ratio := range ratios {
		if ratio < 1 || ratio > 1000 {
			return nil, ErrInvalidConfig
		}
	}

	a := &PageAllocator{
		zones:   zones,
		managed: append([]int64(nil), managed...),
		ratios:  append([]int64(nil), ratios...),
		totalM:  total,
		free:    append([]int64(nil), managed...),
		reserve: make([][]int64, zones),
		kswapd:  make([]bool, zones),
		allocs:  make([]int64, zones),
	}
	a.recalculateWatermarks(totalMin)

	for z := 0; z < zones; z++ {
		a.reserve[z] = make([]int64, zones)
		for c := z + 1; c < zones; c++ {
			sum := int64(0)
			for k := z + 1; k <= c; k++ {
				sum += a.managed[k]
			}
			a.reserve[z][c] = sum / a.ratios[z]
		}
	}

	return a, nil
}

func (a *PageAllocator) Alloc(preferred int, pages int64, gfp GFP) (int, error) {
	if preferred < 0 || preferred >= a.zones || pages < 1 || pages > 1_000_000_000 || gfp > EMERGENCY {
		return 0, ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.checks = 0

	if gfp == EMERGENCY {
		for z := preferred; z >= 0; z-- {
			if a.free[z] >= pages {
				a.free[z] -= pages
				a.allocs[z]++
				if z != preferred {
					a.fallback++
				}
				return z, nil
			}
		}
		return 0, ErrNoMemory
	}

	for z := preferred; z >= 0; z-- {
		a.checks++
		if a.ok(z, pages, a.low[z]+a.boost[z], preferred) {
			a.allocate(z, preferred, pages)
			return z, nil
		}
	}

	if !a.kswapd[preferred] {
		a.kswapd[preferred] = true
		a.wakes++
	}

	watermark := a.min
	if gfp == ATOMIC {
		watermark = a.atomic
	}
	for z := preferred; z >= 0; z-- {
		a.checks++
		if a.ok(z, pages, watermark[z], preferred) {
			a.allocate(z, preferred, pages)
			return z, nil
		}
	}

	if gfp == NORMAL {
		return 0, ErrWouldReclaim
	}
	return 0, ErrNoMemory
}

func (a *PageAllocator) Free(zone int, pages int64) error {
	if zone < 0 || zone >= a.zones || pages < 1 || pages > 1_000_000_000 {
		return ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.free[zone] > a.managed[zone]-pages {
		return ErrOverfree
	}
	a.free[zone] += pages
	a.clearSleepers()
	return nil
}

func (a *PageAllocator) SetTotalMin(totalMin int64) error {
	if totalMin < 0 || totalMin > a.totalM {
		return ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.recalculateWatermarks(totalMin)
	for z := range a.boost {
		if a.boost[z] > a.cap[z] {
			a.boost[z] = a.cap[z]
		}
	}
	a.clearSleepers()
	return nil
}

func (a *PageAllocator) FreePages(zone int) (int64, error) {
	if zone < 0 || zone >= a.zones {
		return 0, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.free[zone], nil
}

func (a *PageAllocator) Marks(zone int) (Marks, error) {
	if zone < 0 || zone >= a.zones {
		return Marks{}, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return Marks{Min: a.min[zone], Low: a.low[zone], High: a.high[zone]}, nil
}

func (a *PageAllocator) Boost(zone int) (int64, error) {
	if zone < 0 || zone >= a.zones {
		return 0, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.boost[zone], nil
}

func (a *PageAllocator) Reserve(zone, preferred int) (int64, error) {
	if zone < 0 || zone >= a.zones || preferred < 0 || preferred >= a.zones {
		return 0, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reserve[zone][preferred], nil
}

func (a *PageAllocator) Kswapd(zone int) (bool, error) {
	if zone < 0 || zone >= a.zones {
		return false, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.kswapd[zone], nil
}

func (a *PageAllocator) Stats() Stats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Stats{
		Allocations: append([]int64(nil), a.allocs...),
		Fallbacks:   a.fallback,
		Wakes:       a.wakes,
	}
}

func (a *PageAllocator) Checks() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.checks
}

func (a *PageAllocator) recalculateWatermarks(totalMin int64) {
	a.totalMin = totalMin
	a.min = make([]int64, a.zones)
	a.low = make([]int64, a.zones)
	a.high = make([]int64, a.zones)
	a.atomic = make([]int64, a.zones)
	a.step = make([]int64, a.zones)
	a.cap = make([]int64, a.zones)

	if len(a.boost) != a.zones {
		a.boost = make([]int64, a.zones)
	}

	for z := 0; z < a.zones; z++ {
		minimum := totalMin * a.managed[z] / a.totalM
		a.min[z] = minimum
		a.low[z] = minimum + minimum/4
		a.high[z] = minimum + minimum/2
		a.atomic[z] = minimum - minimum/2
		a.step[z] = maxInt64(1, a.high[z]/4)
		a.cap[z] = a.high[z] / 2
	}
}

func (a *PageAllocator) ok(zone int, pages int64, watermark int64, preferred int) bool {
	return a.free[zone] > pages+watermark+a.reserve[zone][preferred]
}

func (a *PageAllocator) allocate(zone, preferred int, pages int64) {
	a.free[zone] -= pages
	a.allocs[zone]++
	if zone != preferred {
		a.fallback++
		a.boost[preferred] = minInt64(a.cap[preferred], a.boost[preferred]+a.step[preferred])
	}
}

func (a *PageAllocator) clearSleepers() {
	for z := 0; z < a.zones; z++ {
		if a.kswapd[z] && a.free[z] > a.high[z]+a.boost[z] {
			a.kswapd[z] = false
			a.boost[z] = 0
		}
	}
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
