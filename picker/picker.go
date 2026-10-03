// Package picker 实现带峰值时延估计、在途惩罚与饱和背压的 P2C
// （power-of-two-choices）后端端点选择器。
//
// 所有公开方法均可并发调用：内部用一把读写锁串行化，结果等价于某个
// 合法的串行顺序。被拒绝的操作不改变任何状态（含票据号、最大已见
// 时间与时延估计）。
package picker

import (
	"errors"
	"sync"

	"ontology/pool"
)

// 配置与时序边界。
const (
	minParam = 1
	maxParam = 1_000_000_000 // τ、P0、Pf、rtt 的上界
	maxM     = 1_000_000
	maxN     = 100_000
	maxNow   = 1_000_000_000_000_000
)

var (
	// ErrInvalidParam：id 为空，或 rtt 越界，或构造参数越界。
	ErrInvalidParam = errors.New("picker: invalid parameter")
	// ErrInvalidTime：now 不在 [0,1e15]。
	ErrInvalidTime = errors.New("picker: invalid time")
	// ErrClockBackward：now 小于已通过检查的最大 now。
	ErrClockBackward = errors.New("picker: clock moved backward")
	// ErrNotFound：端点 id 不存在。
	ErrNotFound = errors.New("picker: endpoint not found")
	// ErrExists：端点 id 已存在（含排空中）。
	ErrExists = errors.New("picker: endpoint already exists")
	// ErrFull：端点数（含排空中）已达 Nmax。
	ErrFull = errors.New("picker: endpoint pool full")
	// ErrDraining：端点已处于排空中。
	ErrDraining = errors.New("picker: endpoint is draining")
	// ErrSaturated：存在活跃端点但全部在途达到上限 M。
	ErrSaturated = errors.New("picker: all active endpoints saturated")
	// ErrNoEndpoint：不存在任何活跃端点。
	ErrNoEndpoint = errors.New("picker: no active endpoint")
	// ErrTicket：票据未知或已归还。
	ErrTicket = errors.New("picker: unknown or returned ticket")
)

// Config 是选择器构造参数，单位均为毫秒。
type Config struct {
	Tau          int64 // 峰值衰减期 τ
	Prior        int64 // 先验时延 P0
	FailPenalty  int64 // 失败惩罚时延 Pf
	MaxInflight  int64 // 单端点在途上限 M
	MaxEndpoints int   // 端点数上限 Nmax
}

// Picker 是线程安全的 P2C 端点选择器。
type Picker struct {
	mu         sync.RWMutex
	cfg        Config
	pool       *pool.Pool
	maxNow     int64
	nextTicket int64
}

// New 构造选择器并校验参数范围。
func New(cfg Config) (*Picker, error) {
	if cfg.Tau < minParam || cfg.Tau > maxParam ||
		cfg.Prior < minParam || cfg.Prior > maxParam ||
		cfg.FailPenalty < minParam || cfg.FailPenalty > maxParam ||
		cfg.MaxInflight < minParam || cfg.MaxInflight > maxM ||
		cfg.MaxEndpoints < minParam || cfg.MaxEndpoints > maxN {
		return nil, ErrInvalidParam
	}
	return &Picker{
		cfg:        cfg,
		pool:       pool.New(cfg.Tau, cfg.Prior, cfg.MaxInflight),
		nextTicket: 1,
	}, nil
}

// AddEndpoint 登记端点。id 已存在（含排空中）报 ErrExists，
// 端点数（含排空中）达 Nmax 报 ErrFull。
func (p *Picker) AddEndpoint(id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.pool.Get(id) != nil {
		return ErrExists
	}
	if p.pool.Len() >= p.cfg.MaxEndpoints {
		return ErrFull
	}
	p.pool.Add(id)
	return nil
}

// RemoveEndpoint 摘除端点：在途为 0 立即移除，否则转排空。
// 不存在报 ErrNotFound，已在排空中报 ErrDraining。
func (p *Picker) RemoveEndpoint(id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	removed, alreadyDraining, ok := p.pool.Remove(id)
	switch {
	case !ok:
		return ErrNotFound
	case alreadyDraining:
		return ErrDraining
	case removed:
		return nil
	default:
		return nil
	}
}

// Pick 用注入随机数 r1、r2 做二选一。返回票据号与获胜端点 id。
// 无可选端点时：有活跃端点报 ErrSaturated，否则报 ErrNoEndpoint。
func (p *Picker) Pick(now int64, r1, r2 uint64) (ticket int64, endpoint string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err = p.checkTimeLocked(now); err != nil {
		return 0, "", err
	}

	elig := p.pool.Eligible()
	n := len(elig)
	if n == 0 {
		if p.pool.ActiveCount() > 0 {
			return 0, "", ErrSaturated
		}
		return 0, "", ErrNoEndpoint
	}

	var winner *pool.Endpoint
	if n == 1 {
		winner = elig[0]
	} else {
		i := int(r1 % uint64(n))
		j := int((uint64(i) + 1 + r2%uint64(n-1)) % uint64(n))
		c1 := elig[i].Value(now) * (elig[i].Inflight() + 1)
		c2 := elig[j].Value(now) * (elig[j].Inflight() + 1)
		if c1 < c2 || (c1 == c2 && elig[i].ID() < elig[j].ID()) {
			winner = elig[i]
		} else {
			winner = elig[j]
		}
	}

	ticket = p.nextTicket
	p.nextTicket++
	p.maxNow = now
	p.pool.Acquire(winner, ticket)
	return ticket, winner.ID(), nil
}

// Release 归还票据。ok 为 false 时以 Pf 而非 rtt 作为时延样本。
// 若持有者处于排空且在途归零，则该端点被移除。
func (p *Picker) Release(ticket int64, rtt int64, ok bool, now int64) error {
	if rtt < 0 || rtt > maxParam {
		return ErrInvalidParam
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkTimeLocked(now); err != nil {
		return err
	}

	ep, _, found := p.pool.Release(ticket)
	if !found {
		return ErrTicket
	}
	sample := rtt
	if !ok {
		sample = p.cfg.FailPenalty
	}
	ep.Observe(sample, now)
	p.maxNow = now
	return nil
}

// checkTimeLocked 必须在持有写锁时调用，保证时间范围、时钟回拨判定与
// maxNow 推进处于同一个串行点。
func (p *Picker) checkTimeLocked(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidTime
	}
	if now < p.maxNow {
		return ErrClockBackward
	}
	return nil
}

// ValueAt 返回端点在 now 时刻的时延读值。纯读操作，不推进最大已见时间。
// ok=false 表示端点不存在。
func (p *Picker) ValueAt(id string, now int64) (val int64, ok bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ep := p.pool.Get(id)
	if ep == nil {
		return 0, false
	}
	return ep.Value(now), true
}

// Inflight 返回端点当前在途数。ok=false 表示端点不存在。
func (p *Picker) Inflight(id string) (infl int64, ok bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ep := p.pool.Get(id)
	if ep == nil {
		return 0, false
	}
	return ep.Inflight(), true
}

// IsDraining 报告端点是否处于排空中。不存在返回 false。
func (p *Picker) IsDraining(id string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ep := p.pool.Get(id)
	return ep != nil && ep.State() == pool.Draining
}

// NumEndpoints 返回端点数（含排空中）。
func (p *Picker) NumEndpoints() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pool.Len()
}

// NumActive 返回活跃端点数（含饱和者）。
func (p *Picker) NumActive() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pool.ActiveCount()
}

// OpenTickets 返回未归还票据数。
func (p *Picker) OpenTickets() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pool.OpenTickets()
}

// EndpointIDs 返回当前全部端点 id（含排空中）的副本，按字节序排列。
func (p *Picker) EndpointIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pool.IDs()
}

// Stat 是端点快照的对外视图。
type Stat struct {
	ID       string
	Draining bool
	Inflight int64
	Value    int64
}

// Snapshot 原子返回全部端点状态与未归还票据数（同一把锁下一致）。
func (p *Picker) Snapshot(now int64) ([]Stat, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	eps, open := p.pool.Snapshot(now)
	out := make([]Stat, len(eps))
	for i, e := range eps {
		out[i] = Stat{ID: e.ID, Draining: e.Draining, Inflight: e.Inflight, Value: e.Value}
	}
	return out, open
}
