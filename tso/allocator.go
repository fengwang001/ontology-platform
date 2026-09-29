package tso

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Allocator 是主节点切换下的全局时间戳分配器。
type Allocator struct {
	mu    sync.Mutex
	store Store
	L     int64
	W     int64
	clock func() int64
	log   io.Writer

	leader  bool
	term    int64
	upper   int64 // 已持久化上界（开区间）：只允许 physical < upper
	phys    int64 // 当前逻辑窗口所在的物理毫秒
	logical int64 // phys 上下一个可发的逻辑计数
}

// Option 配置 Allocator。
type Option func(*Allocator)

// WithLogger 将判定日志输出到 w（默认丢弃）。
func WithLogger(w io.Writer) Option {
	return func(a *Allocator) { a.log = w }
}

// WithClock 注入物理毫秒时钟（默认本地墙钟）。注入固定/脚本时钟
// 可保证相同输入序列重放出相同结果。
func WithClock(f func() int64) Option {
	return func(a *Allocator) { a.clock = f }
}

// New 创建 L 个逻辑计数/毫秒、续写窗口 W 毫秒的分配器。
// L 或 W 非正时返回 ErrInvalidConfig。
func New(L, W int64, store Store, opts ...Option) (*Allocator, error) {
	if L <= 0 || W <= 0 {
		return nil, fmt.Errorf("%w: L=%d W=%d (二者必须为正)", ErrInvalidConfig, L, W)
	}
	if store == nil {
		return nil, fmt.Errorf("%w: store 为 nil", ErrInvalidConfig)
	}
	a := &Allocator{
		store: store,
		L:     L,
		W:     W,
		clock: func() int64 { return time.Now().UnixMilli() },
		log:   io.Discard,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// IsLeader 报告当前是否为主节点。
func (a *Allocator) IsLeader() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.leader
}

// Term 返回当前任期（任期；从节点为 0）。
func (a *Allocator) Term() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.term
}

// TakeOver 以原子读后写接任：newTerm 必须严格大于存量任期。
func (a *Allocator) TakeOver(ctx context.Context, newTerm int64, nowMs int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	rec, err := a.store.Read(ctx)
	if err != nil {
		a.logf("接任拒绝: term=%d 读取共享存储失败: %v", newTerm, err)
		return err
	}
	if newTerm <= rec.Term {
		a.logf("接任拒绝: 新任期 %d 不大于存量任期 %d → %v", newTerm, rec.Term, ErrStaleTerm)
		return fmt.Errorf("%w: newTerm=%d storedTerm=%d", ErrStaleTerm, newTerm, rec.Term)
	}

	// 起点取本地时钟与存量上界的较大者：旧主至多发出 upper-1 毫秒，
	// 从 upper 起步保证新主首个时间戳即大于旧主可能发出的一切值，
	// 即便新主时钟落后于旧主或发生回拨。
	start := nowMs
	if rec.Upper > start {
		start = rec.Upper
	}
	newUpper := start + a.W
	next := BoundRecord{Term: newTerm, Upper: newUpper}
	if err := a.store.Write(ctx, newTerm, next); err != nil {
		a.logf("接任失败: term=%d start=%d upper=%d 原子写入被拒: %v", newTerm, start, newUpper, err)
		return err
	}

	// 先持久化上界、再在内存就位：失败时本地不产生任何主节点状态。
	a.leader = true
	a.term = newTerm
	a.upper = newUpper
	a.phys = start
	a.logical = 0
	a.logf("接任成功: term=%d 存量=(%d,%d) 本地时钟=%d 起点=max(%d,%d)=%d 新上界=%d+%d=%d",
		newTerm, rec.Term, rec.Upper, nowMs, nowMs, rec.Upper, start, start, a.W, newUpper)
	return nil
}

// Allocate 一次性分配 n 个同一毫秒内连续的时间戳。
func (a *Allocator) Allocate(ctx context.Context, n int64) ([]Timestamp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.leader {
		a.logf("发放拒绝: 从节点收到请求 n=%d → %v", n, ErrNotLeader)
		return nil, ErrNotLeader
	}
	if n <= 0 || n > a.L {
		a.logf("发放拒绝: term=%d n=%d 非法（要求 1..L=%d）→ %v", a.term, n, a.L, ErrInvalidCount)
		return nil, fmt.Errorf("%w: n=%d L=%d", ErrInvalidCount, n, a.L)
	}

	now := a.clock()

	// 选择本批的物理毫秒：物理部分单调，取本地时钟与上次物理部分的较大者；
	// 上个毫秒的逻辑计数已用尽则先加一。
	p := a.phys
	startLog := a.logical
	if startLog >= a.L {
		p++
		startLog = 0
	}
	if now > p {
		p = now
		startLog = 0
	}

	// 一次请求的 n 个必须在同一毫秒内连续：本毫秒剩余不足则整批移到下一毫秒。
	if a.L-startLog < n {
		prevPhys := p
		remain := a.L - startLog
		p++
		startLog = 0
		a.logf("发放判定: term=%d 毫秒 %d 剩余逻辑计数 %d < n=%d，整批移至下一毫秒 %d",
			a.term, prevPhys, remain, n, p)
	}

	// 发出的物理部分必须严格小于已持久化上界；剩余不足 W 的一半时
	// 先以 max(当前物理部分, 上界)+W 续写。
	reason := ""
	switch {
	case p >= a.upper:
		reason = "已抵达/越过已持久化上界，必须续写"
	case 2*(a.upper-p) < a.W:
		reason = "剩余窗口不足 W 的一半，提前续写"
	}
	if reason != "" {
		base := p
		if a.upper > base {
			base = a.upper
		}
		newUpper := base + a.W
		err := a.store.Write(ctx, a.term, BoundRecord{Term: a.term, Upper: newUpper})
		switch {
		case err == nil:
			a.logf("续写成功: term=%d %s: 新上界=max(%d,%d)+%d=%d",
				a.term, reason, p, a.upper, a.W, newUpper)
			a.upper = newUpper
		case errors.Is(err, ErrTermSuperseded):
			// 任期被更高主节点超过：立即降为从节点，本次请求失败且不消耗。
			a.leader = false
			a.term = 0
			a.phys = 0
			a.logical = 0
			a.upper = 0
			a.logf("续写判定: term 被存储中的更高任期超过 → 立即降级为从节点，本次不发放: %v", err)
			return nil, err
		default:
			// 普通持久化故障：尚未越界则照常发放，越界则本次失败。
			if p < a.upper {
				a.logf("续写失败但不越界: term=%d %v；物理部分 %d < 上界 %d，照常发放",
					a.term, err, p, a.upper)
			} else {
				a.logf("续写失败且越界: term=%d %v；物理部分 %d >= 上界 %d → 本次失败，不消耗",
					a.term, err, p, a.upper)
				return nil, fmt.Errorf("%w: physical=%d upper=%d: %v", ErrPersistUnavailable, p, a.upper, err)
			}
		}
	}

	out := make([]Timestamp, n)
	for i := int64(0); i < n; i++ {
		out[i] = Timestamp{Physical: p, Logical: startLog + i}
	}

	a.phys = p
	a.logical = startLog + n
	a.logf("发放成功: term=%d n=%d 本地时钟=%d 物理=%d 逻辑=[%d,%d] 输出=%v",
		a.term, n, now, p, startLog, startLog+n-1, out)
	return out, nil
}

func (a *Allocator) logf(format string, args ...any) {
	fmt.Fprintf(a.log, "[tso] "+format+"\n", args...)
}
