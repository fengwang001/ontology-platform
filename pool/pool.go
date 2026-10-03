// Package pool 管理后端端点的生命周期与每端点的在途账本。
//
// 端点有两种状态：active（可被选取）与 draining（被摘除、不再入选，
// 但仍可接收在途票据的 Release）。pool 本身不加锁，由上层 picker 串行化。
package pool

import (
	"sort"

	"ontology/ewma"
)

// State 是端点生命周期状态。
type State int

const (
	// Active 表示端点活跃、可被选取。
	Active State = iota
	// Draining 表示端点被摘除、在途请求排空后移除。
	Draining
)

// Endpoint 是单个后端端点的账本记录。
type Endpoint struct {
	id    string
	state State
	infl  int64
	est   *ewma.Estimator
}

// ID 返回端点 id。
func (e *Endpoint) ID() string { return e.id }

// Inflight 返回当前在途数。
func (e *Endpoint) Inflight() int64 { return e.infl }

// State 返回生命周期状态。
func (e *Endpoint) State() State { return e.state }

// Value 返回 now 时刻的时延读值（委托给 ewma 估计器）。
func (e *Endpoint) Value(now int64) int64 { return e.est.Value(now) }

// Observe 在 now 时刻写入一个样本 rtt。
func (e *Endpoint) Observe(rtt, now int64) { e.est.Sample(rtt, now) }

// Pool 是端点集合与在途账本。
type Pool struct {
	byID    map[string]*Endpoint
	tickets map[int64]*Endpoint
	count   int
	maxInfl int64
	tau     int64
	prior   int64
}

// New 创建空端点池。maxInfl 为单端点在途上限 M。
func New(tau, prior, maxInfl int64) *Pool {
	return &Pool{
		byID:    make(map[string]*Endpoint),
		tickets: make(map[int64]*Endpoint),
		maxInfl: maxInfl,
		tau:     tau,
		prior:   prior,
	}
}

// Add 登记一个新端点。返回 false 表示 id 已存在（含排空中）。
func (p *Pool) Add(id string) bool {
	if _, ok := p.byID[id]; ok {
		return false
	}
	p.byID[id] = &Endpoint{
		id:    id,
		state: Active,
		est:   ewma.New(p.tau, p.prior),
	}
	p.count++
	return true
}

// Remove 摘除端点：在途为 0 立即移除；否则转为排空。
// 返回值含义：
//   - ok=false：id 不存在；
//   - removed=true：在途为 0，已立即移除；
//   - alreadyDraining=true：端点此前已处于排空中；
//   - 三者皆 false/零值：由活跃新转入排空。
func (p *Pool) Remove(id string) (removed, alreadyDraining, ok bool) {
	ep, exists := p.byID[id]
	if !exists {
		return false, false, false
	}
	if ep.state == Draining {
		return false, true, true
	}
	if ep.infl == 0 {
		delete(p.byID, id)
		p.count--
		return true, false, true
	}
	ep.state = Draining
	return false, false, true
}

// Get 按 id 取端点，不存在返回 nil。
func (p *Pool) Get(id string) *Endpoint { return p.byID[id] }

// Len 返回端点数（含排空中）。
func (p *Pool) Len() int { return p.count }

// ActiveCount 返回活跃端点数（不论是否饱和）。
func (p *Pool) ActiveCount() int {
	n := 0
	for _, ep := range p.byID {
		if ep.state == Active {
			n++
		}
	}
	return n
}

// Eligible 返回状态活跃且在途数 < M 的端点，按 id 字节序升序排列。
func (p *Pool) Eligible() []*Endpoint {
	out := make([]*Endpoint, 0, len(p.byID))
	for _, ep := range p.byID {
		if ep.state == Active && ep.infl < p.maxInfl {
			out = append(out, ep)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].id < out[b].id })
	return out
}

// Acquire 为端点登记一笔在途请求并记录其票据号。
// 调用方须保证 ep 属于本池且当前符合候选条件。
func (p *Pool) Acquire(ep *Endpoint, ticket int64) {
	ep.infl++
	p.tickets[ticket] = ep
}

// Release 归还票据：在途数减 1。返回的 ep 为持有端点；
// removed 表示端点本处于排空且在途归零，已被真正移除。
// ok=false 表示票据未知或已归还（此时不改任何状态）。
func (p *Pool) Release(ticket int64) (ep *Endpoint, removed, ok bool) {
	ep, exists := p.tickets[ticket]
	if !exists {
		return nil, false, false
	}
	delete(p.tickets, ticket)
	ep.infl--
	if ep.state == Draining && ep.infl == 0 {
		delete(p.byID, ep.id)
		p.count--
		return ep, true, true
	}
	return ep, false, true
}

// InflightTotal 返回所有端点在途数之和，应等于未归还票据数。
func (p *Pool) InflightTotal() int64 {
	var total int64
	for _, ep := range p.byID {
		total += ep.infl
	}
	return total
}

// OpenTickets 返回未归还票据数。
func (p *Pool) OpenTickets() int { return len(p.tickets) }

// IDs 返回全部端点 id（含排空中），按字节序升序。
func (p *Pool) IDs() []string {
	out := make([]string, 0, len(p.byID))
	for id := range p.byID {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// EPStat 是端点的一份只读快照。
type EPStat struct {
	ID       string
	Draining bool
	Inflight int64
	Value    int64
}

// Snapshot 在单次遍历中返回全部端点（含排空中）状态与未归还票据数，
// now 用于读取时延估值。结果是同一把锁下的一致视图。
func (p *Pool) Snapshot(now int64) ([]EPStat, int) {
	stats := make([]EPStat, 0, len(p.byID))
	for _, ep := range p.byID {
		stats = append(stats, EPStat{
			ID:       ep.id,
			Draining: ep.state == Draining,
			Inflight: ep.infl,
			Value:    ep.est.Value(now),
		})
	}
	sort.Slice(stats, func(a, b int) bool { return stats[a].ID < stats[b].ID })
	return stats, len(p.tickets)
}
