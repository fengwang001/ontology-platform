// Package api 是对外接口层，包装 acc 并提供自检。依赖方向：api -> acc -> ckpt。
package api

import (
	"errors"
	"fmt"

	"ontology/acc"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrEmptyKey       = acc.ErrEmptyKey
	ErrNegativeOffset = acc.ErrNegativeOffset
	ErrTooManyPending = acc.ErrTooManyPending
)

// A 是断点续传累加器的对外句柄。
type A struct {
	core *acc.Acc
}

// New 创建累加器，maxPending 为在途条目上限。
func New(maxPending int) (*A, error) {
	c, err := acc.New(maxPending)
	if err != nil {
		return nil, err
	}
	return &A{core: c}, nil
}

// Apply 投递一条记录；重复幂等跳过，非法输入整体失败且不留痕。
func (a *A) Apply(key string, offset, delta int64) error { return a.core.Apply(key, offset, delta) }

// Commit 折叠连续前缀并推进已提交位点。
func (a *A) Commit() { a.core.Commit() }

// Checkpoint 返回已提交位点 cp。
func (a *A) Checkpoint() int64 { return a.core.Checkpoint() }

// Sum 返回 key 的已提交累加值。
func (a *A) Sum(key string) int64 { return a.core.Sum(key) }

// Restore 模拟崩溃重启：清空易失 pending，sum 与 cp 不变。
func (a *A) Restore() { a.core.Restore() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
// 自检在独立的内部实例上进行，不影响调用方状态。
func (a *A) SelfCheck() error {
	// 不变量 1+2：与朴素参照一致、cp 只走连续前缀。
	x, err := New(64)
	if err != nil {
		return err
	}
	applied := map[int64]int64{} // 朴素参照：已 Apply 的 offset -> delta
	ncp, nsum := int64(-1), int64(0)
	ops := []struct {
		key    string
		offset int64
		delta  int64
		commit bool
	}{
		{"k", 0, 10, false}, {"k", 2, 20, false}, {"k", 3, 30, true},
		{"k", 2, 20, false}, {"k", 1, 40, true}, {"k", 5, 7, true},
		{"k", 4, 1, true}, {"k", 8, 2, false}, {"k", 6, 3, true},
	}
	for _, op := range ops {
		if err := x.Apply(op.key, op.offset, op.delta); err != nil {
			return fmt.Errorf("selfcheck apply: %w", err)
		}
		if _, dup := applied[op.offset]; !dup {
			applied[op.offset] = op.delta
		}
		if op.commit {
			x.Commit()
			ncp, nsum = naive(applied, ncp, nsum) // 朴素参照同样只在提交点推进
		}
		if x.Checkpoint() != ncp || x.Sum("k") != nsum {
			return fmt.Errorf("selfcheck: naive mismatch cp=%d/%d sum=%d/%d",
				x.Checkpoint(), ncp, x.Sum("k"), nsum)
		}
	}
	// 不变量 3：Restore 后 cp/sum 不变，仅 pending 清空。
	cpBefore, sumBefore := x.Checkpoint(), x.Sum("k")
	x.Restore()
	if x.Checkpoint() != cpBefore || x.Sum("k") != sumBefore {
		return errors.New("selfcheck: restore changed persisted state")
	}
	// 不变量 4：三类失败互不相同且不留痕。
	for i, err := range []error{
		x.Apply("", 100, 1), x.Apply("k", -1, 1), fillToLimit(x),
	} {
		want := []error{ErrEmptyKey, ErrNegativeOffset, ErrTooManyPending}[i]
		if !errors.Is(err, want) {
			return fmt.Errorf("selfcheck: fault %d not distinguishable: %v", i, err)
		}
	}
	if x.Checkpoint() != cpBefore || x.Sum("k") != sumBefore {
		return errors.New("selfcheck: rejected apply left trace")
	}
	return nil
}

// naive 朴素参照：从 ncp+1 起逐个检查已 Apply 的 offset 集合，遇第一个缺口停。
func naive(applied map[int64]int64, ncp, nsum int64) (int64, int64) {
	for d, ok := applied[ncp+1]; ok; d, ok = applied[ncp+1] {
		nsum += d
		ncp++
	}
	return ncp, nsum
}

func fillToLimit(x *A) error {
	var err error
	for i := int64(1000); err == nil; i++ {
		err = x.Apply("overflow", i, 1)
	}
	return err
}
