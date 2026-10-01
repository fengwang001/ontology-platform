package proctable

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// logStep 打印一次操作的输入、输出与判定依据。
func logStep(t *testing.T, input, output, basis string) {
	t.Helper()
	t.Logf("输入: %s | 输出: %s | 判定依据: %s", input, output, basis)
}

// checkInvariants 校验全局不变量：表项数不超容量；僵尸没有子进程；
// 除始进程外每个表项的父进程存活；父子关系双向一致。
func checkInvariants(t *testing.T, tb *Table, capacity int) {
	t.Helper()
	if got := tb.Size(); got > capacity {
		t.Fatalf("不变量违反: 表项数 %d 超过容量 %d", got, capacity)
	}
	for pid := 1; pid < capacity+64; pid++ {
		e, ok := tb.Lookup(pid)
		if !ok {
			continue
		}
		children := tb.Children(pid)
		if !e.Alive && len(children) != 0 {
			t.Fatalf("不变量违反: 僵尸 %d 仍有子进程 %v", pid, children)
		}
		if pid != InitPID {
			p, ok := tb.Lookup(e.Parent)
			if !ok || !p.Alive {
				t.Fatalf("不变量违反: 进程 %d 的父进程 %d 不存在或已死", pid, e.Parent)
			}
			found := false
			for _, c := range tb.Children(e.Parent) {
				if c == pid {
					found = true
				}
			}
			if !found {
				t.Fatalf("不变量违反: 父进程 %d 的子进程集合缺少 %d", e.Parent, pid)
			}
		}
		for _, c := range children {
			ce, ok := tb.Lookup(c)
			if !ok || ce.Parent != pid {
				t.Fatalf("不变量违反: 子进程 %d 的父指针不等于 %d", c, pid)
			}
		}
	}
}

func mustCreate(t *testing.T, tb *Table, parent int) int {
	t.Helper()
	pid, err := tb.Create(parent)
	if err != nil {
		t.Fatalf("Create(%d) 意外失败: %v", parent, err)
	}
	logStep(t, fmt.Sprintf("Create(%d)", parent), fmt.Sprintf("pid=%d", pid), "父进程存活且表未满")
	return pid
}

func mustExit(t *testing.T, tb *Table, pid, code int) {
	t.Helper()
	if err := tb.Exit(pid, code); err != nil {
		t.Fatalf("Exit(%d,%d) 意外失败: %v", pid, code, err)
	}
	logStep(t, fmt.Sprintf("Exit(%d,%d)", pid, code), "ok", "进程存活且非始进程")
}

func kindOf(err error) Kind {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Kind
	}
	return -1
}

// 场景 1：父进程带着僵尸子进程退出时，僵尸子进程改挂始进程并立即被回收，
// 表项立即释放，随后可再创建。
func TestExitReapsZombieChildrenAndFreesSlots(t *testing.T) {
	const capacity = 4
	tb := New(capacity)
	p2 := mustCreate(t, tb, InitPID)
	p3 := mustCreate(t, tb, p2)
	p4 := mustCreate(t, tb, p2)
	mustExit(t, tb, p3, 30)
	mustExit(t, tb, p4, 40)
	checkInvariants(t, tb, capacity)

	if got := tb.Size(); got != 4 {
		t.Fatalf("退出前表项数应为 4，实际 %d", got)
	}
	mustExit(t, tb, p2, 20)
	logStep(t, "Exit(2,20) 后", fmt.Sprintf("Size=%d Reaped=%v", tb.Size(), tb.Reaped()),
		"僵尸子进程 3、4 改挂始进程即回收；2 的父进程是始进程，也立即回收")
	if got := tb.Size(); got != 1 {
		t.Fatalf("Exit(2) 后应只剩始进程，实际表项数 %d", got)
	}
	wantReaped := []int{3, 4, 2}
	if got := tb.Reaped(); !reflect.DeepEqual(got, wantReaped) {
		t.Fatalf("回收顺序应为 %v，实际 %v", wantReaped, got)
	}
	checkInvariants(t, tb, capacity)

	p5 := mustCreate(t, tb, InitPID)
	if p5 != 5 {
		t.Fatalf("表项释放后可再创建，新编号应严格递增为 5，实际 %d", p5)
	}
	checkInvariants(t, tb, capacity)
}

// 场景 2：父为始进程的进程退出后立即被回收，不留下僵尸。
func TestExitWithInitParentReapedImmediately(t *testing.T) {
	const capacity = 8
	tb := New(capacity)
	p2 := mustCreate(t, tb, InitPID)
	mustExit(t, tb, p2, 7)
	logStep(t, "Exit(2,7) 后", fmt.Sprintf("Size=%d Reaped=%v", tb.Size(), tb.Reaped()),
		"退出者的父进程是始进程，立即回收")
	if got := tb.Size(); got != 1 {
		t.Fatalf("父为始进程的退出应立即回收，实际表项数 %d", got)
	}
	if _, ok := tb.Lookup(p2); ok {
		t.Fatalf("进程 %d 应已被回收", p2)
	}
	checkInvariants(t, tb, capacity)
}

// 场景 3：等待任一子进程时，按成为僵尸的先后回收，而不是按编号。
func TestWaitAnyReapsOldestZombie(t *testing.T) {
	const capacity = 8
	tb := New(capacity)
	p2 := mustCreate(t, tb, InitPID)
	c3 := mustCreate(t, tb, p2)
	c4 := mustCreate(t, tb, p2)
	c5 := mustCreate(t, tb, p2)
	mustExit(t, tb, c4, 44)
	mustExit(t, tb, c3, 33)
	mustExit(t, tb, c5, 55)

	for _, want := range []struct{ pid, code int }{{c4, 44}, {c3, 33}, {c5, 55}} {
		pid, code, err := tb.WaitAny(p2)
		if err != nil {
			t.Fatalf("WaitAny(%d) 意外失败: %v", p2, err)
		}
		logStep(t, fmt.Sprintf("WaitAny(%d)", p2), fmt.Sprintf("pid=%d code=%d", pid, code),
			"回收子进程中最早成为僵尸者")
		if pid != want.pid || code != want.code {
			t.Fatalf("WaitAny 应回收 (%d,%d)，实际 (%d,%d)", want.pid, want.code, pid, code)
		}
	}
	_, _, err := tb.WaitAny(p2)
	logStep(t, fmt.Sprintf("WaitAny(%d)", p2), fmt.Sprintf("err=%v", err), "没有任何子进程")
	if kindOf(err) != ErrNoChildren {
		t.Fatalf("应为 ErrNoChildren，实际 %v", err)
	}
	checkInvariants(t, tb, capacity)
}

// 场景 4：表满后拒绝创建；经回收释放表项后恢复创建。
func TestTableFullThenRecoverAfterReap(t *testing.T) {
	const capacity = 3
	tb := New(capacity)
	p2 := mustCreate(t, tb, InitPID)
	p3 := mustCreate(t, tb, InitPID)

	_, err := tb.Create(InitPID)
	logStep(t, "Create(1)", fmt.Sprintf("err=%v", err), "表已满（3/3）")
	if kindOf(err) != ErrTableFull {
		t.Fatalf("应为 ErrTableFull，实际 %v", err)
	}

	mustExit(t, tb, p2, 2)
	p4 := mustCreate(t, tb, InitPID)
	if p4 != 4 {
		t.Fatalf("编号不复用，应为 4，实际 %d", p4)
	}
	mustExit(t, tb, p3, 3)
	p5 := mustCreate(t, tb, InitPID)
	if p5 != 5 {
		t.Fatalf("编号不复用，应为 5，实际 %d", p5)
	}
	checkInvariants(t, tb, capacity)
}

// 场景 5：多层改挂——进程退出时，其存活的与僵尸的子进程都改挂到始进程；
// 归始进程的僵尸立即回收，存活子进程挂在始进程下继续存活。
func TestMultiLevelReparenting(t *testing.T) {
	const capacity = 8
	tb := New(capacity)
	p2 := mustCreate(t, tb, InitPID)
	p3 := mustCreate(t, tb, p2)
	p4 := mustCreate(t, tb, p3)
	p5 := mustCreate(t, tb, p3)
	mustExit(t, tb, p4, 14)

	mustExit(t, tb, p3, 13)
	logStep(t, "Exit(3,13) 后", fmt.Sprintf("Children(1)=%v", tb.Children(InitPID)),
		"3 的子进程 4（僵尸）、5（存活）改挂始进程；僵尸 4 归始进程即回收")
	if got := tb.Children(InitPID); !reflect.DeepEqual(got, []int{2, 5}) {
		t.Fatalf("始进程的子进程应为 [2 5]，实际 %v", got)
	}
	if _, ok := tb.Lookup(p4); ok {
		t.Fatalf("僵尸 4 改挂始进程后应立即被回收")
	}
	if e, _ := tb.Lookup(p5); e.Parent != InitPID {
		t.Fatalf("存活进程 5 的父进程应为始进程，实际 %d", e.Parent)
	}
	if e, _ := tb.Lookup(p3); e.Parent != p2 || e.Alive {
		t.Fatalf("僵尸 3 应仍挂在存活父进程 2 下，实际 %+v", e)
	}
	checkInvariants(t, tb, capacity)

	mustExit(t, tb, p2, 12)
	logStep(t, "Exit(2,12) 后", fmt.Sprintf("Size=%d Reaped=%v", tb.Size(), tb.Reaped()),
		"2 退出：僵尸 3 改挂始进程即回收；2 的父进程是始进程，也立即回收")
	if got := tb.Children(InitPID); !reflect.DeepEqual(got, []int{5}) {
		t.Fatalf("始进程下应只剩存活进程 5，实际 %v", got)
	}
	if got := tb.Size(); got != 2 {
		t.Fatalf("多层改挂后应只剩始进程与进程 5，实际表项数 %d", got)
	}
	checkInvariants(t, tb, capacity)
}

// 场景 6：错误优先级——多因同时成立时按「不存在、已是僵尸、其余」只报第一个；
// 被拒绝的操作不改变任何状态。
func TestErrorPrecedenceAndAtomicity(t *testing.T) {
	const capacity = 3
	tb := New(capacity)
	p2 := mustCreate(t, tb, InitPID)
	p3 := mustCreate(t, tb, p2)
	mustExit(t, tb, p3, 9)

	snapshot := func() string {
		return fmt.Sprintf("Size=%d Reaped=%v Children(2)=%v", tb.Size(), tb.Reaped(), tb.Children(p2))
	}
	before := snapshot()

	if _, err := tb.Create(99); kindOf(err) != ErrNotFound {
		t.Fatalf("Create(99) 应报 not-found，实际 %v", err)
	}
	logStep(t, "Create(99)", "err=not-found", "父进程不存在优先于表满")

	if _, err := tb.Create(p3); kindOf(err) != ErrZombie {
		t.Fatalf("Create(3) 应报 zombie，实际 %v", err)
	}
	logStep(t, "Create(3)", "err=zombie", "父进程已是僵尸优先于表满")

	if _, err := tb.Wait(99, p3); kindOf(err) != ErrNotFound {
		t.Fatalf("Wait(99,3) 应报 not-found，实际 %v", err)
	}
	logStep(t, "Wait(99,3)", "err=not-found", "父进程不存在优先于目标已是僵尸")

	if err := tb.Exit(p3, 1); kindOf(err) != ErrZombie {
		t.Fatalf("Exit(3) 应报 zombie，实际 %v", err)
	}
	if _, _, err := tb.WaitAny(p3); kindOf(err) != ErrZombie {
		t.Fatalf("WaitAny(3) 应报 zombie，实际 %v", err)
	}
	logStep(t, "Exit(3)/WaitAny(3)", "err=zombie", "对僵尸再做退出或等待")

	if err := tb.Exit(InitPID, 0); kindOf(err) != ErrInitExit {
		t.Fatalf("Exit(1) 应报 init-exit，实际 %v", err)
	}
	logStep(t, "Exit(1)", "err=init-exit", "始进程永不退出")

	if _, err := tb.Wait(p2, InitPID); kindOf(err) != ErrNotChild {
		t.Fatalf("Wait(2,1) 应报 not-child，实际 %v", err)
	}
	logStep(t, "Wait(2,1)", "err=not-child", "目标不是自己的子进程")

	if _, _, err := tb.WaitAny(InitPID); kindOf(err) != ErrNoZombie {
		t.Fatalf("WaitAny(1) 应报 no-zombie，实际 %v", err)
	}
	logStep(t, "WaitAny(1)", "err=no-zombie", "仍有存活子进程（2）但无僵尸")

	if _, _, err := tb.WaitAny(p3); kindOf(err) != ErrZombie {
		t.Fatalf("WaitAny(3) 应报 zombie，实际 %v", err)
	}

	if after := snapshot(); after != before {
		t.Fatalf("被拒绝的操作改变了状态: 前 %s 后 %s", before, after)
	}
	checkInvariants(t, tb, capacity)
}

// 场景 7：指定子进程为僵尸时回收并返回退出码；每个僵尸至多被回收一次。
func TestWaitSpecificChild(t *testing.T) {
	const capacity = 8
	tb := New(capacity)
	p2 := mustCreate(t, tb, InitPID)
	c3 := mustCreate(t, tb, p2)
	c4 := mustCreate(t, tb, p2)

	if _, err := tb.Wait(p2, c3); kindOf(err) != ErrStillAlive {
		t.Fatalf("Wait(2,3) 应报 still-alive，实际 %v", err)
	}
	logStep(t, "Wait(2,3)", "err=still-alive", "指定子进程仍存活")

	mustExit(t, tb, c3, 77)
	code, err := tb.Wait(p2, c3)
	if err != nil || code != 77 {
		t.Fatalf("Wait(2,3) 应返回退出码 77，实际 code=%d err=%v", code, err)
	}
	logStep(t, "Wait(2,3)", "code=77", "子进程为僵尸，回收并返回退出码")

	if _, err := tb.Wait(p2, c3); kindOf(err) != ErrNotFound {
		t.Fatalf("Wait(2,3) 再次应报 not-found，实际 %v", err)
	}
	logStep(t, "Wait(2,3) 再次", "err=not-found", "僵尸至多被回收一次，回收后即不存在")

	mustExit(t, tb, c4, 88)
	if _, err := tb.Wait(InitPID, c4); kindOf(err) != ErrNotChild {
		t.Fatalf("Wait(1,4) 应报 not-child，实际 %v", err)
	}
	logStep(t, "Wait(1,4)", "err=not-child", "4 是 2 的子进程，不是始进程的")
	checkInvariants(t, tb, capacity)
}

// 场景 8：并发调用下不变量始终成立，且每个僵尸至多被回收一次。
func TestConcurrentSafety(t *testing.T) {
	const capacity = 16
	tb := New(capacity)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				pid, err := tb.Create(InitPID)
				if err != nil {
					continue
				}
				_ = tb.Exit(pid, i)
				_, _, _ = tb.WaitAny(InitPID)
				_ = tb.Size()
				_, _ = tb.Lookup(pid)
			}
		}()
	}
	wg.Wait()
	if got := tb.Size(); got > capacity {
		t.Fatalf("并发结束后表项数 %d 超过容量 %d", got, capacity)
	}
	seen := make(map[int]bool)
	for _, pid := range tb.Reaped() {
		if seen[pid] {
			t.Fatalf("进程 %d 被回收超过一次", pid)
		}
		seen[pid] = true
	}
	t.Logf("并发结束: Size=%d 回收数=%d（无重复回收、表项数不超容量）", tb.Size(), len(tb.Reaped()))
	checkInvariants(t, tb, capacity)
}

// 场景 9：相同的串行操作序列重放得到完全相同的表与回收顺序。
func TestReplayDeterministic(t *testing.T) {
	const capacity = 6
	script := func(tb *Table) {
		p2, _ := tb.Create(InitPID)
		p3, _ := tb.Create(p2)
		p4, _ := tb.Create(p2)
		_ = tb.Exit(p3, 3)
		_ = tb.Exit(p2, 2)
		_, _ = tb.Create(InitPID)
		_, _, _ = tb.WaitAny(InitPID)
		_ = tb.Exit(p4, 4)
	}
	tb1 := New(capacity)
	script(tb1)
	tb2 := New(capacity)
	script(tb2)
	if !reflect.DeepEqual(tb1.Reaped(), tb2.Reaped()) {
		t.Fatalf("回收顺序不一致: %v vs %v", tb1.Reaped(), tb2.Reaped())
	}
	if tb1.Size() != tb2.Size() {
		t.Fatalf("表项数不一致: %d vs %d", tb1.Size(), tb2.Size())
	}
	t.Logf("重放一致: Reaped=%v Size=%d", tb1.Reaped(), tb1.Size())
}
