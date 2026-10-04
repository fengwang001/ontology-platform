// Package wip 实现按工序与返工次数分层的在制队列流转。
package wip

import (
	"sync"

	"ontology/routing"
)

// cell 定位一个队列格：工序 i（1 基）与返工次数 k。
type cell struct {
	i int
	k int
}

// workOrder 是一张工单的全部流转状态。
type workOrder struct {
	route    string
	qty      int64 // Q：投入量（拆单时减少）
	done     int64
	scrapped int64
	wipTotal int64 // 在制总量 = 全部队列格之和
	closed   bool
	held     bool // 由 hold 包通过钩子管理
	queue    map[cell]int64
	touched  int // 非导出：本次操作触碰的不同队列格数
	touchSet map[cell]struct{}
}

// HookOrder 是工单在钩子回调中的受控视图。
type HookOrder interface {
	RouteName() string
	IsClosed() bool
	IsHeld() bool
	SetHeld(bool)
}

func (o *workOrder) RouteName() string { return o.route }
func (o *workOrder) IsClosed() bool    { return o.closed }
func (o *workOrder) IsHeld() bool      { return o.held }
func (o *workOrder) SetHeld(h bool)    { o.held = h }

// Hooks 是流转层向挂起层暴露的扩展点（hold 包实现并注入）。
// 所有钩子均在 Manager 锁内调用。
type Hooks struct {
	// OnOpen 在新工单登记的同一把锁内调用。
	OnOpen func(id string, o HookOrder, route string, Q int64)
	// OnSplit 在拆单原子完成后调用。
	OnSplit func(id, newID string, o, n HookOrder, i, k int, qty int64)
	// OnReported 在一次 Report 原子落账后调用（首过统计与挂起判定）。
	OnReported func(id string, o HookOrder, i, k int, good, scrap, rework int64)
}

// Manager 管理工单流转。
type Manager struct {
	rm     *routing.Manager
	mu     sync.Mutex
	orders map[string]*workOrder
	hooks  Hooks
}

// Option 配置 Manager。
type Option func(*Manager)

// WithHooks 注入挂起层钩子。
func WithHooks(h Hooks) Option {
	return func(m *Manager) { m.hooks = h }
}

// NewManager 创建流转管理器。
func NewManager(rm *routing.Manager, opts ...Option) *Manager {
	m := &Manager{
		rm:     rm,
		orders: make(map[string]*workOrder),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Open 开立工单，queue[1][0] = Q。
func (m *Manager) Open(wo, route string, Q int64) error {
	if wo == "" || route == "" || Q < 1 || Q > 1_000_000_000 {
		return routing.ErrInvalid
	}
	r := m.rm.Lookup(route)
	if r == nil {
		return routing.ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.orders[wo]; ok {
		return routing.ErrState
	}
	m.orders[wo] = &workOrder{
		route:    route,
		qty:      Q,
		wipTotal: Q,
		queue:    map[cell]int64{{i: 1, k: 0}: Q},
	}
	if m.hooks.OnOpen != nil {
		m.hooks.OnOpen(wo, m.orders[wo], route, Q)
	}
	return nil
}

// Report 在 (i,k) 格报工：良品、报废、返工在工序间流转。
func (m *Manager) Report(wo string, i, k int, good, scrap, rework int64) error {
	total := good + scrap + rework
	if wo == "" || i < 1 || k < 0 || good < 0 || scrap < 0 || rework < 0 || total < 1 {
		return routing.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[wo]
	if !ok {
		return routing.ErrNotFound
	}
	if o.closed || o.held {
		return routing.ErrState
	}
	r := m.rm.Lookup(o.route)
	if i > r.N || k > r.R {
		return routing.ErrInvalid
	}
	src := cell{i: i, k: k}
	if total > o.queue[src] {
		return routing.ErrOverflow
	}
	if rework > 0 && k == r.R {
		return routing.ErrReworkCap
	}

	o.touched = 0
	o.touchSet = make(map[cell]struct{}, 3)
	touch := func(c cell) {
		if _, seen := o.touchSet[c]; !seen {
			o.touchSet[c] = struct{}{}
			o.touched++
		}
	}

	touch(src)
	o.queue[src] -= total
	if o.queue[src] == 0 {
		delete(o.queue, src)
	}
	o.scrapped += scrap
	o.wipTotal -= scrap

	if good > 0 {
		if i == r.N {
			o.done += good
			o.wipTotal -= good
		} else {
			nc := cell{i: i + 1, k: k}
			touch(nc)
			o.queue[nc] += good
		}
	}
	if rework > 0 {
		rc := cell{i: r.Back[i-1], k: k + 1}
		touch(rc)
		o.queue[rc] += rework
	}
	if m.hooks.OnReported != nil {
		m.hooks.OnReported(wo, o, i, k, good, scrap, rework)
	}
	return nil
}

// Split 把 (i,k) 格的 qty 件拆到新工单。
func (m *Manager) Split(wo, newWo string, i, k int, qty int64) error {
	if wo == "" || newWo == "" || i < 1 || k < 0 || qty < 1 {
		return routing.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[wo]
	if !ok {
		return routing.ErrNotFound
	}
	if o.closed || o.held {
		return routing.ErrState
	}
	if _, exists := m.orders[newWo]; exists {
		return routing.ErrState
	}
	r := m.rm.Lookup(o.route)
	if i > r.N || k > r.R {
		return routing.ErrInvalid
	}
	c := cell{i: i, k: k}
	if qty > o.queue[c] {
		return routing.ErrOverflow
	}

	o.touched = 0
	o.touchSet = map[cell]struct{}{c: {}}
	o.touched = 1
	o.queue[c] -= qty
	if o.queue[c] == 0 {
		delete(o.queue, c)
	}
	o.qty -= qty
	o.wipTotal -= qty

	n := &workOrder{
		route:    o.route,
		qty:      qty,
		wipTotal: qty,
		queue:    map[cell]int64{c: qty},
	}
	m.orders[newWo] = n
	if m.hooks.OnSplit != nil {
		m.hooks.OnSplit(wo, newWo, o, n, i, k, qty)
	}
	return nil
}

// CloseResult 是关闭工单的返回。
type CloseResult struct {
	Done      int64
	Scrapped  int64
	Shortfall int64 // 欠产 Q - done
}

// Close 在在制总量为 0 时关闭工单。
func (m *Manager) Close(wo string) (CloseResult, error) {
	if wo == "" {
		return CloseResult{}, routing.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[wo]
	if !ok {
		return CloseResult{}, routing.ErrNotFound
	}
	o.touched = 0 // Close 任何路径都不触碰队列格
	if o.closed || o.held {
		return CloseResult{}, routing.ErrState
	}
	if o.wipTotal != 0 {
		return CloseResult{}, routing.ErrState
	}
	o.closed = true
	return CloseResult{Done: o.done, Scrapped: o.scrapped, Shortfall: o.qty - o.done}, nil
}

// Snapshot 是工单可观察状态的不可变快照。
type Snapshot struct {
	Route    string
	Q        int64
	Done     int64
	Scrapped int64
	Closed   bool
	Held     bool
	Queue    map[[2]int]int64 // [[i,k]]=数量
}

// State 返回工单快照；工单不存在返回 routing.ErrNotFound。
func (m *Manager) State(wo string) (Snapshot, error) {
	if wo == "" {
		return Snapshot{}, routing.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[wo]
	if !ok {
		return Snapshot{}, routing.ErrNotFound
	}
	s := Snapshot{
		Route:    o.route,
		Q:        o.qty,
		Done:     o.done,
		Scrapped: o.scrapped,
		Closed:   o.closed,
		Held:     o.held,
		Queue:    make(map[[2]int]int64, len(o.queue)),
	}
	for c, v := range o.queue {
		s.Queue[[2]int{c.i, c.k}] = v
	}
	return s, nil
}

// WithLock 在持有 Manager 锁的情况下执行 fn（供 hold 包实现 Resume）。
func (m *Manager) WithLock(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn()
}

// Order 在持锁状态下按号取内部工单（供 hold 包使用），不存在返回 nil。
func (m *Manager) Order(wo string) HookOrder {
	if o, ok := m.orders[wo]; ok {
		return o
	}
	return nil
}
