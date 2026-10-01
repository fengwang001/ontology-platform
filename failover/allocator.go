package failover

import (
	"errors"
	"sync"
)

var (
	ErrInvalidConfig        = errors.New("failover: invalid configuration")
	ErrLevelOutOfRange      = errors.New("failover: level out of range")
	ErrEmptyID              = errors.New("failover: host id is empty")
	ErrHostExists           = errors.New("failover: host id already exists")
	ErrHostNotFound         = errors.New("failover: host not found")
	ErrNoHosts              = errors.New("failover: no hosts registered")
	ErrInsufficientCapacity = errors.New("failover: insufficient capacity")
	ErrInvalidPoint         = errors.New("failover: pick point out of range")
)

type host struct {
	level   int
	healthy bool
}

type Allocator struct {
	mu    sync.Mutex
	l     int
	f     int
	delta int
	cap   []int
	hosts map[string]*host
	cur   []int
	set   []bool
}

// snapshotCur returns a copy of the current hysteresis memory values.
// Intended for tests and diagnostics.
func (a *Allocator) snapshotCur() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int(nil), a.cur...)
}

func New(L, F, D int, cap []int) (*Allocator, error) {
	if L < 1 || L > 16 {
		return nil, ErrInvalidConfig
	}
	if F < 100 || F > 1000 {
		return nil, ErrInvalidConfig
	}
	if D < 1 || D > 100 {
		return nil, ErrInvalidConfig
	}
	if len(cap) != L {
		return nil, ErrInvalidConfig
	}
	sum := 0
	for _, c := range cap {
		if c < 1 || c > 100 {
			return nil, ErrInvalidConfig
		}
		sum += c
	}
	if sum < 100 {
		return nil, ErrInvalidConfig
	}
	caps := append([]int(nil), cap...)
	return &Allocator{
		l:     L,
		f:     F,
		delta: D,
		cap:   caps,
		hosts: make(map[string]*host),
		cur:   make([]int, L),
		set:   make([]bool, L),
	}, nil
}

func (a *Allocator) AddHost(level int, id string, healthy bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if level < 0 || level >= a.l {
		return ErrLevelOutOfRange
	}
	if id == "" {
		return ErrEmptyID
	}
	if _, ok := a.hosts[id]; ok {
		return ErrHostExists
	}
	a.hosts[id] = &host{level: level, healthy: healthy}
	return nil
}

func (a *Allocator) RemoveHost(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.hosts[id]; !ok {
		return ErrHostNotFound
	}
	delete(a.hosts, id)
	return nil
}

func (a *Allocator) SetHealth(id string, healthy bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	h, ok := a.hosts[id]
	if !ok {
		return ErrHostNotFound
	}
	h.healthy = healthy
	return nil
}

func (a *Allocator) Loads() ([]int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loadsLocked()
}

func (a *Allocator) PickLevel(r int) (int, error) {
	if r < 0 || r > 99 {
		return 0, ErrInvalidPoint
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	loads, err := a.loadsLocked()
	if err != nil {
		return 0, err
	}
	prefix := 0
	for p, share := range loads {
		if r < prefix+share {
			return p, nil
		}
		prefix += share
	}
	return 0, ErrInsufficientCapacity
}

// rawStats returns, per level, the total host count and the raw health
// raw_p = 0 (t == 0) or min(100, floor(h*F/t)).
func (a *Allocator) rawStats() (total, healthy, raw []int) {
	total = make([]int, a.l)
	healthy = make([]int, a.l)
	for _, h := range a.hosts {
		total[h.level]++
		if h.healthy {
			healthy[h.level]++
		}
	}
	raw = make([]int, a.l)
	for p := 0; p < a.l; p++ {
		if total[p] > 0 {
			v := healthy[p] * a.f / total[p]
			if v > 100 {
				v = 100
			}
			raw[p] = v
		}
	}
	return total, healthy, raw
}

// nextCur applies the recovery-hysteresis memory update rule.
func (a *Allocator) nextCur(raw []int) []int {
	next := make([]int, a.l)
	for p := 0; p < a.l; p++ {
		switch {
		case !a.set[p], a.cur[p] == 0, raw[p] <= a.cur[p], raw[p] >= a.cur[p]+a.delta:
			next[p] = raw[p]
		default:
			next[p] = a.cur[p]
		}
	}
	return next
}

func (a *Allocator) loadsLocked() ([]int, error) {
	if len(a.hosts) == 0 {
		return nil, ErrNoHosts
	}
	total, _, raw := a.rawStats()
	cur := a.nextCur(raw)

	loads := make([]int, a.l)
	eligible := make([]bool, a.l)
	sum := 0
	for _, v := range cur {
		sum += v
	}

	switch {
	case sum == 0:
		// Panic fallback: the smallest level that has any host takes 100.
		for p := 0; p < a.l; p++ {
			eligible[p] = total[p] > 0
		}
		for p := 0; p < a.l; p++ {
			if total[p] > 0 {
				loads[p] = 100
				break
			}
		}
	case sum >= 100:
		rem := 100
		for p := 0; p < a.l; p++ {
			eligible[p] = cur[p] > 0
			give := cur[p]
			if give > rem {
				give = rem
			}
			loads[p] = give
			rem -= give
		}
	default:
		rem := 100
		firstPositive := -1
		for p := 0; p < a.l; p++ {
			eligible[p] = cur[p] > 0
			loads[p] = cur[p] * 100 / sum
			rem -= loads[p]
			if cur[p] > 0 && firstPositive == -1 {
				firstPositive = p
			}
		}
		loads[firstPositive] += rem
	}

	// Cap redistribution: clip overflow, then hand X out in level order.
	x := 0
	for p := 0; p < a.l; p++ {
		if loads[p] > a.cap[p] {
			x += loads[p] - a.cap[p]
			loads[p] = a.cap[p]
		}
	}
	for p := 0; p < a.l && x > 0; p++ {
		if !eligible[p] {
			continue
		}
		room := a.cap[p] - loads[p]
		if room > x {
			room = x
		}
		loads[p] += room
		x -= room
	}
	if x > 0 {
		return nil, ErrInsufficientCapacity
	}

	a.cur = cur
	for p := range a.set {
		a.set[p] = true
	}
	return loads, nil
}
