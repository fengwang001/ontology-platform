package ontology

import (
	"errors"
	"sync"
)

var (
	ErrInvalidConfig   = errors.New("invalid config")
	ErrLevelOutOfRange = errors.New("level out of range")
	ErrEmptyID         = errors.New("host id is empty")
	ErrDuplicateID     = errors.New("host id already exists")
	ErrHostNotFound    = errors.New("host not found")
	ErrNoHosts         = errors.New("no hosts registered")
	ErrOutOfCapacity   = errors.New("out of capacity")
	ErrInvalidPoint    = errors.New("pick point out of range")
)

type host struct {
	level   int
	healthy bool
}

type FailoverAllocator struct {
	mu     sync.Mutex
	L      int
	F      int
	delta  int
	cap    []int
	hosts  map[string]*host
	totals []int
	health []int
	cur    []int
}

func NewFailoverAllocator(L, F, delta int, caps []int) (*FailoverAllocator, error) {
	if L < 1 || L > 16 || F < 100 || F > 1000 || delta < 1 || delta > 100 {
		return nil, ErrInvalidConfig
	}
	if len(caps) != L {
		return nil, ErrInvalidConfig
	}
	sum := 0
	for _, c := range caps {
		if c < 1 || c > 100 {
			return nil, ErrInvalidConfig
		}
		sum += c
	}
	if sum < 100 {
		return nil, ErrInvalidConfig
	}
	cp := make([]int, L)
	copy(cp, caps)
	return &FailoverAllocator{
		L:      L,
		F:      F,
		delta:  delta,
		cap:    cp,
		hosts:  make(map[string]*host),
		totals: make([]int, L),
		health: make([]int, L),
		cur:    make([]int, L),
	}, nil
}

func (a *FailoverAllocator) AddHost(level int, id string, healthy bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if level < 0 || level >= a.L {
		return ErrLevelOutOfRange
	}
	if id == "" {
		return ErrEmptyID
	}
	if _, ok := a.hosts[id]; ok {
		return ErrDuplicateID
	}
	a.hosts[id] = &host{level: level, healthy: healthy}
	a.totals[level]++
	if healthy {
		a.health[level]++
	}
	return nil
}

func (a *FailoverAllocator) RemoveHost(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	h, ok := a.hosts[id]
	if !ok {
		return ErrHostNotFound
	}
	delete(a.hosts, id)
	a.totals[h.level]--
	if h.healthy {
		a.health[h.level]--
	}
	return nil
}

func (a *FailoverAllocator) SetHealth(id string, healthy bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	h, ok := a.hosts[id]
	if !ok {
		return ErrHostNotFound
	}
	if h.healthy == healthy {
		return nil
	}
	h.healthy = healthy
	if healthy {
		a.health[h.level]++
	} else {
		a.health[h.level]--
	}
	return nil
}

// rawLocked computes the over-provisioned raw health of level p:
// raw_p = 0 when no host exists, else min(100, floor(h*F/t)).
func (a *FailoverAllocator) rawLocked(p int) int {
	t := a.totals[p]
	if t == 0 {
		return 0
	}
	v := a.health[p] * a.F / t
	if v > 100 {
		return 100
	}
	return v
}

// computeLocked runs the full Loads pipeline against a snapshot of cur
// values. When update is true and the allocation succeeds, the recovered
// memory values (raw with recovery hysteresis) are committed. Rejected
// allocations leave every cur untouched.
func (a *FailoverAllocator) computeLocked(update bool) ([]int, error) {
	if len(a.hosts) == 0 {
		return nil, ErrNoHosts
	}

	raw := make([]int, a.L)
	next := make([]int, a.L)
	for p := 0; p < a.L; p++ {
		raw[p] = a.rawLocked(p)
		cur := a.cur[p]
		switch {
		case cur == 0 || raw[p] <= cur || raw[p] >= cur+a.delta:
			next[p] = raw[p]
		default:
			next[p] = cur
		}
	}

	loads := make([]int, a.L)
	sum := 0
	for _, v := range next {
		sum += v
	}

	panicMode := sum == 0
	switch {
	case panicMode:
		for p := 0; p < a.L; p++ {
			if a.totals[p] > 0 {
				loads[p] = 100
				break
			}
		}
	case sum >= 100:
		remain := 100
		for p := 0; p < a.L; p++ {
			v := next[p]
			if v > remain {
				v = remain
			}
			loads[p] = v
			remain -= v
		}
	default:
		assigned := 0
		for p := 0; p < a.L; p++ {
			v := next[p] * 100 / sum
			loads[p] = v
			assigned += v
		}
		for p := 0; p < a.L; p++ {
			if next[p] > 0 {
				loads[p] += 100 - assigned
				break
			}
		}
	}

	x := 0
	for p := 0; p < a.L; p++ {
		if loads[p] > a.cap[p] {
			x += loads[p] - a.cap[p]
			loads[p] = a.cap[p]
		}
	}
	for p := 0; p < a.L && x > 0; p++ {
		eligible := next[p] > 0
		if panicMode {
			eligible = a.totals[p] > 0
		}
		if !eligible {
			continue
		}
		room := a.cap[p] - loads[p]
		if room <= 0 {
			continue
		}
		give := x
		if give > room {
			give = room
		}
		loads[p] += give
		x -= give
	}
	if x > 0 {
		return nil, ErrOutOfCapacity
	}

	if update {
		a.cur = next
	}
	return loads, nil
}

func (a *FailoverAllocator) Loads() ([]int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.computeLocked(true)
}

func (a *FailoverAllocator) PickLevel(r int) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r < 0 || r > 99 {
		return 0, ErrInvalidPoint
	}
	loads, err := a.computeLocked(true)
	if err != nil {
		return 0, err
	}
	prefix := 0
	for p, v := range loads {
		if r < prefix+v {
			return p, nil
		}
		prefix += v
	}
	return 0, ErrOutOfCapacity
}
