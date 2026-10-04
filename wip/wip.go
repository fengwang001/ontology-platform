package wip

import (
	"errors"
	"sync"

	"ontology/routing"
)

var (
	ErrInvalid   = errors.New("wip: invalid argument")
	ErrNotFound  = errors.New("wip: not found")
	ErrConflict  = errors.New("wip: identifier already exists")
	ErrState     = errors.New("wip: illegal state")
	ErrOverQty   = errors.New("wip: quantity exceeds queue cell")
	ErrReworkCap = errors.New("wip: rework limit exceeded")
	ErrClosed    = errors.New("wip: work order closed")
	ErrHeld      = errors.New("wip: work order held")
)

type Manager struct {
	reg *routing.Registry

	mu  sync.Mutex
	wos map[string]*workOrder
}

type cell struct{ i, k int }

type workOrder struct {
	mu sync.Mutex

	id     string
	route  *routing.Route
	q      int64
	queue  map[cell]int64
	done   int64
	scrap  int64
	wip    int64
	closed bool
	held   bool

	// touched is the number of distinct queue cells touched by the most
	// recent Report on this order; Close leaves it at zero. Unexported test
	// hook: it is never populated from n or R sized scans.
	touched int
}

func NewManager(reg *routing.Registry) *Manager {
	if reg == nil {
		reg = routingStd()
	}
	return &Manager{reg: reg, wos: map[string]*workOrder{}}
}

func routingStd() *routing.Registry {
	return routing.Std()
}

// Registry exposes the route registry used by this manager.
func (m *Manager) Registry() *routing.Registry { return m.reg }

func (m *Manager) get(wo string) *workOrder {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wos[wo]
}

func (m *Manager) Open(wo string, routeID string, q int64) error {
	if wo == "" || q < 1 || q > 1_000_000_000 {
		return ErrInvalid
	}
	rt, err := m.reg.Get(routeID)
	if err != nil {
		if errors.Is(err, routing.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.wos[wo]; ok {
		return ErrConflict
	}
	o := &workOrder{
		id:    wo,
		route: rt,
		q:     q,
		queue: map[cell]int64{{i: 1, k: 0}: q},
		wip:   q,
	}
	m.wos[wo] = o
	return nil
}

func (m *Manager) Report(wo string, i, k int, good, scrap, rework int64) error {
	if wo == "" || i < 1 || k < 0 || good < 0 || scrap < 0 || rework < 0 ||
		good+scrap+rework < 1 {
		return ErrInvalid
	}
	o := m.get(wo)
	if o == nil {
		return ErrNotFound
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return ErrClosed
	}
	if o.held {
		return ErrHeld
	}
	if i > o.route.N || k > o.route.R {
		return ErrInvalid
	}
	src := cell{i: i, k: k}
	have := o.queue[src]
	total := good + scrap + rework
	if total > have {
		return ErrOverQty
	}
	if rework > 0 && k == o.route.R {
		return ErrReworkCap
	}

	touched := map[cell]struct{}{src: {}}
	o.add(src, -total)
	o.scrap += scrap
	leaving := scrap
	if good > 0 {
		if i == o.route.N {
			o.done += good
			leaving += good
		} else {
			dst := cell{i: i + 1, k: k}
			o.add(dst, good)
			touched[dst] = struct{}{}
		}
	}
	if rework > 0 {
		dst := cell{i: o.route.Back[i-1], k: k + 1}
		o.add(dst, rework)
		touched[dst] = struct{}{}
	}
	o.wip -= leaving
	o.touched = len(touched)
	return nil
}

func (m *Manager) Split(wo, newWO string, i, k int, qty int64) error {
	if wo == "" || newWO == "" || i < 1 || k < 0 || qty < 1 {
		return ErrInvalid
	}
	o := m.get(wo)
	if o == nil {
		return ErrNotFound
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return ErrClosed
	}
	if o.held {
		return ErrHeld
	}
	if i > o.route.N || k > o.route.R {
		return ErrInvalid
	}
	src := cell{i: i, k: k}
	if o.queue[src] < qty {
		return ErrOverQty
	}

	m.mu.Lock()
	if _, ok := m.wos[newWO]; ok {
		m.mu.Unlock()
		return ErrState
	}
	child := &workOrder{
		id:    newWO,
		route: o.route,
		q:     qty,
		queue: map[cell]int64{src: qty},
		wip:   qty,
	}
	m.wos[newWO] = child
	m.mu.Unlock()

	o.add(src, -qty)
	o.q -= qty
	o.wip -= qty
	return nil
}

type CloseResult struct {
	Done     int64
	Scrapped int64
	Shortage int64
}

func (m *Manager) Close(wo string) (*CloseResult, error) {
	if wo == "" {
		return nil, ErrInvalid
	}
	o := m.get(wo)
	if o == nil {
		return nil, ErrNotFound
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, ErrClosed
	}
	if o.held {
		return nil, ErrHeld
	}
	if o.wip != 0 {
		return nil, ErrState
	}
	o.closed = true
	o.touched = 0
	return &CloseResult{
		Done:     o.done,
		Scrapped: o.scrap,
		Shortage: o.q - o.done,
	}, nil
}

func (o *workOrder) add(c cell, delta int64) {
	v := o.queue[c] + delta
	if v == 0 {
		delete(o.queue, c)
	} else {
		o.queue[c] = v
	}
}

// SetHeld flips the hold flag consulted by Report/Split/Close. The hold
// package is the only intended caller; the flag keeps the rejection ordering
// (closed before held) inside the locked work-order transition.
func (m *Manager) SetHeld(wo string, held bool) bool {
	o := m.get(wo)
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.held = held
	return true
}

func (m *Manager) IsHeld(wo string) (bool, bool) {
	o := m.get(wo)
	if o == nil {
		return false, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.held, true
}

// State is an immutable point-in-time snapshot of one work order.
type State struct {
	ID       string
	RouteID  string
	N, R     int
	Insp     []bool
	Q        int64
	Queue    map[cell]int64
	Done     int64
	Scrapped int64
	WIP      int64
	Closed   bool
	Held     bool
	Touched  int
}

// CellKey exposes the (operation, rework-level) pair used by State.Queue.
type CellKey = cell

// I returns the operation number (1-based) of the cell.
func (c cell) I() int { return c.i }

// K returns the rework count of the cell.
func (c cell) K() int { return c.k }

// At returns the quantity queued at operation i, rework level k.
func (s *State) At(i, k int) int64 { return s.Queue[cell{i: i, k: k}] }

func (m *Manager) Snapshot(wo string) (*State, error) {
	o := m.get(wo)
	if o == nil {
		return nil, ErrNotFound
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	q := make(map[cell]int64, len(o.queue))
	for c, v := range o.queue {
		q[c] = v
	}
	return &State{
		ID:       o.id,
		RouteID:  o.route.ID,
		N:        o.route.N,
		R:        o.route.R,
		Insp:     append([]bool(nil), o.route.Insp...),
		Q:        o.q,
		Queue:    q,
		Done:     o.done,
		Scrapped: o.scrap,
		WIP:      o.wip,
		Closed:   o.closed,
		Held:     o.held,
		Touched:  o.touched,
	}, nil
}

var Default = NewManager(nil)
