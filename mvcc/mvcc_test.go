package mvcc

import (
	"errors"
	"sync"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功, 得到错误: %v", err)
	}
}

func mustErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("期望错误 %v, 得到 %v", want, err)
	}
}

func mustVisible(t *testing.T, e *Engine, tuple string, x, c, s int, want bool) {
	t.Helper()
	got, err := e.Visible(tuple, x, c, s)
	mustOK(t, err)
	if got != want {
		t.Fatalf("Visible(%s,%d,%d,%d) = %v, 期望 %v", tuple, x, c, s, got, want)
	}
}

// cmin 恰等于当前命令号时不可见，下一命令可见。
func TestCminEqualCurrentCommand(t *testing.T) {
	e := New()
	mustOK(t, e.Begin(1))
	s := e.Snapshot()
	mustOK(t, e.Insert("t", 1, 0))
	mustVisible(t, e, "t", 1, 0, s, false)
	mustVisible(t, e, "t", 1, 1, s, true)
}

// 自己删除的元组在同一命令内仍可见，下一命令不可见。
func TestOwnDeleteSameCommand(t *testing.T) {
	e := New()
	mustOK(t, e.Begin(1))
	s := e.Snapshot()
	mustOK(t, e.Insert("t", 1, 0))
	mustOK(t, e.Delete("t", 1, 1))
	mustVisible(t, e, "t", 1, 1, s, true)
	mustVisible(t, e, "t", 1, 2, s, false)
}

// 子事务中止后其插入不可见，且其删除标记可被覆盖。
func TestAbortedSubEffects(t *testing.T) {
	e := New()
	mustOK(t, e.Begin(1))
	mustOK(t, e.BeginSub(2, 1))
	mustOK(t, e.Insert("t", 2, 0))
	mustOK(t, e.Abort(2))
	s := e.Snapshot()
	mustVisible(t, e, "t", 1, 1, s, false)

	// 另一棵树准备已提交的元组 u。
	mustOK(t, e.Begin(10))
	mustOK(t, e.Insert("u", 10, 0))
	mustOK(t, e.Commit(10))

	mustOK(t, e.BeginSub(3, 1))
	mustOK(t, e.Delete("u", 3, 1))
	// 标记者仍活动，覆盖被拒绝。
	mustErr(t, e.Delete("u", 1, 2), ErrTupleAlreadyDeleted)
	mustOK(t, e.Abort(3))
	// 标记者已中止，可覆盖。
	mustOK(t, e.Delete("u", 1, 2))
	mustVisible(t, e, "u", 1, 3, s, false)
}

// 父事务中止使已子提交的子事务效果一并中止。
func TestParentAbortCascadesSubCommitted(t *testing.T) {
	e := New()
	mustOK(t, e.Begin(1))
	mustOK(t, e.BeginSub(2, 1))
	mustOK(t, e.Insert("t", 2, 0))
	mustOK(t, e.CommitSub(2))
	mustOK(t, e.Abort(1))

	mustOK(t, e.Begin(9))
	s := e.Snapshot()
	mustVisible(t, e, "t", 9, 0, s, false)
}

// 其他树的事务在快照之后提交则不可见，之前提交则可见。
func TestSnapshotBoundary(t *testing.T) {
	e := New()
	mustOK(t, e.Begin(1))
	mustOK(t, e.Insert("before", 1, 0))
	mustOK(t, e.Commit(1))
	s := e.Snapshot()
	mustOK(t, e.Begin(2))
	mustOK(t, e.Insert("after", 2, 0))
	mustOK(t, e.Commit(2))

	mustOK(t, e.Begin(3))
	mustVisible(t, e, "before", 3, 0, s, true)
	mustVisible(t, e, "after", 3, 0, s, false)
}

// 已子提交的子事务在根提交前对其他树不可见，根提交后可见。
func TestSubCommitInvisibleBeforeRootCommit(t *testing.T) {
	e := New()
	mustOK(t, e.Begin(1))
	mustOK(t, e.BeginSub(2, 1))
	mustOK(t, e.Insert("t", 2, 0))
	mustOK(t, e.CommitSub(2))
	s1 := e.Snapshot()
	mustOK(t, e.Begin(9))
	mustVisible(t, e, "t", 9, 0, s1, false)

	mustOK(t, e.Commit(1))
	s2 := e.Snapshot()
	mustVisible(t, e, "t", 9, 0, s2, true)
}

// 各操作的拒绝原因与检查顺序。
func TestErrorPriority(t *testing.T) {
	e := New()

	// Begin: x 不为正优先于 x 已存在。
	mustErr(t, e.Begin(0), ErrTxNotPositive)
	mustOK(t, e.Begin(1))
	mustErr(t, e.Begin(1), ErrTxExists)

	// BeginSub: x 不为正 > x 已存在 > p 不存在 > p 非运行中。
	mustErr(t, e.BeginSub(-1, 1), ErrTxNotPositive)
	mustErr(t, e.BeginSub(1, 1), ErrTxExists)
	mustErr(t, e.BeginSub(2, 99), ErrTxNotFound)
	mustOK(t, e.Begin(3))
	mustOK(t, e.Commit(3))
	mustErr(t, e.BeginSub(2, 3), ErrTxNotRunning)

	// CommitSub/Commit/Abort: 不存在 > 非运行中 > 类型不符 > 运行中后代。
	mustErr(t, e.CommitSub(99), ErrTxNotFound)
	mustErr(t, e.Commit(99), ErrTxNotFound)
	mustErr(t, e.Abort(99), ErrTxNotFound)
	mustErr(t, e.CommitSub(3), ErrTxNotRunning)
	mustErr(t, e.Commit(3), ErrTxNotRunning)
	mustErr(t, e.Abort(3), ErrTxNotRunning)
	mustErr(t, e.CommitSub(1), ErrTxTypeMismatch) // 1 是顶层
	mustOK(t, e.BeginSub(4, 1))
	mustErr(t, e.Commit(4), ErrTxTypeMismatch) // 4 是子事务
	mustErr(t, e.CommitSub(1), ErrTxTypeMismatch)
	mustErr(t, e.Commit(1), ErrTxRunningDescendants) // 4 仍运行中
	mustOK(t, e.Abort(4))
	mustOK(t, e.Commit(1))

	// Insert/Delete: 不存在 > 非运行中 > 命令号为负 > 命令号回退 > 元组检查。
	mustErr(t, e.Insert("a", 99, 0), ErrTxNotFound)
	mustErr(t, e.Insert("a", 1, 0), ErrTxNotRunning)
	mustOK(t, e.Begin(5))
	mustErr(t, e.Insert("a", 5, -1), ErrNegativeCommand)
	mustOK(t, e.Insert("a", 5, 3))
	mustErr(t, e.Insert("b", 5, 2), ErrCommandOrder)
	mustErr(t, e.Insert("a", 5, 3), ErrTupleExists)
	mustErr(t, e.Delete("zz", 5, 3), ErrTupleNotFound)
	mustErr(t, e.Delete("zz", 5, -1), ErrNegativeCommand)

	// 被拒绝的操作不改变已用最大命令号：拒绝后 c=3 仍被接受。
	mustOK(t, e.Insert("b", 5, 3))

	// Visible: 不存在 > 非运行中 > 命令号为负 > 快照未知 > 元组不存在。
	mustErrVisible(t, e, "a", 99, 0, 1, ErrTxNotFound)
	mustErrVisible(t, e, "a", 1, 0, 1, ErrTxNotRunning)
	mustErrVisible(t, e, "a", 5, -1, 1, ErrNegativeCommand)
	mustErrVisible(t, e, "a", 5, 0, 999, ErrSnapshotUnknown)
	s := e.Snapshot()
	mustErrVisible(t, e, "zz", 5, 0, s, ErrTupleNotFound)
}

func mustErrVisible(t *testing.T, e *Engine, tuple string, x, c, s int, want error) {
	t.Helper()
	_, err := e.Visible(tuple, x, c, s)
	mustErr(t, err, want)
}

// 并发：同一元组的并发 Delete 恰有一个成功；快照编号连续无空洞。
func TestConcurrency(t *testing.T) {
	e := New()
	mustOK(t, e.Begin(1))
	mustOK(t, e.Insert("t", 1, 0))
	mustOK(t, e.Commit(1))

	const n = 32
	mustOK(t, e.Begin(2))
	snapIDs := make([]int, n)
	delErrs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			snapIDs[i] = e.Snapshot()
		}(i)
		go func(i int) {
			defer wg.Done()
			delErrs[i] = e.Delete("t", 2, 1)
		}(i)
	}
	wg.Wait()

	seen := make(map[int]bool)
	for _, id := range snapIDs {
		if id < 1 || id > n || seen[id] {
			t.Fatalf("快照编号不连续或有空洞: %v", snapIDs)
		}
		seen[id] = true
	}

	successes := 0
	for _, err := range delErrs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrTupleAlreadyDeleted) {
			t.Fatalf("并发 Delete 出现意外错误: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("并发 Delete 成功数为 %d, 期望恰为 1", successes)
	}
}

// 相同操作序列重放得到完全相同的结果与错误。
func TestReplayDeterminism(t *testing.T) {
	run := func() []error {
		e := New()
		var errs []error
		errs = append(errs, e.Begin(1))
		errs = append(errs, e.BeginSub(2, 1))
		errs = append(errs, e.Insert("t", 2, 0))
		errs = append(errs, e.CommitSub(2))
		errs = append(errs, e.Commit(1))
		s := e.Snapshot()
		errs = append(errs, e.Begin(3))
		_, err := e.Visible("t", 3, 0, s)
		errs = append(errs, err)
		return errs
	}
	first := run()
	for i := 0; i < 10; i++ {
		again := run()
		for j := range first {
			if !errors.Is(again[j], first[j]) && (again[j] == nil) != (first[j] == nil) {
				t.Fatalf("第 %d 次重放第 %d 步结果不一致: %v vs %v", i, j, first[j], again[j])
			}
		}
	}
}
