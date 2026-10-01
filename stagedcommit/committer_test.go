package stagedcommit

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// logState 在测试日志中打印当前状态快照，作为判定依据。
func logState(t *testing.T, c *Committer, note string) {
	t.Helper()
	snap := c.Snapshot()
	t.Logf("状态[%s]: viewCommit=%d view=%v staging=%v log=%v nextCommit=%d",
		note, snap.Commit, snap.Data, c.StagingSnapshot(), c.Log(), c.NextCommitNumber())
}

func mustPut(t *testing.T, c *Committer, key, value string) {
	t.Helper()
	if err := c.Put(key, value); err != nil {
		t.Fatalf("Put(%q, %q) 失败: %v", key, value, err)
	}
}

func mustCommit(t *testing.T, c *Committer, want uint64) {
	t.Helper()
	n, err := c.Commit()
	if err != nil {
		t.Fatalf("Commit 失败: %v", err)
	}
	if n != want {
		t.Fatalf("Commit 返回提交号=%d, 期望 %d", n, want)
	}
	t.Logf("结果: Commit 成功, 提交号=%d", n)
}

// TestTwoPhaseCommit 覆盖两步提交：暂存写入/覆盖/删除，经准备与定稿后可见。
func TestTwoPhaseCommit(t *testing.T) {
	c := New()

	t.Log("输入: Put(a,1), Put(b,1), Put(b,2) (同键覆盖)")
	mustPut(t, c, "a", "1")
	mustPut(t, c, "b", "1")
	mustPut(t, c, "b", "2")
	logState(t, c, "暂存后")

	if snap := c.Snapshot(); len(snap.Data) != 0 {
		t.Fatalf("判定依据: 未提交应对读不可见, 视图应为空, 实际 %v", snap.Data)
	}

	t.Log("输入: Commit (第一阶段写日志记已准备, 第二阶段应用视图并定稿)")
	mustCommit(t, c, 1)
	logState(t, c, "第一次提交后")

	snap := c.Snapshot()
	if want := map[string]string{"a": "1", "b": "2"}; !reflect.DeepEqual(snap.Data, want) {
		t.Fatalf("判定依据: 视图应为 %v, 实际 %v", want, snap.Data)
	}
	if snap.Commit != 1 {
		t.Fatalf("判定依据: 视图提交号应为 1, 实际 %d", snap.Commit)
	}
	if s := c.StagingSnapshot(); len(s) != 0 {
		t.Fatalf("判定依据: 提交后暂存区应清空, 实际 %v", s)
	}
	entry := c.Log()[0]
	if entry.Number != 1 || entry.State != StateFinalized {
		t.Fatalf("判定依据: 日志条目应为 {1 finalized}, 实际 %+v", entry)
	}
	wantChanges := []Change{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}
	if !reflect.DeepEqual(entry.Changes, wantChanges) {
		t.Fatalf("判定依据: 日志变更应为 %+v, 实际 %+v", wantChanges, entry.Changes)
	}

	t.Log("输入: Put(c,3), Commit -> 提交号递增为 2")
	mustPut(t, c, "c", "3")
	mustCommit(t, c, 2)

	t.Log("输入: Delete(a) (a 在可见视图且未被暂存改动, 允许), Commit -> 提交号 3")
	if err := c.Delete("a"); err != nil {
		t.Fatalf("Delete(a) 失败: %v", err)
	}
	mustCommit(t, c, 3)
	logState(t, c, "删除提交后")

	if want := map[string]string{"b": "2", "c": "3"}; !reflect.DeepEqual(c.Snapshot().Data, want) {
		t.Fatalf("判定依据: 删除后视图应为 %v, 实际 %v", want, c.Snapshot().Data)
	}
	if got := c.NextCommitNumber(); got != 4 {
		t.Fatalf("判定依据: 提交号从 1 递增, 下一提交号应为 4, 实际 %d", got)
	}
	for i, e := range c.Log() {
		if e.State != StateFinalized || e.Number != uint64(i+1) {
			t.Fatalf("判定依据: 日志条目 %d 应为已定稿且提交号连续, 实际 %+v", i, e)
		}
	}
}

// TestUncommittedInvisibleAndRollback 覆盖未提交不可见与回滚无痕迹。
func TestUncommittedInvisibleAndRollback(t *testing.T) {
	c := New()

	t.Log("输入: Put(x,9) 后未提交")
	mustPut(t, c, "x", "9")
	logState(t, c, "暂存未提交")

	if snap := c.Snapshot(); len(snap.Data) != 0 {
		t.Fatalf("判定依据: 未提交变更对读不可见, 视图应为空, 实际 %v", snap.Data)
	}
	if staged := c.StagingSnapshot(); len(staged) != 1 || staged[0].Key != "x" {
		t.Fatalf("判定依据: 暂存区应包含 x, 实际 %v", staged)
	}

	t.Log("输入: Rollback")
	if err := c.Rollback(); err != nil {
		t.Fatalf("判定依据: Rollback 应永远成功, 实际 %v", err)
	}
	logState(t, c, "回滚后")

	if s := c.StagingSnapshot(); len(s) != 0 {
		t.Fatalf("判定依据: 回滚后暂存区应清空, 实际 %v", s)
	}
	if l := c.Log(); len(l) != 0 {
		t.Fatalf("判定依据: 回滚应无痕迹, 日志应为空, 实际 %v", l)
	}
	if got := c.NextCommitNumber(); got != 1 {
		t.Fatalf("判定依据: 回滚不消耗提交号, 下一提交号应为 1, 实际 %d", got)
	}
}

// TestCrashBetweenPhases 覆盖崩溃在两步之间：半成品不可见、日志留已准备
// 未定稿条目、暂存区清空、提交号消耗，恢复判定废弃并丢弃提交号。
func TestCrashBetweenPhases(t *testing.T) {
	c := New()
	mustPut(t, c, "k", "v1")
	mustCommit(t, c, 1)

	t.Log("输入: Put(k,v2), Put(y,new), ArmCrash, Commit (崩溃在两步之间)")
	mustPut(t, c, "k", "v2")
	mustPut(t, c, "y", "new")
	c.ArmCrash()
	n, err := c.Commit()
	t.Logf("结果: Commit 返回 (%d, %v)", n, err)
	if !errors.Is(err, ErrSimulatedCrash) {
		t.Fatalf("判定依据: 应返回 ErrSimulatedCrash, 实际 %v", err)
	}
	if n != 2 {
		t.Fatalf("判定依据: 崩溃提交照常消耗提交号 2, 实际 %d", n)
	}
	logState(t, c, "崩溃后")

	if want := map[string]string{"k": "v1"}; !reflect.DeepEqual(c.Snapshot().Data, want) {
		t.Fatalf("判定依据: 半成品提交零效果, 视图应为 %v, 实际 %v", want, c.Snapshot().Data)
	}
	if s := c.StagingSnapshot(); len(s) != 0 {
		t.Fatalf("判定依据: 崩溃后暂存区照常清空, 实际 %v", s)
	}
	if got := c.NextCommitNumber(); got != 3 {
		t.Fatalf("判定依据: 提交号照常消耗, 下一提交号应为 3, 实际 %d", got)
	}
	if log := c.Log(); len(log) != 2 || log[1].Number != 2 || log[1].State != StatePrepared {
		t.Fatalf("判定依据: 日志应留下 {2 prepared} 未定稿条目, 实际 %v", log)
	}

	t.Log("输入: Recover (判定最新已准备未定稿条目为已废弃)")
	if got := c.Recover(); got != 2 {
		t.Fatalf("判定依据: Recover 应返回被废弃提交号 2, 实际 %d", got)
	}
	logState(t, c, "恢复后")
	if log := c.Log(); log[1].State != StateAborted {
		t.Fatalf("判定依据: 条目 2 应被标记为已废弃, 实际 %v", log[1].State)
	}
	if want := map[string]string{"k": "v1"}; !reflect.DeepEqual(c.Snapshot().Data, want) {
		t.Fatalf("判定依据: 恢复后视图仍不变, 应为 %v, 实际 %v", want, c.Snapshot().Data)
	}

	t.Log("输入: Recover (连续第二次)")
	if got := c.Recover(); got != 0 {
		t.Fatalf("判定依据: 连续第二次 Recover 必返回 0, 实际 %d", got)
	}

	t.Log("输入: 恢复后继续正常使用 Put(k,v3), Commit")
	mustPut(t, c, "k", "v3")
	mustCommit(t, c, 3)
	if want := map[string]string{"k": "v3"}; !reflect.DeepEqual(c.Snapshot().Data, want) {
		t.Fatalf("判定依据: 恢复后可正常提交, 视图应为 %v, 实际 %v", want, c.Snapshot().Data)
	}
	logState(t, c, "恢复后再提交")
}

// TestRecoverWithoutPendingReturnsZero 覆盖无待判定条目时 Recover 返回 0。
func TestRecoverWithoutPendingReturnsZero(t *testing.T) {
	c := New()
	if got := c.Recover(); got != 0 {
		t.Fatalf("判定依据: 空日志 Recover 应返回 0, 实际 %d", got)
	}
	mustPut(t, c, "a", "1")
	mustCommit(t, c, 1)
	if got := c.Recover(); got != 0 {
		t.Fatalf("判定依据: 已定稿提交不需恢复, Recover 应返回 0, 实际 %d", got)
	}
	t.Log("结果: 无待判定条目时 Recover 均返回 0")
}

// TestRejectionsKeepState 覆盖三类非法输入被拒后状态不变且互不相同的错误。
func TestRejectionsKeepState(t *testing.T) {
	c := New()
	mustPut(t, c, "k", "v")
	mustCommit(t, c, 1)
	logState(t, c, "初始提交后")

	snapshotOf := func() (View, []Change, []LogEntry, uint64) {
		return c.Snapshot(), c.StagingSnapshot(), c.Log(), c.NextCommitNumber()
	}
	beforeView, beforeStaging, beforeLog, beforeNext := snapshotOf()

	t.Log("输入: Commit (暂存区为空)")
	if _, err := c.Commit(); !errors.Is(err, ErrEmptyCommit) {
		t.Fatalf("判定依据: 空提交应返回 ErrEmptyCommit, 实际 %v", err)
	}
	t.Log("结果: 返回 ErrEmptyCommit")

	t.Log("输入: Put(\"\", x)")
	if err := c.Put("", "x"); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("判定依据: 空键写入应返回 ErrEmptyKey, 实际 %v", err)
	}
	t.Log("结果: 返回 ErrEmptyKey")

	t.Log("输入: Delete(\"\")")
	if err := c.Delete(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("判定依据: 空键删除应返回 ErrEmptyKey, 实际 %v", err)
	}
	t.Log("结果: 返回 ErrEmptyKey")

	t.Log("输入: Delete(missing) (键不在可见视图)")
	if err := c.Delete("missing"); !errors.Is(err, ErrDeleteNotAllowed) {
		t.Fatalf("判定依据: 删除不可见键应返回 ErrDeleteNotAllowed, 实际 %v", err)
	}
	t.Log("结果: 返回 ErrDeleteNotAllowed")

	if errors.Is(ErrEmptyKey, ErrEmptyCommit) ||
		errors.Is(ErrEmptyCommit, ErrDeleteNotAllowed) ||
		errors.Is(ErrEmptyKey, ErrDeleteNotAllowed) {
		t.Fatal("判定依据: 三类错误必须互不相同")
	}

	t.Log("输入: Put(k,v2) 成功, Delete(k) (键已在暂存区被改动)")
	mustPut(t, c, "k", "v2")
	if err := c.Delete("k"); !errors.Is(err, ErrDeleteNotAllowed) {
		t.Fatalf("判定依据: 删除已被暂存改动的键应返回 ErrDeleteNotAllowed, 实际 %v", err)
	}
	if s := c.StagingSnapshot(); !reflect.DeepEqual(s, []Change{{Key: "k", Value: "v2"}}) {
		t.Fatalf("判定依据: 被拒后暂存区应保持 {k=v2}, 实际 %v", s)
	}
	if err := c.Rollback(); err != nil {
		t.Fatalf("Rollback 应永远成功, 实际 %v", err)
	}
	logState(t, c, "全部拒绝后")

	afterView, afterStaging, afterLog, afterNext := snapshotOf()
	if !reflect.DeepEqual(afterView, beforeView) {
		t.Fatalf("判定依据: 被拒后视图不变, 前 %v 后 %v", beforeView, afterView)
	}
	if !reflect.DeepEqual(afterStaging, beforeStaging) {
		t.Fatalf("判定依据: 被拒后暂存区不变, 前 %v 后 %v", beforeStaging, afterStaging)
	}
	if !reflect.DeepEqual(afterLog, beforeLog) {
		t.Fatalf("判定依据: 被拒后日志不变, 前 %v 后 %v", beforeLog, afterLog)
	}
	if afterNext != beforeNext {
		t.Fatalf("判定依据: 被拒后提交号计数不变, 前 %d 后 %d", beforeNext, afterNext)
	}

	t.Log("输入: 被拒后继续正常使用 Put(k,v9), Commit")
	mustPut(t, c, "k", "v9")
	mustCommit(t, c, 2)
	if want := map[string]string{"k": "v9"}; !reflect.DeepEqual(c.Snapshot().Data, want) {
		t.Fatalf("判定依据: 被拒后仍可正常提交, 视图应为 %v, 实际 %v", want, c.Snapshot().Data)
	}
	logState(t, c, "拒绝后再提交")
}

// TestConcurrentReadersSameView 覆盖并发只读同一实例得到逐字段相同的视图。
func TestConcurrentReadersSameView(t *testing.T) {
	c := New()
	mustPut(t, c, "a", "1")
	mustPut(t, c, "b", "2")
	mustCommit(t, c, 1)

	const readers = 16
	views := make([]View, readers)
	var wg sync.WaitGroup
	wg.Add(readers)
	for i := 0; i < readers; i++ {
		go func(idx int) {
			defer wg.Done()
			views[idx] = c.Snapshot()
		}(i)
	}
	wg.Wait()

	for i := 1; i < readers; i++ {
		if !reflect.DeepEqual(views[i], views[0]) {
			t.Fatalf("判定依据: 并发只读视图必须逐字段相同, 读者 0=%v 读者 %d=%v",
				views[0], i, views[i])
		}
	}
	t.Logf("结果: %d 个并发读者读到逐字段相同的视图 %v (提交边界 %d)",
		readers, views[0].Data, views[0].Commit)
}

// TestConcurrentReadConsistency 覆盖读写并发时任一读到的视图都对应某个
// 完整提交边界，且同一边界的视图逐字段相同。
func TestConcurrentReadConsistency(t *testing.T) {
	c := New()
	mustPut(t, c, "base", "0")
	mustCommit(t, c, 1)

	const totalCommits = 50
	const readers = 8

	// expected 构造提交号 n 对应的完整视图：提交 i 恰好写入 key-i。
	expected := func(n uint64) map[string]string {
		m := map[string]string{"base": "0"}
		for i := uint64(2); i <= n; i++ {
			m[fmt.Sprintf("key-%d", i)] = fmt.Sprintf("v%d", i)
		}
		return m
	}

	errCh := make(chan string, readers+1)
	stop := make(chan struct{})
	writerDone := make(chan struct{})

	go func() {
		defer close(writerDone)
		for i := 2; i <= totalCommits; i++ {
			if err := c.Put(fmt.Sprintf("key-%d", i), fmt.Sprintf("v%d", i)); err != nil {
				errCh <- fmt.Sprintf("写者 Put 失败: %v", err)
				return
			}
			if _, err := c.Commit(); err != nil {
				errCh <- fmt.Sprintf("写者 Commit 失败: %v", err)
				return
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(readers)
	for r := 0; r < readers; r++ {
		go func(id int) {
			defer wg.Done()
			seen := map[uint64]View{}
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := c.Snapshot()
				_ = c.StagingSnapshot()
				_ = c.Log()
				_ = c.NextCommitNumber()
				if want := expected(snap.Commit); !reflect.DeepEqual(snap.Data, want) {
					errCh <- fmt.Sprintf("读者 %d: 视图不对应任何完整提交边界: commit=%d data=%v want=%v",
						id, snap.Commit, snap.Data, want)
					return
				}
				if prev, ok := seen[snap.Commit]; ok && !reflect.DeepEqual(prev.Data, snap.Data) {
					errCh <- fmt.Sprintf("读者 %d: 同一提交边界 %d 读到不同视图", id, snap.Commit)
					return
				}
				seen[snap.Commit] = snap
			}
		}(r)
	}

	<-writerDone
	close(stop)
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}

	final := c.Snapshot()
	if final.Commit != totalCommits {
		t.Fatalf("判定依据: 最终视图提交号应为 %d, 实际 %d", totalCommits, final.Commit)
	}
	if want := expected(totalCommits); !reflect.DeepEqual(final.Data, want) {
		t.Fatalf("判定依据: 最终视图应为 %v, 实际 %v", want, final.Data)
	}
	t.Logf("结果: %d 个读者与写者并发期间, 全部读到的视图均落在完整提交边界上; 最终视图提交号=%d",
		readers, final.Commit)
}
