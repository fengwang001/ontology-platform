package rollout

import "sync"

// batch 是同一 readyAt 时刻变为就绪的新版本实例分批记账。
type batch struct {
	readyAt int64
	count   int64
}

// Planner 是带最小就绪时长与停滞判定的滚动更新步进计划器。
// 所有方法可被并发调用，效果等价于某一串行顺序。
type Planner struct {
	mu     sync.Mutex
	n      int64
	s      int64
	u      int64
	mr     int64
	p      int64
	maxNow int64

	a, b, c, d int64
	// batches 按 readyAt 升序保存，同 readyAt 合并为一批；count 之和恒等于 c。
	batches []batch
	stall   int64
}

// StepResult 是一次 Step 的结果。
type StepResult struct {
	Up      int64
	Cleaned int64
	Removed int64
	Stalled bool
}

// State 是计划器某一时刻的完整快照，便于查询与测试。
type State struct {
	OldReady    int64
	OldUnready  int64
	NewReady    int64
	NewUnready  int64
	StableReady int64
	StallCount  int64
	MaxNow      int64
	Done        bool
}

// Total 返回实例总数 a+b+c+d。
func (s State) Total() int64 {
	return s.OldReady + s.OldUnready + s.NewReady + s.NewUnready
}

// Available 返回快照时刻的可用数 a+cs。
func (s State) Available() int64 {
	return s.OldReady + s.StableReady
}

// New 创建计划器。配置非法时返回 ErrInvalidConfig，且不返回实例。
func New(N int64, PS int, PU int, MR int64, P int) (*Planner, error) {
	if N < 1 || N > 1_000_000 ||
		PS < 0 || PS > 100 ||
		PU < 0 || PU > 100 ||
		MR < 0 || MR > 1_000_000_000_000 ||
		P < 1 || P > 1000 {
		return nil, ErrInvalidConfig
	}
	s := ceilDiv(N*int64(PS), 100)
	u := (N * int64(PU)) / 100
	if s == 0 && u == 0 {
		u = 1
	}
	return &Planner{
		n:      N,
		s:      s,
		u:      u,
		mr:     MR,
		p:      int64(P),
		maxNow: 0,
		a:      N,
	}, nil
}

// Step 基于调用开始时的状态一次算出新建、清理、缩减三个量并同时生效。
func (p *Planner) Step(now int64) (StepResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := checkCommon(now, p.maxNow); err != nil {
		return StepResult{}, err
	}

	total := p.a + p.b + p.c + p.d
	avail := p.a + p.stableLocked(now)

	up := max64(0, min64(p.n+p.s-total, p.n-(p.c+p.d)))
	r1 := p.b
	r2 := min64(p.a, max64(0, avail-(p.n-p.u)))

	p.d += up
	p.b -= r1
	p.a -= r2
	p.maxNow = now

	moved := up+r1+r2 > 0
	if p.doneLocked(now) {
		p.stall = 0
	} else if moved {
		p.stall = 0
	} else {
		p.stall++
	}
	return StepResult{Up: up, Cleaned: r1, Removed: r2, Stalled: p.stall >= p.p}, nil
}

// NewReady 使 k 个新版本未就绪实例变为就绪，记为 readyAt=now 的一批。
func (p *Planner) NewReady(k, now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := checkCommon2(k, now, p.maxNow); err != nil {
		return err
	}
	if k > p.d {
		return ErrNoUnreadyNew
	}
	p.d -= k
	p.c += k
	p.addBatch(now, k)
	p.maxNow = now
	p.stall = 0
	return nil
}

// NewFail 使 k 个新版本就绪实例按 readyAt 大者先退回未就绪。
func (p *Planner) NewFail(k, now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := checkCommon2(k, now, p.maxNow); err != nil {
		return err
	}
	if k > p.c {
		return ErrNoReadyNew
	}
	remaining := k
	for i := len(p.batches) - 1; i >= 0 && remaining > 0; i-- {
		take := min64(p.batches[i].count, remaining)
		p.batches[i].count -= take
		remaining -= take
	}
	// 末尾可能出现空批（被扣光的最新批），从后向前压缩。
	for len(p.batches) > 0 && p.batches[len(p.batches)-1].count == 0 {
		p.batches = p.batches[:len(p.batches)-1]
	}
	p.c -= k
	p.d += k
	p.maxNow = now
	return nil
}

// OldUnready 使 k 个旧版本就绪实例变为未就绪。
func (p *Planner) OldUnready(k, now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := checkCommon2(k, now, p.maxNow); err != nil {
		return err
	}
	if k > p.a {
		return ErrNoReadyOld
	}
	p.a -= k
	p.b += k
	p.maxNow = now
	return nil
}

// Done 当且仅当旧实例清空、新版本就绪数为 N 且全部稳定。
func (p *Planner) Done() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doneLocked(p.maxNow)
}

// Snapshot 返回当前状态快照；查询不会被拒绝。
func (p *Planner) Snapshot(now int64) State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return State{
		OldReady:    p.a,
		OldUnready:  p.b,
		NewReady:    p.c,
		NewUnready:  p.d,
		StableReady: p.stableLocked(now),
		StallCount:  p.stall,
		MaxNow:      p.maxNow,
		Done:        p.doneLocked(p.maxNow),
	}
}

func (p *Planner) doneLocked(now int64) bool {
	return p.a+p.b == 0 && p.c == p.n && p.stableLocked(now) == p.n
}

// stableLocked 返回在 now 时刻已稳定（now-readyAt >= MR）的新版本就绪数。
// MR=0 时所有就绪批都稳定。
func (p *Planner) stableLocked(now int64) int64 {
	var cs int64
	for _, bt := range p.batches {
		if now-bt.readyAt >= p.mr {
			cs += bt.count
		}
	}
	return cs
}

func (p *Planner) addBatch(readyAt, count int64) {
	for i := range p.batches {
		if p.batches[i].readyAt == readyAt {
			p.batches[i].count += count
			return
		}
	}
	p.batches = append(p.batches, batch{})
	i := len(p.batches) - 1
	for i > 0 && p.batches[i-1].readyAt > readyAt {
		p.batches[i] = p.batches[i-1]
		i--
	}
	p.batches[i] = batch{readyAt: readyAt, count: count}
}

func checkCommon(now, maxNow int64) error {
	if now < 0 {
		return ErrInvalidArg
	}
	if now < maxNow {
		return ErrClockRewind
	}
	return nil
}

func checkCommon2(k, now, maxNow int64) error {
	if k < 1 || now < 0 {
		return ErrInvalidArg
	}
	if now < maxNow {
		return ErrClockRewind
	}
	return nil
}

func ceilDiv(x, y int64) int64 {
	return (x + y - 1) / y
}

func min64(x, y int64) int64 {
	if x < y {
		return x
	}
	return y
}

func max64(x, y int64) int64 {
	if x > y {
		return x
	}
	return y
}
