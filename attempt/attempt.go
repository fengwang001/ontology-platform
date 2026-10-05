// Package attempt 多轮次执行与重跑账：在 fanout 单轮执行之上记录轮次
// 上限与每个作业进入 Running 的次数。所有方法可并发调用，效果等价于
// 某个串行顺序。
package attempt

import (
	"errors"
	"fmt"
	"sync"

	"ontology/fanout"
	"ontology/matrix"
)

// ErrRerunLimit 为重跑超限错误，可用 errors.Is 区分。
var ErrRerunLimit = errors.New("attempt: rerun limit exceeded")

// Config 为执行器配置：矩阵展开配置加并行度、失败快停与最多轮次。
type Config struct {
	Matrix   matrix.Config
	P        int
	FailFast bool
	A        int
}

// Executor 为顶层执行器。
type Executor struct {
	mu     sync.Mutex
	fo     *fanout.Executor
	jobs   []matrix.Job
	a      int
	rounds int
}

// New 校验配置并构造执行器。拒绝次序：参数非法 > 矩阵过大 > 空矩阵。
func New(cfg Config) (*Executor, error) {
	if cfg.P < 1 || cfg.P > 256 {
		return nil, fmt.Errorf("%w: P=%d", matrix.ErrInvalid, cfg.P)
	}
	if cfg.A < 1 || cfg.A > 5 {
		return nil, fmt.Errorf("%w: A=%d", matrix.ErrInvalid, cfg.A)
	}
	m, err := matrix.New(cfg.Matrix)
	if err != nil {
		return nil, err
	}
	experimental := make([]bool, len(m.Jobs))
	for i, j := range m.Jobs {
		experimental[i] = j.Experimental
	}
	return &Executor{
		fo:   fanout.NewExecutor(len(m.Jobs), experimental, cfg.P, cfg.FailFast),
		jobs: m.Jobs,
		a:    cfg.A,
	}, nil
}

// Start 启动第一轮；只能调用一次。
func (e *Executor) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.fo.Start(); err != nil {
		return err
	}
	e.rounds++
	return nil
}

// Finish 上报作业 i 的结果。拒绝次序：参数非法 > 状态不符。
func (e *Executor) Finish(i int, ok bool) error { return e.fo.Finish(i, ok) }

// CancelAll 在已启动且未终结时取消所有 Running 与 Pending。
func (e *Executor) CancelAll() { e.fo.CancelAll() }

// Rerun 开启新一轮。拒绝次序：状态不符（未终结或结果为 Succeeded）>
// 重跑超限。通过后轮次加 1，所有非 Succeeded 的作业置回 Pending 并
// 立即启动其中下标最小的 min(P, 个数) 个。
func (e *Executor) Rerun() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.fo.Terminal() || e.fo.Result() == fanout.ResultSucceeded {
		return fmt.Errorf("%w: cannot rerun now", fanout.ErrState)
	}
	if e.rounds >= e.a {
		return ErrRerunLimit
	}
	e.rounds++
	e.fo.ResetForRerun()
	return nil
}

// Terminal 报告本轮是否终结。
func (e *Executor) Terminal() bool { return e.fo.Terminal() }

// Result 返回本轮结果，仅在终结后有定义。
func (e *Executor) Result() fanout.Result { return e.fo.Result() }

// State 返回作业 i 的当前状态。
func (e *Executor) State(i int) fanout.State { return e.fo.State(i) }

// Runs 返回作业 i 进入 Running 的次数。
func (e *Executor) Runs(i int) int { return e.fo.Runs(i) }

// Rounds 返回已用轮次。
func (e *Executor) Rounds() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rounds
}

// Jobs 返回展开后的作业列表。
func (e *Executor) Jobs() []matrix.Job { return e.jobs }

// N 返回作业数。
func (e *Executor) N() int { return len(e.jobs) }
