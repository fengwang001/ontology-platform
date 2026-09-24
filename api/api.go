// Package api 是对外门面：位点推进、两种模式的一致性读与内置自检。
package api

import (
	"ontology/fresh"
	"ontology/lag"
)

// 三类可判定的哨兵错误，互不相同。
var (
	ErrCommitGap   = fresh.ErrCommitGap
	ErrApplyRange  = fresh.ErrApplyRange
	ErrNegativeLag = lag.ErrNegativeLag
)

// 读模式与结果类型，直接复用 lag 包的定义。
type (
	Mode   = lag.Mode
	Result = lag.Result
)

const (
	Downgrade = lag.Downgrade
	Block     = lag.Block
)

// Engine 是一致性读引擎，并发安全。
type Engine struct {
	r *lag.Reader
}

// New 构造初始 H=-1, A=-1 的引擎。
func New() *Engine { return &Engine{r: lag.NewReader(fresh.New())} }

// Commit 推进 H 到 n（n 必须等于 H+1）。
func (e *Engine) Commit(n int64) error { return e.r.Commit(n) }

// Apply 推进 A 到 n（必须 A < n <= H），并放行阻塞等待者。
func (e *Engine) Apply(n int64) error { return e.r.Apply(n) }

// Read 按模式读，T = H - lag 在调用时刻冻结。
func (e *Engine) Read(lagv int64, m Mode) (Result, error) { return e.r.Read(lagv, m) }

// Head 返回当前 H。
func (e *Engine) Head() int64 { return e.r.Head() }

// Applied 返回当前 A。
func (e *Engine) Applied() int64 { return e.r.Applied() }

// SelfCheck 在内置操作序列上核验四条不变量；全部通过返回 nil。
// 只操作内部新建的实例，可并发调用。
func (e *Engine) SelfCheck() error {
	if err := checkNaiveAndMonotonic(); err != nil {
		return err
	}
	if err := checkBlock(); err != nil {
		return err
	}
	return checkFailureNoTrace()
}

// 不变量 1（与朴素参照一致）与 2（单调）。
func checkNaiveAndMonotonic() error {
	g := New()
	prevH, prevA := g.Head(), g.Applied()
	for i := int64(0); i < 200; i++ {
		if err := g.Commit(i); err != nil || g.Head() != prevH+1 {
			return errOr("不变量2: Commit 未严格 +1", err)
		}
		if i%3 == 0 { // 每三步把 A 推进到 H
			if err := g.Apply(i); err != nil {
				return errOr("不变量2: Apply 失败", err)
			}
		}
		h, a := g.Head(), g.Applied()
		if h < prevH || a < prevA || a > h {
			return errOr("不变量2: 单调性或 A<=H 被破坏", nil)
		}
		prevH, prevA = h, a
		for lg := int64(0); lg <= 3; lg++ {
			res, err := g.Read(lg, Downgrade)
			if err != nil || res.Pos != a || res.Downgraded != (a < h-lg) {
				return errOr("不变量1: 与朴素参照不一致", err)
			}
		}
	}
	return nil
}

// 不变量 3（阻塞不滞后）。
func checkBlock() error {
	g := New()
	for i := int64(0); i <= 4; i++ {
		if err := g.Commit(i); err != nil {
			return err
		}
	}
	t := g.Head() - 2 // 冻结 T 的期望值
	done := make(chan Result, 1)
	go func() { res, _ := g.Read(2, Block); done <- res }()
	for n := g.Applied() + 1; n <= t; n++ {
		if err := g.Apply(n); err != nil {
			return err
		}
	}
	res := <-done
	if res.Pos != g.Applied() || res.Pos < t || res.Downgraded {
		return errOr("不变量3: 阻塞返回后 Applied < T", nil)
	}
	return nil
}

// 不变量 4（失败不留痕）。
func checkFailureNoTrace() error {
	g := New()
	if err := g.Commit(0); err != nil {
		return err
	}
	h, a := g.Head(), g.Applied()
	bads := []error{g.Commit(2), g.Apply(-1), g.Apply(1)}
	if _, err := g.Read(-1, Downgrade); err != ErrNegativeLag {
		return errOr("不变量4: lag<0 未报哨兵错误", nil)
	}
	for i, want := range []error{ErrCommitGap, ErrApplyRange, ErrApplyRange} {
		if bads[i] != want {
			return errOr("不变量4: 错误不可判定", nil)
		}
	}
	if g.Head() != h || g.Applied() != a {
		return errOr("不变量4: 被拒操作改变了状态", nil)
	}
	return g.Commit(1) // 被拒后仍可正常使用
}

func errOr(msg string, err error) error {
	if err != nil {
		return err
	}
	return &checkError{msg}
}

type checkError struct{ msg string }

func (e *checkError) Error() string { return "api: 自检失败 " + e.msg }
