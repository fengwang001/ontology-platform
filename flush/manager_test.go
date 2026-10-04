package flush

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func mustNew(t *testing.T, d int) *Manager {
	t.Helper()
	m, err := NewManager(d)
	if err != nil {
		t.Fatalf("NewManager(%d) rejected: %v", d, err)
	}
	return m
}

func expectOK(t *testing.T, op string, err *Error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: want success, got %v", op, err)
	}
}

func expectCode(t *testing.T, op string, err *Error, code Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: want rejection %v, got success", op, code)
	}
	if err.Code != code {
		t.Fatalf("%s: want code %v, got %v (%v)", op, code, err.Code, err)
	}
}

func expectDirty(t *testing.T, m *Manager, want []int) {
	t.Helper()
	if got := m.DirtyPages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DirtyPages: want %v, got %v", want, got)
	}
}

func expectDeps(t *testing.T, m *Manager, want map[int][]int) {
	t.Helper()
	if len(want) == 0 {
		want = map[int][]int{}
	}
	if got := m.Dependencies(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Dependencies: want %v, got %v", want, got)
	}
}

func expectPlan(t *testing.T, m *Manager, target int64, want []int) {
	t.Helper()
	got, err := m.Plan(target)
	expectOK(t, fmt.Sprintf("Plan(%d)", target), err)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Plan(%d): want %v, got %v", target, want, got)
	}
}

// TestSpecExample 复现题目给出的完整示例。
func TestSpecExample(t *testing.T) {
	m := mustNew(t, 10)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "Modify(2,20)", m.Modify(2, 20))
	expectOK(t, "Modify(3,30)", m.Modify(3, 30))
	if got := m.Checkpoint(); got != 10 {
		t.Fatalf("Checkpoint: want 10, got %d", got)
	}
	expectOK(t, "AddDep(3,1)", m.AddDep(3, 1))
	expectCode(t, "AddDep(1,3)", m.AddDep(1, 3), CodeDependencyCycle)
	expectPlan(t, m, 25, []int{3, 1, 2})
	expectOK(t, "SetFlushed(25)", m.SetFlushed(25))
	expectCode(t, "FlushStart(3) flushed=25", m.FlushStart(3), CodeLogNotFlushed)
	expectOK(t, "SetFlushed(30)", m.SetFlushed(30))
	expectCode(t, "FlushStart(1) pred dirty", m.FlushStart(1), CodePredecessorDirty)
	expectOK(t, "FlushStart(3)", m.FlushStart(3))
	expectOK(t, "Modify(3,40)", m.Modify(3, 40))
	expectOK(t, "FlushDone(3)", m.FlushDone(3))
	if pg := m.pages[3]; !pg.dirty || pg.inFlight || pg.oldest != 40 || pg.firstAfter != 0 {
		t.Fatalf("page 3 state after FlushDone: %+v", pg)
	}
	expectDeps(t, m, map[int][]int{3: {1}})
	if got := m.Checkpoint(); got != 10 {
		t.Fatalf("Checkpoint: want 10, got %d", got)
	}
	expectDirty(t, m, []int{1, 2, 3})
}

// TestModifyOnDirtyKeepsOldest 脏页（非在途）上的 Modify 不改变 oldest。
func TestModifyOnDirtyKeepsOldest(t *testing.T) {
	m := mustNew(t, 10)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "Modify(2,20)", m.Modify(2, 20))
	expectOK(t, "Modify(1,30)", m.Modify(1, 30))
	if pg := m.pages[1]; pg.oldest != 10 || pg.lsn != 30 {
		t.Fatalf("page 1: oldest=%d lsn=%d", pg.oldest, pg.lsn)
	}
	expectDirty(t, m, []int{1, 2})
	if got := m.Checkpoint(); got != 10 {
		t.Fatalf("Checkpoint: want 10, got %d", got)
	}
}

// TestFlushDoneReinsertsMiddle 在途期间被修改的页 FlushDone 后仍脏，
// 且按 oldest=firstAfter 插回链表中间而不是末尾。
func TestFlushDoneReinsertsMiddle(t *testing.T) {
	m := mustNew(t, 10)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "Modify(2,20)", m.Modify(2, 20))
	expectOK(t, "Modify(3,30)", m.Modify(3, 30))
	expectOK(t, "SetFlushed(30)", m.SetFlushed(30))
	// 页 1 在途期间被修改，firstAfter=40。
	expectOK(t, "FlushStart(1)", m.FlushStart(1))
	expectOK(t, "Modify(1,40)", m.Modify(1, 40))
	// 页 3 在途期间被修改，firstAfter=50，先完成，排到末尾。
	expectOK(t, "FlushStart(3)", m.FlushStart(3))
	expectOK(t, "Modify(3,50)", m.Modify(3, 50))
	expectOK(t, "FlushDone(3)", m.FlushDone(3))
	expectDirty(t, m, []int{1, 2, 3})
	// 页 1 完成时 oldest=40，应插到 2(20) 与 3(50) 之间。
	expectOK(t, "FlushDone(1)", m.FlushDone(1))
	expectDirty(t, m, []int{2, 1, 3})
	if pg := m.pages[1]; !pg.dirty || pg.inFlight || pg.oldest != 40 || pg.firstAfter != 0 {
		t.Fatalf("page 1 state: %+v", pg)
	}
	if got := m.Checkpoint(); got != 20 {
		t.Fatalf("Checkpoint: want 20, got %d", got)
	}
}

// TestFlushDoneCleanRemovesOutEdges 在途期间无修改时页变干净、离开链表并删除出边。
func TestFlushDoneCleanRemovesOutEdges(t *testing.T) {
	m := mustNew(t, 10)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "Modify(2,20)", m.Modify(2, 20))
	expectOK(t, "AddDep(1,2)", m.AddDep(1, 2))
	expectOK(t, "SetFlushed(20)", m.SetFlushed(20))
	expectOK(t, "FlushStart(1)", m.FlushStart(1))
	expectOK(t, "FlushDone(1)", m.FlushDone(1))
	expectDirty(t, m, []int{2})
	expectDeps(t, m, nil)
	if pg := m.pages[1]; pg.dirty || pg.inFlight {
		t.Fatalf("page 1 should be clean: %+v", pg)
	}
	expectCode(t, "FlushStart(1) clean", m.FlushStart(1), CodePageNotDirty)
}

// TestAddDepCleanSourceNotRegistered a 干净时 AddDep 返回成功但不登记。
func TestAddDepCleanSourceNotRegistered(t *testing.T) {
	m := mustNew(t, 10)
	expectOK(t, "Modify(2,10)", m.Modify(2, 10))
	expectOK(t, "AddDep(1,2) clean a", m.AddDep(1, 2))
	expectDeps(t, m, nil)
	// a 曾经脏过但已刷净，同样不登记。
	expectOK(t, "Modify(1,20)", m.Modify(1, 20))
	expectOK(t, "SetFlushed(20)", m.SetFlushed(20))
	expectOK(t, "FlushStart(1)", m.FlushStart(1))
	expectOK(t, "FlushDone(1)", m.FlushDone(1))
	expectOK(t, "AddDep(1,2) cleaned a", m.AddDep(1, 2))
	expectDeps(t, m, nil)
}

// TestAddDepIdempotent 重复登记幂等。
func TestAddDepIdempotent(t *testing.T) {
	m := mustNew(t, 10)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "Modify(2,20)", m.Modify(2, 20))
	expectOK(t, "AddDep(1,2) #1", m.AddDep(1, 2))
	expectOK(t, "AddDep(1,2) #2", m.AddDep(1, 2))
	expectDeps(t, m, map[int][]int{1: {2}})
}

// TestAddDepCycle 直接与间接成环均被拒绝，且拒绝不登记边。
func TestAddDepCycle(t *testing.T) {
	m := mustNew(t, 10)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "Modify(2,20)", m.Modify(2, 20))
	expectOK(t, "Modify(3,30)", m.Modify(3, 30))
	expectOK(t, "AddDep(1,2)", m.AddDep(1, 2))
	expectCode(t, "AddDep(2,1) direct cycle", m.AddDep(2, 1), CodeDependencyCycle)
	expectOK(t, "AddDep(2,3)", m.AddDep(2, 3))
	expectCode(t, "AddDep(3,1) indirect cycle", m.AddDep(3, 1), CodeDependencyCycle)
	expectDeps(t, m, map[int][]int{1: {2}, 2: {3}})
	expectOK(t, "AddDep(1,3) no cycle", m.AddDep(1, 3))
	expectDeps(t, m, map[int][]int{1: {2, 3}, 2: {3}})
	expectCode(t, "AddDep(1,1) self", m.AddDep(1, 1), CodeInvalidArgument)
}

// TestFlushStartRejectOrder 覆盖四类拒绝及其优先级顺序。
func TestFlushStartRejectOrder(t *testing.T) {
	m := mustNew(t, 10)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "Modify(2,20)", m.Modify(2, 20))
	expectOK(t, "Modify(3,30)", m.Modify(3, 30))
	expectOK(t, "AddDep(2,3)", m.AddDep(2, 3))
	expectOK(t, "SetFlushed(20)", m.SetFlushed(20))
	// 1) 页不脏：即使其余条件也不满足，也只报不脏。
	expectCode(t, "FlushStart(9) clean", m.FlushStart(9), CodePageNotDirty)
	// 2) 页已在途：在途页即便 lsn 已超过水位，也先报在途。
	expectOK(t, "FlushStart(1)", m.FlushStart(1))
	expectOK(t, "Modify(1,40)", m.Modify(1, 40))
	expectCode(t, "FlushStart(1) in flight", m.FlushStart(1), CodePageInFlight)
	// 3) 日志未落盘：页 2 的 lsn=20 不大于水位，先把它弄脏到 50。
	expectOK(t, "Modify(2,50)", m.Modify(2, 50))
	expectCode(t, "FlushStart(2) log not flushed", m.FlushStart(2), CodeLogNotFlushed)
	// 4) 前置页未刷：水位抬到 50 后，页 3 的前置页 2 仍脏。
	expectOK(t, "SetFlushed(50)", m.SetFlushed(50))
	expectCode(t, "FlushStart(3) pred dirty", m.FlushStart(3), CodePredecessorDirty)
	// 前置页刷净后（在途无修改），页 3 可以开始。
	expectOK(t, "FlushStart(2)", m.FlushStart(2))
	expectOK(t, "FlushDone(2)", m.FlushDone(2))
	expectOK(t, "FlushStart(3)", m.FlushStart(3))
}

// TestFlushStartLogBoundary 落盘水位恰等于 p.lsn 允许，少 1 被拒。
func TestFlushStartLogBoundary(t *testing.T) {
	m := mustNew(t, 10)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "SetFlushed(9)", m.SetFlushed(9))
	expectCode(t, "FlushStart(1) flushed=9", m.FlushStart(1), CodeLogNotFlushed)
	expectOK(t, "SetFlushed(10)", m.SetFlushed(10))
	expectOK(t, "FlushStart(1) flushed=10", m.FlushStart(1))
}

// TestInFlightPredecessorBlocks 在途前置页仍阻塞后继页。
func TestInFlightPredecessorBlocks(t *testing.T) {
	m := mustNew(t, 10)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "Modify(2,20)", m.Modify(2, 20))
	expectOK(t, "AddDep(1,2)", m.AddDep(1, 2))
	expectOK(t, "SetFlushed(20)", m.SetFlushed(20))
	expectOK(t, "FlushStart(1)", m.FlushStart(1))
	expectCode(t, "FlushStart(2) pred in flight", m.FlushStart(2), CodePredecessorDirty)
	expectOK(t, "FlushDone(1)", m.FlushDone(1))
	expectOK(t, "FlushStart(2)", m.FlushStart(2))
}

// TestCheckpointEmptyList 链表为空时 Checkpoint 取最大 Modify LSN 加一。
func TestCheckpointEmptyList(t *testing.T) {
	m := mustNew(t, 10)
	if got := m.Checkpoint(); got != 1 {
		t.Fatalf("Checkpoint before any Modify: want 1, got %d", got)
	}
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	if got := m.Checkpoint(); got != 10 {
		t.Fatalf("Checkpoint: want 10, got %d", got)
	}
	expectOK(t, "SetFlushed(10)", m.SetFlushed(10))
	expectOK(t, "FlushStart(1)", m.FlushStart(1))
	expectOK(t, "FlushDone(1)", m.FlushDone(1))
	if got := m.Checkpoint(); got != 11 {
		t.Fatalf("Checkpoint on empty list: want 11, got %d", got)
	}
}

// TestPlanPullsPredecessorBeyondTarget 前置页即使 oldest 不小于 target 也被拉入计划。
func TestPlanPullsPredecessorBeyondTarget(t *testing.T) {
	m := mustNew(t, 10)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "Modify(2,20)", m.Modify(2, 20))
	expectOK(t, "AddDep(2,1)", m.AddDep(2, 1))
	expectPlan(t, m, 15, []int{2, 1})
	// 计划不改变任何状态。
	expectDirty(t, m, []int{1, 2})
	expectDeps(t, m, map[int][]int{2: {1}})
}

// TestPlanMultiplePredecessors 多个前置页按页号升序展开，跳过在途页与已入计划页。
func TestPlanMultiplePredecessors(t *testing.T) {
	m := mustNew(t, 10)
	for i := 1; i <= 5; i++ {
		expectOK(t, fmt.Sprintf("Modify(%d,%d)", i, i*10), m.Modify(i, int64(i*10)))
	}
	expectOK(t, "AddDep(4,1)", m.AddDep(4, 1))
	expectOK(t, "AddDep(2,1)", m.AddDep(2, 1))
	expectOK(t, "AddDep(3,1)", m.AddDep(3, 1))
	expectOK(t, "SetFlushed(50)", m.SetFlushed(50))
	expectOK(t, "FlushStart(2)", m.FlushStart(2))
	// 页 2 在途，作为前置页被跳过；3、4 按页号升序先入计划。
	expectPlan(t, m, 15, []int{3, 4, 1})
	// target 覆盖到 3 时，3 已在计划中，不重复展开。
	expectPlan(t, m, 35, []int{3, 4, 1})
	// 在途页在顶层遍历中同样被跳过。
	expectPlan(t, m, 100, []int{3, 4, 1, 5})
}

// TestDirtyFullOnlyRejectsCleanPages 脏页已满只拒干净页的 Modify，且被拒的 Modify 不推进最大 LSN。
func TestDirtyFullOnlyRejectsCleanPages(t *testing.T) {
	m := mustNew(t, 2)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "Modify(2,20)", m.Modify(2, 20))
	expectCode(t, "Modify(3,30) full", m.Modify(3, 30), CodeDirtyPoolFull)
	// 被拒的 Modify 不推进最大 LSN：25 仍应被接受。
	expectOK(t, "Modify(1,25) dirty page", m.Modify(1, 25))
	expectCode(t, "Modify(3,26) still full", m.Modify(3, 26), CodeDirtyPoolFull)
	// LSN 检查先于脏页满检查。
	expectCode(t, "Modify(3,20) stale lsn", m.Modify(3, 20), CodeLSNNotAdvanced)
	expectDirty(t, m, []int{1, 2})
}

// TestRejectedOpsKeepState 各类被拒操作不改变页状态、链表、依赖边与落盘水位。
func TestRejectedOpsKeepState(t *testing.T) {
	m := mustNew(t, 2)
	expectOK(t, "Modify(1,10)", m.Modify(1, 10))
	expectOK(t, "Modify(2,20)", m.Modify(2, 20))
	expectOK(t, "AddDep(1,2)", m.AddDep(1, 2))
	expectOK(t, "SetFlushed(20)", m.SetFlushed(20))
	snapshot := func() (int64, []int, map[int][]int, int64) {
		return m.Checkpoint(), m.DirtyPages(), m.Dependencies(), m.flushed
	}
	beforeCp, beforeDirty, beforeDeps, beforeFlushed := snapshot()
	rejected := []struct {
		name string
		err  *Error
		code Code
	}{
		{"NewManager(0)", func() *Error { _, e := NewManager(0); return e }(), CodeInvalidArgument},
		{"NewManager(1e6+1)", func() *Error { _, e := NewManager(MaxD + 1); return e }(), CodeInvalidArgument},
		{"Modify(-1,30)", m.Modify(-1, 30), CodeInvalidArgument},
		{"Modify(1,0)", m.Modify(1, 0), CodeInvalidArgument},
		{"Modify(1,1e15+1)", m.Modify(1, MaxLSN+1), CodeInvalidArgument},
		{"Modify(1,20) stale", m.Modify(1, 20), CodeLSNNotAdvanced},
		{"Modify(3,30) full", m.Modify(3, 30), CodeDirtyPoolFull},
		{"SetFlushed(-1)", m.SetFlushed(-1), CodeInvalidArgument},
		{"SetFlushed(19) regression", m.SetFlushed(19), CodeLSNNotAdvanced},
		{"FlushStart(9) clean", m.FlushStart(9), CodePageNotDirty},
		{"FlushStart(2) pred dirty", m.FlushStart(2), CodePredecessorDirty},
		{"FlushDone(1) not in flight", m.FlushDone(1), CodePageNotInFlight},
		{"AddDep(1,1)", m.AddDep(1, 1), CodeInvalidArgument},
		{"AddDep(2,1) cycle", m.AddDep(2, 1), CodeDependencyCycle},
	}
	for _, r := range rejected {
		expectCode(t, r.name, r.err, r.code)
	}
	if _, err := m.Plan(0); err == nil || err.Code != CodeInvalidArgument {
		t.Fatalf("Plan(0): want invalid argument, got %v", err)
	}
	afterCp, afterDirty, afterDeps, afterFlushed := snapshot()
	if beforeCp != afterCp || beforeFlushed != afterFlushed ||
		!reflect.DeepEqual(beforeDirty, afterDirty) || !reflect.DeepEqual(beforeDeps, afterDeps) {
		t.Fatalf("state changed by rejected ops: before=(%d,%v,%v,%d) after=(%d,%v,%v,%d)",
			beforeCp, beforeDirty, beforeDeps, beforeFlushed,
			afterCp, afterDirty, afterDeps, afterFlushed)
	}
	if m.maxLSN != 20 {
		t.Fatalf("maxLSN advanced by rejected Modify: %d", m.maxLSN)
	}
}

// TestConcurrentSmoke 并发调用下不变量始终成立（配合 -race 运行）。
func TestConcurrentSmoke(t *testing.T) {
	m := mustNew(t, 64)
	var lsn atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				p := (g*7 + i) % 32
				switch i % 7 {
				case 0:
					m.Modify(p, lsn.Add(1))
				case 1:
					m.SetFlushed(lsn.Load())
				case 2:
					m.FlushStart(p)
				case 3:
					m.FlushDone(p)
				case 4:
					m.AddDep(p, (p+1)%32)
				case 5:
					m.Checkpoint()
				case 6:
					m.Plan(lsn.Load() + 1)
				}
			}
		}(g)
	}
	wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	var prev int64
	count := 0
	for e := m.list.Front(); e != nil; e = e.Next() {
		pg := m.pages[e.Value.(int)]
		if !pg.dirty {
			t.Fatalf("clean page %d in list", e.Value)
		}
		if pg.oldest > pg.lsn {
			t.Fatalf("page %d oldest=%d > lsn=%d", e.Value, pg.oldest, pg.lsn)
		}
		if count > 0 && pg.oldest <= prev {
			t.Fatalf("list oldest not strictly ascending: %d then %d", prev, pg.oldest)
		}
		prev = pg.oldest
		count++
	}
	if count > m.limit {
		t.Fatalf("dirty count %d exceeds limit %d", count, m.limit)
	}
	for a, bs := range m.outEdges {
		if !m.pages[a].dirty {
			t.Fatalf("edge from clean page %d", a)
		}
		for b := range bs {
			if m.reachable(b, a) {
				t.Fatalf("cycle via edge %d->%d", a, b)
			}
		}
	}
}
