// Package ingest 维护每台设备的遥测序号归宿、设备缓冲范围、连续水位，
// 并通过 plan 包对缺口序号进行断点补传规划。
package ingest

import (
	"errors"
	"sync"

	"ontology/plan"
	"ontology/seqset"
)

// 哨兵错误，按拒绝优先级 ErrInvalid > ErrClockBack > ErrNoDevice > ErrRegress 排列。
var (
	ErrInvalid   = errors.New("ingest: invalid argument")
	ErrClockBack = errors.New("ingest: clock moved backwards")
	ErrNoDevice  = errors.New("ingest: device not registered")
	ErrExists    = errors.New("ingest: device already registered")
	ErrRegress   = errors.New("ingest: range regression")
)

// Config 是服务端规划参数。
type Config = plan.Config

// Stats 是一台设备的快照计数。
type Stats struct {
	Hi, Lo, F      int64
	Received, Lost int64
	Missing        int64
	Dup, Late      int64
}

// Registry 是全部设备的归属表，所有方法可并发调用。
type Registry struct {
	mu   sync.Mutex
	devs map[string]*device
}

// device 是单台设备的完整状态。
type device struct {
	hi, lo, f int64
	lastNow   int64
	recv      *seqset.Set
	lost      *seqset.Set
	gaps      *plan.GapSet
	dup       int64
	late      int64
}

// NewRegistry 创建空设备表。
func NewRegistry() *Registry { return &Registry{devs: map[string]*device{}} }

// Register 登记一台设备，构造参数为该设备的规划参数。
func (r *Registry) Register(dev string, cfg Config) error {
	if dev == "" || cfg.Lm < 1 || cfg.Lm > 10000 ||
		cfg.K < 1 || cfg.K > 100 ||
		cfg.Tq < 1 || cfg.Tq > 1e9 ||
		cfg.R < 1 || cfg.R > 10 {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.devs[dev]; ok {
		return ErrExists
	}
	r.devs[dev] = &device{
		lo:   1,
		recv: seqset.New(),
		lost: seqset.New(),
		gaps: plan.New(cfg),
	}
	return nil
}

// sink 把 plan 判丢的区间记入 Lost 归宿集。
type sink struct{ d *device }

func (s sink) LostRange(l, rr int64) {
	s.d.lost.Insert(l, rr)
}

// advanceF 沿 Received∪Lost 的连续前缀推进水位。
func (d *device) advanceF() {
	for {
		x := d.f + 1
		if d.recv.Contains(x) || d.lost.Contains(x) {
			d.f = x
			continue
		}
		return
	}
}

func validClock(now int64) bool { return now >= 0 && now <= 1e12 }

// Ingest 处理一个到达序号。
func (r *Registry) Ingest(dev string, seq, now int64) error {
	if seq < 1 || seq > 1e12 || !validClock(now) {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devs[dev]
	if !ok {
		return ErrNoDevice
	}
	if now < d.lastNow {
		return ErrClockBack
	}
	d.lastNow = now
	if seq > d.hi {
		if seq > d.hi+1 {
			d.gaps.AddRun(d.hi+1, seq)
		}
		d.hi = seq
		d.recv.Insert(seq, seq+1)
		d.advanceF()
		return nil
	}
	// seq <= hi：判定归宿。缺口（含在途）到达仅移出自身。
	gotRecv := d.recv.Contains(seq)
	gotLost := d.lost.Contains(seq)
	switch {
	case gotLost:
		d.late++
	case gotRecv:
		d.dup++
	default:
		d.gaps.Fill(seq)
		d.recv.Insert(seq, seq+1)
		d.advanceF()
	}
	return nil
}

// Hello 接受设备声明的本地缓冲范围。
func (r *Registry) Hello(dev string, lo2, hi2, now int64) error {
	if lo2 < 1 || hi2 < lo2-1 || hi2 > 1e12 || !validClock(now) {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devs[dev]
	if !ok {
		return ErrNoDevice
	}
	if now < d.lastNow {
		return ErrClockBack
	}
	if lo2 < d.lo || hi2 < d.hi {
		return ErrRegress
	}
	d.lastNow = now
	// 先扩 hi：新增序号登记为缺口（其中小于新 lo 的马上判丢）。
	if hi2 > d.hi {
		d.gaps.AddRun(d.hi+1, hi2+1)
		d.hi = hi2
	}
	// 抬 lo：挖出 [旧lo,新lo) 内仍 Missing 的序号（即使在途）判丢。
	if lo2 > d.lo {
		d.gaps.LoseBelow(sink{d}, d.lo, lo2)
		d.lo = lo2
	}
	d.advanceF()
	return nil
}

// Plan 结算超时并返回本次补传请求清单。
func (r *Registry) Plan(dev string, now, budget int64) ([]plan.Range, error) {
	if budget < 1 || budget > 1e6 || !validClock(now) {
		return nil, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devs[dev]
	if !ok {
		return nil, ErrNoDevice
	}
	if now < d.lastNow {
		return nil, ErrClockBack
	}
	d.lastNow = now
	sk := sink{d}
	d.gaps.Settle(sk, now)
	out := d.gaps.Emit(now, budget)
	if out == nil {
		out = []plan.Range{}
	}
	d.advanceF()
	return out, nil
}

// Stats 返回设备当前快照；设备不存在返回 ErrNoDevice。
func (r *Registry) Stats(dev string) (Stats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devs[dev]
	if !ok {
		return Stats{}, ErrNoDevice
	}
	return Stats{
		Hi:       d.hi,
		Lo:       d.lo,
		F:        d.f,
		Received: d.recv.Count(),
		Lost:     d.lost.Count(),
		Missing:  d.gaps.Missing(),
		Dup:      d.dup,
		Late:     d.late,
	}, nil
}
