// Package fanout 单轮并行扇出执行：并行度上限与失败快停。
// 所有方法可并发调用，效果等价于某个串行顺序。
package fanout

import (
	"errors"
	"fmt"
	"sync"
)

// 错误哨兵，均可用 errors.Is 区分。被拒绝的操作不改任何状态。
var (
	ErrParam = errors.New("fanout: invalid argument") // 参数非法（下标越界）
	ErrState = errors.New("fanout: state mismatch")   // 状态不符
)

// State 为作业状态。
type State int

const (
	Pending State = iota
	Running
	Succeeded
	Failed
	Cancelled
)

func (s State) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Running:
		return "Running"
	case Succeeded:
		return "Succeeded"
	case Failed:
		return "Failed"
	case Cancelled:
		return "Cancelled"
	}
	return "Unknown"
}

// Result 为一轮终结后的结果。
type Result int

const (
	ResultSucceeded Result = iota
	ResultFailed
	ResultCancelled
)

func (r Result) String() string {
	switch r {
	case ResultSucceeded:
		return "Succeeded"
	case ResultFailed:
		return "Failed"
	case ResultCancelled:
		return "Cancelled"
	}
	return "Unknown"
}

// Executor 为单轮扇出执行器。Pending 作业的下标按升序保存在 queue 中，
// Finish 寻找下一个待启动作业只检视队首，至多 1 个作业（scanned 计数）。
type Executor struct {
	mu           sync.Mutex
	states       []State
	experimental []bool
	runs         []int
	queue        []int
	p            int
	failFast     bool
	started      bool
	running      int
	scanned      int
}

// NewExecutor 构造执行器；参数须已由调用方校验（n>=1，1<=p<=256，
// len(experimental)==n）。
func NewExecutor(n int, experimental []bool, p int, failFast bool) *Executor {
	queue := make([]int, n)
	for i := range queue {
		queue[i] = i
	}
	return &Executor{
		states:       make([]State, n),
		experimental: experimental,
		runs:         make([]int, n),
		queue:        queue,
		p:            p,
		failFast:     failFast,
	}
}

// N 返回作业数。
func (e *Executor) N() int { return len(e.states) }

// Start 启动下标最小的 min(P, n) 个作业；只能调用一次。
func (e *Executor) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return fmt.Errorf("%w: Start called twice", ErrState)
	}
	e.started = true
	k := min(e.p, len(e.states))
	for i := 0; i < k; i++ {
		e.startLocked(i)
	}
	e.queue = e.queue[k:]
	return nil
}

func (e *Executor) startLocked(i int) {
	e.states[i] = Running
	e.runs[i]++
	e.running++
}

// Finish 上报作业 i 的结果。拒绝次序：参数非法（下标越界）> 状态不符。
// 非试验性作业失败且 failFast 为真时，其余 Running 与 Pending 立即转
// Cancelled；否则启动下标最小的 Pending（若有）。
func (e *Executor) Finish(i int, ok bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i < 0 || i >= len(e.states) {
		return fmt.Errorf("%w: job index %d out of range", ErrParam, i)
	}
	if e.states[i] != Running {
		return fmt.Errorf("%w: job %d is %s", ErrState, i, e.states[i])
	}
	if ok {
		e.states[i] = Succeeded
	} else {
		e.states[i] = Failed
	}
	e.running--
	if !ok && e.failFast && !e.experimental[i] {
		for j := range e.states {
			if e.states[j] == Running {
				e.states[j] = Cancelled
				e.running--
			}
		}
		for _, j := range e.queue {
			e.states[j] = Cancelled
		}
		e.queue = nil
		return nil
	}
	e.tryStartLocked()
	return nil
}

// tryStartLocked 启动下标最小的 Pending（队首）；只检视 1 个作业。
func (e *Executor) tryStartLocked() {
	if len(e.queue) == 0 || e.running >= e.p {
		return
	}
	e.scanned++
	i := e.queue[0]
	e.queue = e.queue[1:]
	e.startLocked(i)
}

// CancelAll 在已启动且未终结时把所有 Running 与 Pending 转 Cancelled，
// 其余情形为无操作。
func (e *Executor) CancelAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started || (e.running == 0 && len(e.queue) == 0) {
		return
	}
	for i := range e.states {
		if e.states[i] == Running {
			e.states[i] = Cancelled
			e.running--
		}
	}
	for _, i := range e.queue {
		e.states[i] = Cancelled
	}
	e.queue = nil
}

// Terminal 报告本轮是否终结（没有 Running 与 Pending）。
func (e *Executor) Terminal() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.terminalLocked()
}

func (e *Executor) terminalLocked() bool {
	return e.started && e.running == 0 && len(e.queue) == 0
}

// Result 返回本轮结果，仅在终结后有定义：存在非试验性 Failed 为
// ResultFailed；否则存在 Cancelled 为 ResultCancelled；否则 ResultSucceeded。
func (e *Executor) Result() Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resultLocked()
}

func (e *Executor) resultLocked() Result {
	cancelled := false
	for i, s := range e.states {
		if s == Failed && !e.experimental[i] {
			return ResultFailed
		}
		if s == Cancelled {
			cancelled = true
		}
	}
	if cancelled {
		return ResultCancelled
	}
	return ResultSucceeded
}

// ResetForRerun 把所有非 Succeeded 的作业置回 Pending，并立即启动其中
// 下标最小的 min(P, 个数) 个。调用方须保证本轮已终结且结果非 Succeeded。
func (e *Executor) ResetForRerun() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.queue = e.queue[:0]
	for i := range e.states {
		if e.states[i] != Succeeded {
			e.states[i] = Pending
			e.queue = append(e.queue, i)
		}
	}
	e.running = 0
	k := min(e.p, len(e.queue))
	for _, i := range e.queue[:k] {
		e.startLocked(i)
	}
	e.queue = e.queue[k:]
}

// State 返回作业 i 的当前状态。
func (e *Executor) State(i int) State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.states[i]
}

// Runs 返回作业 i 进入 Running 的次数。
func (e *Executor) Runs(i int) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runs[i]
}

// Scanned 返回 Finish 路径累计检视的作业数（用于验证与 n 无关）。
func (e *Executor) Scanned() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.scanned
}
