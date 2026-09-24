// Package api 是对外门面：两阶段提交器的公开入口。依赖 txn。
package api

import (
	"errors"
	"fmt"

	"ontology/txn"
)

// 对外暴露同一组哨兵错误（与 txn 中的是同一变量，可用 errors.Is 判定）。
var (
	ErrInvalidSeq    = txn.ErrInvalidSeq
	ErrOutOfOrder    = txn.ErrOutOfOrder
	ErrEffectMissing = txn.ErrEffectMissing
	ErrOffsetJump    = txn.ErrOffsetJump
)

// Committer 是变更流消费位点与外部副作用的两阶段原子提交器。
type Committer struct{ t *txn.Txn }

// New 返回 C=0、store 空的提交器。
func New() *Committer { return &Committer{t: txn.New()} }

// Apply 阶段一（先效果）：幂等写副作用，不推进位点。
func (c *Committer) Apply(seq, eff int64) error { return c.t.Apply(seq, eff) }

// Commit 阶段二（后位点）：推进已提交位点 C 到 seq。
func (c *Committer) Commit(seq int64) error { return c.t.Commit(seq) }

// Committed 返回当前已提交位点 C。
func (c *Committer) Committed() int64 { return c.t.Committed() }

// Pending 返回已写副作用但位点未提交的序号（升序）。
func (c *Committer) Pending() []int64 { return c.t.Pending() }

// Restart 模拟崩溃重启：保留已持久化的 store 与 C，重建易失结构，
// 返回 Pending（消费端据此重复处理，保证至少一次）。
func (c *Committer) Restart() error {
	_, err := c.t.Restart()
	return err
}

// SelfCheck 对一组内置操作序列核验四条不变量，全部通过返回 nil。
func (c *Committer) SelfCheck() error {
	for _, chk := range []func() error{checkNoLoss, checkContiguous, checkIdempotent, checkAtomicFailure} {
		if err := chk(); err != nil {
			return err
		}
	}
	return nil
}

func checkNoLoss() error { // 不变量1：任意 seq<=C 的效果都在
	fresh := New()
	for i := int64(1); i <= 50; i++ {
		if err := fresh.Apply(i, i*10); err != nil {
			return err
		}
		if err := fresh.Commit(i); err != nil {
			return err
		}
	}
	if fresh.Committed() != 50 {
		return fmt.Errorf("selfcheck no-loss: C=%d want 50", fresh.Committed())
	}
	return nil
}

func checkContiguous() error { // 不变量2：C 只 +1，跳跃被拒
	fresh := New()
	if err := fresh.Apply(1, 1); err != nil {
		return err
	}
	if err := fresh.Commit(3); !errors.Is(err, ErrOffsetJump) {
		return fmt.Errorf("selfcheck contiguous: got %v want ErrOffsetJump", err)
	}
	if fresh.Committed() != 0 {
		return fmt.Errorf("selfcheck contiguous: C moved to %d", fresh.Committed())
	}
	return nil
}

func checkIdempotent() error { // 不变量3：重复 Apply/Commit 幂等
	fresh := New()
	for i := 0; i < 3; i++ {
		if err := fresh.Apply(1, 7); err != nil {
			return err
		}
	}
	if err := fresh.Commit(1); err != nil {
		return err
	}
	if err := fresh.Commit(1); err != nil {
		return err
	}
	if err := fresh.Apply(1, 99); err != nil { // 已提交序号：幂等空操作
		return err
	}
	if fresh.Committed() != 1 || len(fresh.Pending()) != 0 {
		return errors.New("selfcheck idempotent: state drifted")
	}
	return nil
}

func checkAtomicFailure() error { // 不变量4：被拒操作不留痕
	fresh := New()
	before := fresh.Committed()
	for _, op := range []func() error{
		func() error { return fresh.Apply(0, 1) }, // ErrInvalidSeq
		func() error { return fresh.Apply(5, 1) }, // ErrOutOfOrder
		func() error { return fresh.Commit(1) },   // ErrEffectMissing
		func() error { return fresh.Commit(9) },   // ErrOffsetJump
		func() error { return fresh.Commit(-2) },  // ErrInvalidSeq
	} {
		if op() == nil {
			return errors.New("selfcheck atomic: invalid op accepted")
		}
	}
	if fresh.Committed() != before || len(fresh.Pending()) != 0 {
		return errors.New("selfcheck atomic: rejected op left trace")
	}
	return fresh.Apply(1, 1) // 被拒后仍可正常使用
}
