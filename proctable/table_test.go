package proctable

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// step 在测试日志中打印一次操作的输入、输出与判定依据。
func step(t *testing.T, input, output, basis string) {
	t.Helper()
	t.Logf("输入: %s | 输出: %s | 判定依据: %s", input, output, basis)
}

// checkInvariants 校验进程表的核心不变量：
// 表项数不超过容量；僵尸没有任何子进程；除始进程外每个表项的父进程存活。
func checkInvariants(t *testing.T, tb *Table) {
	t.Helper()
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if len(tb.entries) > tb.capacity {
		t.Fatalf("不变量违反: 表项数 %d 超过容量 %d", len(tb.entries), tb.capacity)
	}
	for pid, e := range tb.entries {
		if !e.Alive && len(tb.children[pid]) > 0 {
			t.Fatalf("不变量违反: 僵尸 %d 仍有子进程 %v", pid, tb.children[pid])
		}
		if pid == InitPID {
			continue
		}
		parent, ok := tb.entries[e.Parent]
		if !ok || !parent.Alive {
			t.Fatalf("不变量违反: 进程 %d 的父进程 %d 不存活", pid, e.Parent)
		}
	}
	for parent, kids := range tb.children {
		for child := range kids {
			if tb.entries[child].Parent != parent {
				t.Fatalf("不变量违反: 子进程索引 %d->%d 与表项父指针不一致", parent, child)
			}
		}
	}
}

// TestExitWithZombieChildrenFreesEntries 验证父进程带着僵尸子进程退出时，
// 归始进程的僵尸子进程与父进程自身的表项立即释放，并可再创建。
func TestExitWithZombieChildrenFreesEntries(t *testing.T) {
	tb := NewTable(3)
	step(t, "NewTable(3)", tb.String(), "始进程 1 建表时已存在")

	p2, err := tb.Create(InitPID)
	if err != nil {
		t.Fatalf("Create(1) 失败: %v", err)
	}
	p3, err := tb.Create(p2)
	if err != nil {
		t.Fatalf("Create(2) 失败: %v", err)
	}
	step(t, "Create(1), Create(2)", fmt.Sprintf("pid=%d,%d %s", p2, p3, tb.String()), "表未满且父进程存活")

	if err := tb.Exit(p3, 7); err != nil {
		t.Fatalf("Exit(3) 失败: %v", err)
	}
	step(t, "Exit(3,7)", tb.String(), "父进程 2 存活，3 成为保留退出码的僵尸")
	if tb.Len() != 3 {
		t.Fatalf("退出后表项数应为 3，实际 %d", tb.Len())
	}

	if err := tb.Exit(p2, 0); err != nil {
		t.Fatalf("Exit(2) 失败: %v", err)
	}
	step(t, "Exit(2,0)", tb.String(), "僵尸子进程 3 改挂始进程后立即回收；2 的父进程是始进程，也立即回收")
	if tb.Len() != 1 {
		t.Fatalf("表项应立即释放到只剩始进程，实际 %d", tb.Len())
	}
	if _, ok := tb.Lookup(p3); ok {
		t.Fatalf("僵尸子进程 %d 的表项应已释放", p3)
	}
	if _, ok := tb.Lookup(p2); ok {
		t.Fatalf("父进程 %d 的表项应已释放", p2)
	}

	p4, err := tb.Create(InitPID)
	if err != nil {
		t.Fatalf("回收后应可再创建: %v", err)
	}
	step(t, "Create(1)", fmt.Sprintf("pid=%d %s", p4, tb.String()), "表项已释放，容量恢复")
	if p4 != 4 {
		t.Fatalf("新编号应严格递增为 4，实际 %d", p4)
	}
	checkInvariants(t, tb)
}

// TestExitWithInitParentReapedImmediately 验证父进程为始进程的退出者
// 不留下僵尸，表项立即被回收。
func TestExitWithInitParentReapedImmediately(t *testing.T) {
	tb := NewTable(4)
	p2, err := tb.Create(InitPID)
	if err != nil {
		t.Fatalf("Create(1) 失败: %v", err)
	}
	step(t, "Create(1)", fmt.Sprintf("pid=%d %s", p2, tb.String()), "父进程为始进程")

	if err := tb.Exit(p2, 42); err != nil {
		t.Fatalf("Exit(2) 失败: %v", err)
	}
	step(t, "Exit(2,42)", tb.String(), "退出者的父进程是始进程，立即被回收")
	if tb.Len() != 1 {
		t.Fatalf("表项应立即回收，实际剩余 %d", tb.Len())
	}
	if _, ok := tb.Lookup(p2); ok {
		t.Fatalf("进程 %d 应已被回收", p2)
	}
	checkInvariants(t, tb)
}

// TestWaitAnyReapsInZombieOrder 验证等待任一子进程时按成为僵尸的
// 先后顺序回收，并区分「仍有存活子进程」与「没有任何子进程」。
func TestWaitAnyReapsInZombieOrder(t *testing.T) {
	tb := NewTable(8)
	p2, _ := tb.Create(InitPID)
	p3, _ := tb.Create(p2)
	p4, _ := tb.Create(p2)
	p5, _ := tb.Create(p2)
	step(t, "Create(1);Create(2)x3", tb.String(), "进程 2 下有子进程 3,4,5")

	// 退出顺序 4,3,5：成为僵尸的先后为 4 < 3 < 5。
	for _, pid := range []int{p4, p3, p5} {
		if err := tb.Exit(pid, pid*10); err != nil {
			t.Fatalf("Exit(%d) 失败: %v", pid, err)
		}
	}
	step(t, "Exit(4,40);Exit(3,30);Exit(5,50)", tb.String(), "僵尸顺序 4<3<5")

	for _, want := range []struct{ pid, code int }{{p4, 40}, {p3, 30}, {p5, 50}} {
		gotPID, gotCode, err := tb.WaitAny(p2)
		step(t, fmt.Sprintf("WaitAny(%d)", p2), fmt.Sprintf("pid=%d code=%d err=%v", gotPID, gotCode, err),
			"回收子进程中最早成为僵尸者")
		if err != nil || gotPID != want.pid || gotCode != want.code {
			t.Fatalf("WaitAny 应为 (%d,%d)，实际 (%d,%d,%v)", want.pid, want.code, gotPID, gotCode, err)
		}
	}

	_, _, err := tb.WaitAny(p2)
	step(t, "WaitAny(2)", fmt.Sprintf("err=%v", err), "子进程已全部回收，没有任何子进程")
	if !errors.Is(err, ErrNoChildren) {
		t.Fatalf("应为 ErrNoChildren，实际 %v", err)
	}

	p6, _ := tb.Create(p2)
	_, _, err = tb.WaitAny(p2)
	step(t, fmt.Sprintf("Create(2)=%d; WaitAny(2)", p6), fmt.Sprintf("err=%v", err),
		"仍有存活子进程但暂无僵尸")
	if !errors.Is(err, ErrNoZombie) {
		t.Fatalf("应为 ErrNoZombie，实际 %v", err)
	}
	checkInvariants(t, tb)
}

// TestTableFullThenReapResumesCreate 验证表满时拒绝创建，回收后恢复创建。
func TestTableFullThenReapResumesCreate(t *testing.T) {
	tb := NewTable(3)
	p2, _ := tb.Create(InitPID)
	p3, _ := tb.Create(p2)
	step(t, "Create(1);Create(2)", tb.String(), "表项数达到容量 3")

	before := tb.String()
	_, err := tb.Create(InitPID)
	step(t, "Create(1)", fmt.Sprintf("err=%v", err), "表已满，整体拒绝")
	if !errors.Is(err, ErrTableFull) {
		t.Fatalf("应为 ErrTableFull，实际 %v", err)
	}
	if tb.String() != before {
		t.Fatalf("被拒绝的操作不得改变状态: %s -> %s", before, tb.String())
	}

	if err := tb.Exit(p3, 9); err != nil {
		t.Fatalf("Exit(3) 失败: %v", err)
	}
	_, err = tb.Create(InitPID)
	step(t, "Exit(3,9); Create(1)", fmt.Sprintf("err=%v %s", err, tb.String()),
		"3 的父进程 2 存活，3 成为僵尸仍占表项，表仍满")
	if !errors.Is(err, ErrTableFull) {
		t.Fatalf("僵尸仍占表项，应为 ErrTableFull，实际 %v", err)
	}

	code, err := tb.Wait(p2, p3)
	step(t, "Wait(2,3)", fmt.Sprintf("code=%d err=%v %s", code, err, tb.String()),
		"回收僵尸子进程，表项释放")
	if err != nil || code != 9 {
		t.Fatalf("Wait 应返回退出码 9，实际 (%d,%v)", code, err)
	}

	p4, err := tb.Create(InitPID)
	step(t, "Create(1)", fmt.Sprintf("pid=%d err=%v %s", p4, err, tb.String()),
		"回收后容量恢复，可再创建")
	if err != nil || p4 != 4 {
		t.Fatalf("回收后应能创建 pid=4，实际 (%d,%v)", p4, err)
	}
	checkInvariants(t, tb)
}

// TestMultiLevelReparent 验证多层改挂：退出者的子进程（含僵尸）逐层
// 改挂到始进程，归始进程的僵尸立即回收。
func TestMultiLevelReparent(t *testing.T) {
	tb := NewTable(8)
	// 链式结构 1<-2<-3<-4<-5。
	p2, _ := tb.Create(InitPID)
	p3, _ := tb.Create(p2)
	p4, _ := tb.Create(p3)
	p5, _ := tb.Create(p4)
	step(t, "链式创建 2<-3<-4<-5", tb.String(), "四层父子关系")

	if err := tb.Exit(p5, 55); err != nil {
		t.Fatalf("Exit(5) 失败: %v", err)
	}
	step(t, "Exit(5,55)", tb.String(), "父进程 4 存活，5 成为僵尸")

	if err := tb.Exit(p3, 33); err != nil {
		t.Fatalf("Exit(3) 失败: %v", err)
	}
	e4, _ := tb.Lookup(p4)
	step(t, "Exit(3,33)", tb.String(), "4 改挂到始进程；3 的父进程 2 存活，3 保持僵尸")
	if e4.Parent != InitPID {
		t.Fatalf("进程 4 的父进程应改挂为始进程，实际 %d", e4.Parent)
	}

	if err := tb.Exit(p2, 22); err != nil {
		t.Fatalf("Exit(2) 失败: %v", err)
	}
	step(t, "Exit(2,22)", tb.String(), "僵尸子进程 3 改挂始进程后立即回收；2 的父进程是始进程，也立即回收")
	if _, ok := tb.Lookup(p3); ok {
		t.Fatalf("僵尸 3 应已被回收")
	}
	if _, ok := tb.Lookup(p2); ok {
		t.Fatalf("进程 2 应已被回收")
	}

	if err := tb.Exit(p4, 44); err != nil {
		t.Fatalf("Exit(4) 失败: %v", err)
	}
	step(t, "Exit(4,44)", tb.String(), "僵尸子进程 5 改挂始进程后立即回收；4 的父进程是始进程，也立即回收")
	if tb.Len() != 1 {
		t.Fatalf("全部回收后应只剩始进程，实际 %d", tb.Len())
	}
	checkInvariants(t, tb)
}

// TestRejectedOperations 验证各类拒绝原因可区分、多因同时成立时按
// 「不存在、已是僵尸、其余」的固定顺序只报第一个，且被拒绝的操作
// 不改变任何状态。
func TestRejectedOperations(t *testing.T) {
	tb := NewTable(3)
	p2, _ := tb.Create(InitPID)
	p3, _ := tb.Create(p2) // 表已满：1,2,3
	if err := tb.Exit(p3, 1); err != nil {
		t.Fatalf("Exit(3) 失败: %v", err)
	}
	// 当前状态：1 存活，2 存活，3 是 2 的僵尸子进程，表满。
	step(t, "构造场景", tb.String(), "1,2 存活；3 为僵尸；表满")

	cases := []struct {
		name  string
		op    func() error
		want  error
		basis string
	}{
		{"创建-父不存在", func() error { _, e := tb.Create(999); return e }, ErrNotExist, "父进程不存在优先于表满"},
		{"创建-父是僵尸且表满", func() error { _, e := tb.Create(p3); return e }, ErrZombie, "已是僵尸优先于表满"},
		{"创建-表满", func() error { _, e := tb.Create(InitPID); return e }, ErrTableFull, "父进程存活但表已满"},
		{"退出-不存在", func() error { return tb.Exit(999, 0) }, ErrNotExist, "进程不存在"},
		{"退出-僵尸", func() error { return tb.Exit(p3, 0) }, ErrZombie, "僵尸不能再次退出"},
		{"退出-始进程", func() error { return tb.Exit(InitPID, 0) }, ErrInitExit, "始进程永不退出"},
		{"等待-调用者不存在", func() error { _, e := tb.Wait(999, p3); return e }, ErrNotExist, "调用者不存在"},
		{"等待-目标不存在", func() error { _, e := tb.Wait(p2, 999); return e }, ErrNotExist, "目标不存在"},
		{"等待-非子进程", func() error { _, e := tb.Wait(InitPID, p3); return e }, ErrNotChild, "3 不是 1 的子进程"},
		{"等待-子进程存活", func() error { _, e := tb.Wait(InitPID, p2); return e }, ErrChildAlive, "指定子进程仍存活"},
		{"等待任一-调用者不存在", func() error { _, _, e := tb.WaitAny(999); return e }, ErrNotExist, "调用者不存在"},
	}
	for _, tc := range cases {
		before := tb.String()
		err := tc.op()
		step(t, tc.name, fmt.Sprintf("err=%v", err), tc.basis)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: 应为 %v，实际 %v", tc.name, tc.want, err)
		}
		if tb.String() != before {
			t.Fatalf("%s: 被拒绝的操作改变了状态: %s -> %s", tc.name, before, tb.String())
		}
	}

	// 对僵尸调用创建/退出/等待一律报 ErrZombie。
	for _, tc := range []struct {
		name string
		op   func() error
	}{
		{"僵尸上创建", func() error { _, e := tb.Create(p3); return e }},
		{"僵尸退出", func() error { return tb.Exit(p3, 0) }},
		{"僵尸等待", func() error { _, e := tb.Wait(p3, p2); return e }},
		{"僵尸等待任一", func() error { _, _, e := tb.WaitAny(p3); return e }},
	} {
		before := tb.String()
		err := tc.op()
		step(t, tc.name, fmt.Sprintf("err=%v", err), "对僵尸的操作一律拒绝")
		if !errors.Is(err, ErrZombie) {
			t.Fatalf("%s: 应为 ErrZombie，实际 %v", tc.name, err)
		}
		if tb.String() != before {
			t.Fatalf("%s: 被拒绝的操作改变了状态", tc.name)
		}
	}

	// 无子进程与仅有存活子进程的区分。
	if _, _, err := tb.WaitAny(InitPID); !errors.Is(err, ErrNoZombie) {
		t.Fatalf("始进程仅有存活子进程 2，应为 ErrNoZombie，实际 %v", err)
	}
	step(t, "WaitAny(1)", "err=ErrNoZombie", "仍有存活子进程但暂无僵尸")
	if _, _, err := tb.WaitAny(p3); !errors.Is(err, ErrZombie) {
		t.Fatalf("僵尸调用 WaitAny 应为 ErrZombie，实际 %v", err)
	}
	code, err := tb.Wait(p2, p3)
	if err != nil || code != 1 {
		t.Fatalf("Wait(2,3) 应回收并返回退出码 1，实际 (%d,%v)", code, err)
	}
	step(t, "Wait(2,3)", fmt.Sprintf("code=%d %s", code, tb.String()), "回收僵尸子进程")
	if _, _, err := tb.WaitAny(p2); !errors.Is(err, ErrNoChildren) {
		t.Fatalf("2 已无子进程，应为 ErrNoChildren，实际 %v", err)
	}
	step(t, "WaitAny(2)", "err=ErrNoChildren", "没有任何子进程")
	checkInvariants(t, tb)
}

// TestConcurrentAccess 验证创建、退出、等待与查询可并发调用，
// 任何时刻表项数不超过容量，不变量保持成立。
func TestConcurrentAccess(t *testing.T) {
	tb := NewTable(64)
	const workers = 8
	const rounds = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < rounds; i++ {
				target := 1 + rng.Intn(128)
				switch rng.Intn(6) {
				case 0:
					_, _ = tb.Create(target)
				case 1:
					_ = tb.Exit(target, rng.Intn(256))
				case 2:
					_, _ = tb.Wait(target, 1+rng.Intn(128))
				case 3:
					_, _, _ = tb.WaitAny(target)
				case 4:
					_, _ = tb.Lookup(target)
				case 5:
					_ = tb.Children(target)
				}
				if n := tb.Len(); n > tb.Capacity() {
					t.Errorf("表项数 %d 超过容量 %d", n, tb.Capacity())
					return
				}
			}
		}(int64(w) + 1)
	}
	wg.Wait()
	step(t, "8 协程 x 200 次混合操作", tb.String(), "并发下表项数始终不超过容量")
	checkInvariants(t, tb)
}

// TestReplayDeterminism 验证相同的操作序列重放得到完全相同的表与
// 回收顺序。
func TestReplayDeterminism(t *testing.T) {
	run := func() (string, []string) {
		tb := NewTable(16)
		rng := rand.New(rand.NewSource(992))
		var trace []string
		for i := 0; i < 500; i++ {
			target := 1 + rng.Intn(64)
			var out string
			switch rng.Intn(4) {
			case 0:
				pid, err := tb.Create(target)
				out = fmt.Sprintf("Create(%d)=(%d,%v)", target, pid, err)
			case 1:
				out = fmt.Sprintf("Exit(%d)=%v", target, tb.Exit(target, i))
			case 2:
				code, err := tb.Wait(target, 1+rng.Intn(64))
				out = fmt.Sprintf("Wait(%d)=(%d,%v)", target, code, err)
			case 3:
				pid, code, err := tb.WaitAny(target)
				out = fmt.Sprintf("WaitAny(%d)=(%d,%d,%v)", target, pid, code, err)
			}
			trace = append(trace, out)
		}
		return tb.String(), trace
	}
	table1, trace1 := run()
	table2, trace2 := run()
	if table1 != table2 {
		t.Fatalf("重放后表不一致:\n%s\n%s", table1, table2)
	}
	if len(trace1) != len(trace2) {
		t.Fatalf("重放轨迹长度不一致")
	}
	for i := range trace1 {
		if trace1[i] != trace2[i] {
			t.Fatalf("第 %d 步重放结果不一致:\n%s\n%s", i, trace1[i], trace2[i])
		}
	}
	step(t, "固定种子操作序列重放两次", table1, "表与回收顺序完全一致")
}
