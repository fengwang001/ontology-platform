package repl

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/sm"
)

// triple 是某一时刻的 (committed, lastApplied, state)。
type triple struct{ c, l, s int }

// recompute 是朴素批量重算：从 snapState 起把 cmds[0..committed-1] 升序 apply。
func recompute(cmds []sm.Command, committed int, snapState int) int {
	s := snapState
	for i := 0; i < committed; i++ {
		s = sm.Apply(cmds[i], s)
	}
	return s
}

// SelfCheck 对内置操作序列核验四条不变量（含 O(1) 续读），任一不成立即返回错误。
// 只返回通过/不通过，不暴露 readCount 的数值。
func (r *Replica) SelfCheck() error {
	if err := fiveSteps(); err != nil {
		return err
	}
	if err := rejectionNoTrace(); err != nil {
		return err
	}
	if err := batchingConverges(); err != nil {
		return err
	}
	return incrementalRead()
}

// fiveSteps 核验第三节五步表 S1..S5。
func fiveSteps() error {
	q := New()
	for _, c := range []sm.Command{
		{Op: sm.Add, K: 2}, {Op: sm.Mul, K: 3}, {Op: sm.Add, K: 1}, {Op: sm.Add, K: 5},
	} {
		if err := q.Append(c); err != nil {
			return err
		}
	}
	want := []triple{{0, 0, 0}, {3, 0, 0}, {3, 3, 7}, {4, 2, 6}, {4, 4, 12}}
	if err := checkStep(q, want[0]); err != nil {
		return err
	}
	if err := q.Commit(3); err != nil {
		return err
	}
	if err := checkStep(q, want[1]); err != nil {
		return err
	}
	q.Apply()
	if err := checkStep(q, want[2]); err != nil {
		return err
	}
	if err := q.Restart(2, 6); err != nil {
		return err
	}
	if err := q.Commit(4); err != nil {
		return err
	}
	if err := checkStep(q, want[3]); err != nil {
		return err
	}
	q.Apply()
	return checkStep(q, want[4])
}

// rejectionNoTrace 核验三类错误互不相同、且被拒后状态不变。
func rejectionNoTrace() error {
	bad := New()
	before := triple{bad.committed, bad.lastApplied, bad.state}
	ec := bad.Commit(1)
	ea := bad.Append(sm.Command{})
	es := bad.Restart(1, 9)
	if !errors.Is(ec, ErrCommitOutOfRange) || !errors.Is(ea, ErrEmptyCommand) ||
		!errors.Is(es, ErrSnapOutOfRange) || ec == ea || ea == es {
		return fmt.Errorf("selfcheck: 哨兵错误不可判定或不互异")
	}
	after := triple{bad.committed, bad.lastApplied, bad.state}
	if before != after || len(bad.log) != 0 {
		return fmt.Errorf("selfcheck: 被拒操作留下痕迹")
	}
	return nil
}

// batchingConverges 核验分批 Apply 与一次性/朴素重算结果一致。
func batchingConverges() error {
	rng := rand.New(rand.NewSource(731))
	cmds := make([]sm.Command, 200)
	for i := range cmds {
		if rng.Intn(2) == 0 {
			cmds[i] = sm.Command{Op: sm.Add, K: rng.Intn(7) - 3}
		} else {
			cmds[i] = sm.Command{Op: sm.Mul, K: rng.Intn(5) + 1}
		}
	}
	a, b := New(), New()
	for _, c := range cmds {
		_ = a.Append(c)
		_ = b.Append(c)
	}
	for c := 0; c < len(cmds); {
		c += 1 + rng.Intn(7)
		if c > len(cmds) {
			c = len(cmds)
		}
		_ = a.Commit(c)
		a.Apply()
	}
	_ = b.Commit(len(cmds))
	b.Apply()
	if a.State() != b.State() || a.State() != recompute(cmds, len(cmds), 0) {
		return fmt.Errorf("selfcheck: 分批与重算不一致")
	}
	return nil
}

// incrementalRead 核验 m-1 条已应用后再 Apply 只读取 1 条（不随 m 线性增长）。
func incrementalRead() error {
	for _, m := range []int{100, 1000, 10000} {
		z := New()
		for i := 0; i < m; i++ {
			_ = z.Append(sm.Command{Op: sm.Add, K: 1})
		}
		_ = z.Commit(m - 1)
		z.Apply()
		_ = z.Commit(m)
		z.Apply()
		if z.readCount != 1 {
			return fmt.Errorf("selfcheck: m=%d 时读取条目数=%d，应为 1", m, z.readCount)
		}
	}
	return nil
}

func checkStep(q *Replica, w triple) error {
	if got := (triple{q.Committed(), q.LastApplied(), q.State()}); got != w {
		return fmt.Errorf("selfcheck: 得到 %v，期望 %v", got, w)
	}
	return nil
}
