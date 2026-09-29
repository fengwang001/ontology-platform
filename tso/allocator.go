package tso

import (
	"log"
	"os"
	"sync"
	"time"
)

// Options 构造分配器的参数。
type Options struct {
	L int64 // 每个物理毫秒内逻辑计数取值个数（0..L-1）
	W int64 // 每次续写的上界窗口（毫秒）
}

// Clock 返回当前物理毫秒；可注入以保证重放确定性。
type Clock func() int64

// ExtendFault 注入续写故障：返回 true 时本次持久化视为失败。
type ExtendFault func(term int64, high int64) bool

// Allocator 是单个节点上的全局时间戳分配器。
type Allocator struct {
	mu      sync.Mutex
	store   Store
	logger  *log.Logger
	clock   Clock
	fault   ExtendFault
	l       int64
	w       int64
	leader  bool
	term    int64
	high    int64 // 已持久化的物理部分排他上界
	phys    int64 // 本节点上次发放使用的物理部分
	logical int64 // 本物理毫秒内下一个可用逻辑计数
}

// New 创建分配器（初始为从节点）。
func New(store Store, opts Options, clock Clock, fault ExtendFault, logger *log.Logger) (*Allocator, error) {
	if opts.L <= 0 || opts.W <= 0 {
		return nil, ErrInvalidConfig
	}
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	if logger == nil {
		logger = defaultLogger()
	}
	return &Allocator{
		store:  store,
		logger: logger,
		clock:  clock,
		fault:  fault,
		l:      opts.L,
		w:      opts.W,
	}, nil
}

// IsLeader 报告当前是否为主节点。
func (a *Allocator) IsLeader() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.leader
}

// Term 返回当前已知任期（从节点为其最后任期）。
func (a *Allocator) Term() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.term
}

// Allocate 批量发放 n 个严格连续递增的时间戳。
func (a *Allocator) Allocate(n int64) ([]Timestamp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.leader {
		a.logger.Printf("Allocate n=%d -> 拒绝: %v (term=%d)", n, ErrNotLeader, a.term)
		return nil, ErrNotLeader
	}
	if n <= 0 || n > a.l {
		a.logger.Printf("Allocate n=%d -> 拒绝: %v (L=%d)", n, ErrInvalidCount, a.l)
		return nil, ErrInvalidCount
	}

	// 物理部分取本地时钟与上次物理部分的较大者，吸收时钟回拨。
	now := a.clock()
	if now > a.phys {
		a.phys = now
		a.logical = 0
	}
	// 一次请求的 n 个须在同一毫秒内连续；本毫秒剩余不足则整体移到下一毫秒。
	if a.l-a.logical < n {
		a.phys++
		a.logical = 0
	}

	// 剩余（到持久化上界的毫秒数）不足 W 的一半时先续写。
	remaining := a.high - a.phys
	if 2*remaining < a.w {
		proposed := a.phys
		if a.high > proposed {
			proposed = a.high
		}
		proposed += a.w

		if a.fault != nil && a.fault(a.term, proposed) {
			// 持久化故障：存储未改变。不越界则照常发放，越界则本次失败。
			if a.phys >= a.high {
				a.logger.Printf("Allocate n=%d 续写故障 proposed=%d -> 失败: %v (high=%d, phys=%d)",
					n, proposed, ErrExceedsBound, a.high, a.phys)
				return nil, ErrExceedsBound
			}
			a.logger.Printf("Allocate n=%d 续写故障 proposed=%d -> 不越界，沿用旧上界 high=%d 照常发放",
				n, proposed, a.high)
		} else {
			committed, latest := a.store.CompareBound(func(current Bound) Bound {
				// 任期已被超过时存储会因 term < current.Term 拒绝本写入。
				return Bound{Term: a.term, High: proposed}
			})
			if !committed {
				// 任期被超过：立即降为从节点。
				a.leader = false
				a.term = latest.Term
				a.logger.Printf("Allocate n=%d 续写被拒(存储任期=%d) -> 立即降为从节点",
					n, latest.Term)
				return nil, ErrNotLeader
			}
			a.high = proposed
			a.logger.Printf("Allocate n=%d 续写成功 high=%d (term=%d)", n, a.high, a.term)
		}
	}

	// 发出的物理部分必须小于已持久化上界。
	if a.phys >= a.high {
		a.logger.Printf("Allocate n=%d -> 失败: %v (phys=%d, high=%d)",
			n, ErrExceedsBound, a.phys, a.high)
		return nil, ErrExceedsBound
	}

	out := make([]Timestamp, 0, n)
	for i := int64(0); i < n; i++ {
		out = append(out, Timestamp{Physical: a.phys, Logical: a.logical + i})
	}
	first := out[0]
	last := out[n-1]
	a.logical += n
	a.logger.Printf("Allocate n=%d -> %d 个 [%s .. %s] 依据: phys=max(clock,last)=%d logicalBase=%d high=%d term=%d",
		n, n, first, last, a.phys, a.logical-n, a.high, a.term)
	return out, nil
}

// TakeOver 以更大任期接任主节点（原子读后写）。
func (a *Allocator) TakeOver(term int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	var start, newHigh int64
	reject := false
	committed, latest := a.store.CompareBound(func(current Bound) Bound {
		if term <= current.Term {
			reject = true
			return current
		}
		// 起始物理部分取本地时钟与存量上界的较大者。
		start = a.clock()
		if current.High > start {
			start = current.High
		}
		newHigh = start + a.w
		return Bound{Term: term, High: newHigh}
	})
	if reject || !committed {
		a.logger.Printf("TakeOver term=%d -> 拒绝: %v (存量=%s)",
			term, ErrTermNotHigher, formatBound(latest))
		return ErrTermNotHigher
	}

	a.leader = true
	a.term = term
	a.high = newHigh
	a.phys = start
	a.logical = 0
	a.logger.Printf("TakeOver term=%d -> 成为主节点, 起点 phys=%d, 新上界 high=%d 依据: max(clock,storedHigh)+W, 存量=%s",
		term, start, newHigh, formatBound(latest))
	return nil
}

// StepDown 降为从节点。
func (a *Allocator) StepDown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.leader = false
	a.logger.Printf("StepDown term=%d -> 降为从节点", a.term)
}

func defaultLogger() *log.Logger {
	return log.New(os.Stdout, "[tso] ", log.LstdFlags|log.Lmicroseconds)
}
