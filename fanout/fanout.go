// Package fanout 并行扇出执行器：并行度上限、失败快停与重跑。
package fanout

import (
	"errors"
	"sync"

	"ontology/attempt"
	"ontology/matrix"
)

var (
	// ErrInvalidParam 参数非法（含下标越界）。
	ErrInvalidParam = matrix.ErrInvalidParam
	// ErrTooLarge 矩阵过大。
	ErrTooLarge = matrix.ErrTooLarge
	// ErrEmpty 空矩阵。
	ErrEmpty = matrix.ErrEmpty
	// ErrRerunExhausted 重跑超限。
	ErrRerunExhausted = attempt.ErrExhausted
	// ErrState 状态不符。
	ErrState = errors.New("fanout: state mismatch")
)

// State 为作业状态；本轮终结结果复用 Succeeded/Failed/Cancelled 三个值。
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

// Config 为执行器配置。
type Config struct {
	Matrix   matrix.Config
	P        int
	FailFast bool
	A        int
}

// Executor 为并行扇出执行器。所有方法可并发调用，效果等价于某个串行顺序。
type Executor struct {
	mu       sync.Mutex
	combos   []matrix.Combo
	state    []State
	runs     []int
	exp      []bool
	p        int
	failFast bool
	ledger   *attempt.Ledger
	started  bool
	done     bool
	result   State
	running  int
	pending  []int // 下标升序 FIFO
	scanned  int   // Finish 为寻找下一个待启动作业而检视的作业数
}

// New 校验配置、展开矩阵并创建执行器。拒绝次序：参数非法 > 矩阵过大 > 空矩阵。
func New(cfg Config) (*Executor, error) {
	if cfg.P < 1 || cfg.P > 256 || cfg.A < 1 || cfg.A > 5 {
		return nil, ErrInvalidParam
	}
	combos, err := matrix.Expand(cfg.Matrix)
	if err != nil {
		return nil, err
	}
	e := &Executor{
		combos:   combos,
		state:    make([]State, len(combos)),
		runs:     make([]int, len(combos)),
		exp:      make([]bool, len(combos)),
		p:        cfg.P,
		failFast: cfg.FailFast,
		ledger:   attempt.New(cfg.A),
	}
	for i, c := range combos {
		e.exp[i] = c.Experimental()
	}
	return e, nil
}

// Len 返回作业数。
func (e *Executor) Len() int { return len(e.combos) }

// Job 返回第 i 个作业的组合。
func (e *Executor) Job(i int) matrix.Combo { return e.combos[i] }

// State 返回第 i 个作业的状态。
func (e *Executor) State(i int) (State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i < 0 || i >= len(e.state) {
		return Pending, ErrInvalidParam
	}
	return e.state[i], nil
}

// Runs 返回第 i 个作业进入 Running 的次数。
func (e *Executor) Runs(i int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i < 0 || i >= len(e.runs) {
		return 0, ErrInvalidParam
	}
	return e.runs[i], nil
}

// Round 返回当前轮次。
func (e *Executor) Round() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ledger.Round()
}

// Result 返回本轮结果；bool 为假表示本轮尚未终结。
func (e *Executor) Result() (State, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.result, e.done
}

// Start 启动下标最小的 min(P, n) 个作业，只能调一次。
func (e *Executor) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return ErrState
	}
	e.started = true
	e.ledger.Begin()
	e.pending = make([]int, len(e.state))
	for i := range e.pending {
		e.pending[i] = i
	}
	e.fillLocked()
	e.checkDoneLocked()
	return nil
}

// Finish 上报第 i 个作业完成。i 须为 Running，否则按拒绝次序报错。
func (e *Executor) Finish(i int, ok bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i < 0 || i >= len(e.state) {
		return ErrInvalidParam
	}
	if e.state[i] != Running {
		return ErrState
	}
	if ok {
		e.state[i] = Succeeded
	} else {
		e.state[i] = Failed
	}
	e.running--
	if !ok && e.failFast && !e.exp[i] {
		// 非试验性失败触发快停：立即取消，无确认阶段。
		for j, s := range e.state {
			if s == Running {
				e.state[j] = Cancelled
			}
		}
		for _, j := range e.pending {
			e.state[j] = Cancelled
		}
		e.pending = e.pending[:0]
		e.running = 0
	} else if len(e.pending) > 0 {
		e.scanned++
		j := e.pending[0]
		e.pending = e.pending[1:]
		e.launchLocked(j)
	}
	e.checkDoneLocked()
	return nil
}

// CancelAll 在本轮未终结时把所有 Running 与 Pending 转 Cancelled。
func (e *Executor) CancelAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started || e.done {
		return
	}
	for i, s := range e.state {
		if s == Running || s == Pending {
			e.state[i] = Cancelled
		}
	}
	e.pending = e.pending[:0]
	e.running = 0
	e.checkDoneLocked()
}

// Rerun 重跑：须已终结且结果非 Succeeded，已用轮次达到 A 报重跑超限。
// 通过后轮次加 1，所有非 Succeeded 作业置回 Pending 并立即启动其中
// 下标最小的 min(P, 个数) 个。
func (e *Executor) Rerun() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.done || e.result == Succeeded {
		return ErrState
	}
	if e.ledger.Exhausted() {
		return ErrRerunExhausted
	}
	var reset []int
	e.pending = e.pending[:0]
	for i, s := range e.state {
		if s != Succeeded {
			e.state[i] = Pending
			e.pending = append(e.pending, i)
			reset = append(reset, i)
		}
	}
	if err := e.ledger.Commit(reset); err != nil {
		return err
	}
	e.done = false
	e.result = Pending
	e.fillLocked()
	return nil
}

func (e *Executor) launchLocked(j int) {
	e.state[j] = Running
	e.running++
	e.runs[j]++
}

// fillLocked 从队首依次启动，直到 Running 数达到 P 或没有 Pending。
func (e *Executor) fillLocked() {
	for len(e.pending) > 0 && e.running < e.p {
		j := e.pending[0]
		e.pending = e.pending[1:]
		e.launchLocked(j)
	}
}

// checkDoneLocked 在没有 Running 与 Pending 时终结本轮并计算结果：
// 存在非试验性 Failed 为 Failed；否则存在 Cancelled 为 Cancelled；否则 Succeeded。
func (e *Executor) checkDoneLocked() {
	if e.done || !e.started || e.running > 0 || len(e.pending) > 0 {
		return
	}
	e.done = true
	res := Succeeded
	for i, s := range e.state {
		if s == Failed && !e.exp[i] {
			res = Failed
			break
		}
	}
	if res != Failed {
		for _, s := range e.state {
			if s == Cancelled {
				res = Cancelled
				break
			}
		}
	}
	e.result = res
}
