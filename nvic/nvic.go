package nvic

import (
	"fmt"
	"sync"
)

// EventKind 标识 NVIC 一次操作可能产生的事件种类。
type EventKind int

const (
	// Enter 表示从线程态进入一个中断。
	Enter EventKind = iota
	// Preempt 表示中断 A 运行时，更高组优先级的中断 B 抢占进入。
	Preempt
	// Exit 表示栈顶中断退出（活动标志清除）。
	Exit
	// TailChain 表示退出后不经线程态直接进入另一个（或同一个）中断。
	TailChain
	// Resume 表示退出后返回到仍在栈中的被抢占中断。
	Resume
	// Idle 表示退出后栈空且没有可进入的候选，CPU 回到线程态。
	Idle
)

// Event 描述一次操作产生的单个事件。IRQ 字段对 Idle 事件无意义（为 -1）。
type Event struct {
	Kind EventKind
	IRQ  int
}

// 可区分的拒绝原因。
var (
	ErrInvalidInterruptCount = nvicErr("nvic: interrupt count M must be >= 1")
	ErrInvalidSubPriority    = nvicErr("nvic: sub-priority bits s must be in [0,7]")
	ErrIRQOutOfRange         = nvicErr("nvic: interrupt number out of range")
	ErrInvalidPriority       = nvicErr("nvic: priority must be in [0,255]")
	ErrInvalidBase           = nvicErr("nvic: base priority must be in [0,255]")
	ErrReturnFromThread      = nvicErr("nvic: Return called while stack is empty")
)

type nvicErr string

func (e nvicErr) Error() string { return string(e) }

// NVIC 是嵌套向量中断控制器模型。零值不可用，请用 New 构造。
//
// 所有方法均在内部互斥锁保护下执行，可被并发调用；并发执行的结果
// 等价于某个串行顺序。
type NVIC struct {
	mu     sync.Mutex
	m      int     // 中断数量
	s      int     // 子优先级位数
	enable []bool  // 使能标志
	pend   []bool  // 挂起标志
	active []bool  // 活动标志
	prio   []uint8 // 8 位优先级，数值小者更紧急
	base   uint8   // 屏蔽阈值，0 表示不屏蔽
	stack  []int   // 运行栈（栈顶在末尾），其中中断无重复
}

// New 构造一个包含 M 个中断、子优先级占 s 位的 NVIC。
// 初始时所有标志为假、所有优先级为 0、阈值为 0、CPU 处于线程态。
func New(m int, s int) (*NVIC, error) {
	if m < 1 {
		return nil, ErrInvalidInterruptCount
	}
	if s < 0 || s > 7 {
		return nil, ErrInvalidSubPriority
	}
	return &NVIC{
		m:      m,
		s:      s,
		enable: make([]bool, m),
		pend:   make([]bool, m),
		active: make([]bool, m),
		prio:   make([]uint8, m),
	}, nil
}

// checkIRQ 校验中断编号，必须在持锁状态下调用。
func (n *NVIC) checkIRQ(irq int) error {
	if irq < 0 || irq >= n.m {
		return ErrIRQOutOfRange
	}
	return nil
}

// group 返回某中断此刻的组优先级 g(p)=p>>s。
func (n *NVIC) group(irq int) int { return int(n.prio[irq]) >> n.s }

// sub 返回某中断此刻的子优先级（p 的低 s 位）。
func (n *NVIC) sub(irq int) int { return int(n.prio[irq]) & (1<<n.s - 1) }

// masked 报告组优先级为 g 的中断是否被阈值屏蔽。
// base 为 0 时不屏蔽；否则组优先级不小于 g(base) 的中断被屏蔽。
func (n *NVIC) masked(g int) bool {
	return n.base != 0 && g >= int(n.base)>>n.s
}

// candidate 返回当前候选：使能、挂起、非活动且未被屏蔽的中断中，
// 按（组优先级，子优先级，编号）字典序最小者。无候选时 ok 为假。
func (n *NVIC) candidateLocked() (irq int, ok bool) {
	best := -1
	for i := 0; i < n.m; i++ {
		if !n.enable[i] || !n.pend[i] || n.active[i] {
			continue
		}
		gi := n.group(i)
		if n.masked(gi) {
			continue
		}
		if best < 0 {
			best = i
			continue
		}
		gb := n.group(best)
		if gi < gb ||
			(gi == gb && (n.sub(i) < n.sub(best) ||
				(n.sub(i) == n.sub(best) && i < best))) {
			best = i
		}
	}
	return best, best >= 0
}

// runningLevel 返回当前运行级：栈顶中断的当前组优先级；
// 栈空时 ok 为假（表示无穷大，任何候选都可进入）。
func (n *NVIC) runningLevelLocked() (level int, ok bool) {
	if len(n.stack) == 0 {
		return 0, false
	}
	return n.group(n.stack[len(n.stack)-1]), true
}

// enter 使候选进入：清挂起、置活动、压栈，并追加相应事件。
// 栈非空时事件标记为抢占（Preempt），否则为 Enter。
func (n *NVIC) enterLocked(irq int, evs []Event) []Event {
	n.pend[irq] = false
	n.active[irq] = true
	kind := Enter
	if len(n.stack) > 0 {
		kind = Preempt
	}
	n.stack = append(n.stack, irq)
	return append(evs, Event{Kind: kind, IRQ: irq})
}

// dispatch 在非 Return 操作施加后执行一次调度：
// 候选存在且其组优先级严格小于当前运行级（栈空时为无穷大）则进入。
func (n *NVIC) dispatchLocked(evs []Event) []Event {
	cand, ok := n.candidateLocked()
	if !ok {
		return evs
	}
	level, running := n.runningLevelLocked()
	if !running || n.group(cand) < level {
		evs = n.enterLocked(cand, evs)
	}
	return evs
}

// Enable 使能某中断，并立即调度一次。
func (n *NVIC) Enable(irq int) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkIRQ(irq); err != nil {
		return nil, err
	}
	n.enable[irq] = true
	return n.dispatchLocked(nil), nil
}

// Disable 屏蔽某中断。已活动的中断不受影响；屏蔽只阻止后续进入。
func (n *NVIC) Disable(irq int) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkIRQ(irq); err != nil {
		return nil, err
	}
	n.enable[irq] = false
	return []Event{}, nil
}

// Pend 将中断置为挂起（活动中的中断也可被挂起），并立即调度。
func (n *NVIC) Pend(irq int) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkIRQ(irq); err != nil {
		return nil, err
	}
	n.pend[irq] = true
	return n.dispatchLocked(nil), nil
}

// Clear 取消某中断的挂起。已活动的中断不受影响。
func (n *NVIC) Clear(irq int) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkIRQ(irq); err != nil {
		return nil, err
	}
	n.pend[irq] = false
	return []Event{}, nil
}

// SetPriority 设置中断的 8 位优先级（数值小者更紧急），并立即调度。
// 判定一律使用各中断此刻的当前优先级。
func (n *NVIC) SetPriority(irq, priority int) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkIRQ(irq); err != nil {
		return nil, err
	}
	if priority < 0 || priority > 255 {
		return nil, ErrInvalidPriority
	}
	n.prio[irq] = uint8(priority)
	return n.dispatchLocked(nil), nil
}

// SetBase 设置屏蔽阈值（0 到 255，0 表示不屏蔽），并立即调度。
func (n *NVIC) SetBase(base int) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if base < 0 || base > 255 {
		return nil, ErrInvalidBase
	}
	n.base = uint8(base)
	return n.dispatchLocked(nil), nil
}

// Return 令栈顶中断退出（活动清除，产生 Exit），随后：
// 候选存在且其组优先级严格小于新栈顶运行级（栈空时任何候选都可）时，
// 不经线程态直接进入该候选（TailChain，可尾链进入它自己）；
// 否则栈非空产生 Resume（新栈顶），栈空产生 Idle。
// 栈为空（处于线程态）时整体拒绝且不改变任何状态。
func (n *NVIC) Return() ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.stack) == 0 {
		return nil, ErrReturnFromThread
	}
	top := n.stack[len(n.stack)-1]
	n.stack = n.stack[:len(n.stack)-1]
	n.active[top] = false
	evs := []Event{{Kind: Exit, IRQ: top}}

	cand, ok := n.candidateLocked()
	level, running := n.runningLevelLocked()
	if ok && (!running || n.group(cand) < level) {
		evs = append(evs, Event{Kind: TailChain, IRQ: cand})
		n.pend[cand] = false
		n.active[cand] = true
		n.stack = append(n.stack, cand)
		return evs, nil
	}
	if running {
		evs = append(evs, Event{Kind: Resume, IRQ: n.stack[len(n.stack)-1]})
	} else {
		evs = append(evs, Event{Kind: Idle, IRQ: -1})
	}
	return evs, nil
}

// ---- 查询（加锁，返回副本） ----

// Enabled 返回使能标志。
func (n *NVIC) Enabled(irq int) (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkIRQ(irq); err != nil {
		return false, err
	}
	return n.enable[irq], nil
}

// Pending 返回挂起标志。
func (n *NVIC) Pending(irq int) (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkIRQ(irq); err != nil {
		return false, err
	}
	return n.pend[irq], nil
}

// Active 返回活动标志。
func (n *NVIC) Active(irq int) (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkIRQ(irq); err != nil {
		return false, err
	}
	return n.active[irq], nil
}

// Priority 返回当前 8 位优先级。
func (n *NVIC) Priority(irq int) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkIRQ(irq); err != nil {
		return 0, err
	}
	return int(n.prio[irq]), nil
}

// Base 返回当前屏蔽阈值（0 表示不屏蔽）。
func (n *NVIC) Base() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return int(n.base)
}

// GroupPriority 返回 g(p)=p>>s。
func (n *NVIC) GroupPriority(irq int) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkIRQ(irq); err != nil {
		return 0, err
	}
	return n.group(irq), nil
}

// SubPriority 返回 p 的低 s 位。
func (n *NVIC) SubPriority(irq int) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkIRQ(irq); err != nil {
		return 0, err
	}
	return n.sub(irq), nil
}

// Stack 返回运行栈的副本（栈底在前、栈顶在末尾）。
func (n *NVIC) Stack() []int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]int(nil), n.stack...)
}

// RunningLevel 返回栈顶中断的当前组优先级；栈空时 ok 为假（表示线程态）。
func (n *NVIC) RunningLevel() (level int, ok bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.runningLevelLocked()
}

// String 返回事件的可读表示，如 Enter(2)、Preempt(0)、TailChain(3)、
// Resume(1)、Exit(0)、Idle。
func (e Event) String() string {
	switch e.Kind {
	case Enter:
		return fmt.Sprintf("Enter(%d)", e.IRQ)
	case Preempt:
		return fmt.Sprintf("Preempt(%d)", e.IRQ)
	case Exit:
		return fmt.Sprintf("Exit(%d)", e.IRQ)
	case TailChain:
		return fmt.Sprintf("TailChain(%d)", e.IRQ)
	case Resume:
		return fmt.Sprintf("Resume(%d)", e.IRQ)
	default:
		return "Idle"
	}
}
