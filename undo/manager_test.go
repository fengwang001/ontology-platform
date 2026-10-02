package undo

import (
	"reflect"
	"sync"
	"testing"
)

func mustManager(t *testing.T, s, k, pb int) *Manager {
	t.Helper()
	m, err := NewManager(s, k, pb)
	if err != nil {
		t.Fatalf("NewManager(%d,%d,%d): %v", s, k, pb, err)
	}
	return m
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustCode(t *testing.T, err error, code Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", code)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T (%v)", err, err)
	}
	if e.Code != code {
		t.Fatalf("expected code %v, got %v (%v)", code, e.Code, err)
	}
}

func mustSnap(t *testing.T, m *Manager, want Snapshot) {
	t.Helper()
	got := m.Snapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func mustPurge(t *testing.T, m *Manager, n int, want []int) {
	t.Helper()
	got, err := m.Purge(n)
	if err != nil {
		t.Fatalf("Purge(%d): %v", n, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Purge(%d) = %v, want %v", n, got, want)
	}
}

// 规格中的完整示例回放。
func TestSpecExample(t *testing.T) {
	m := mustManager(t, 3, 4, 100)
	mustOK(t, m.Begin(1))
	mustOK(t, m.Modify(1))
	mustOK(t, m.Modify(1))
	mustOK(t, m.Insert(1))
	mustOK(t, m.Commit(1)) // trx_no=1：I1(n=1) 进 I 缓存，U1(n=2) 入历史链 [1]
	mustSnap(t, m, Snapshot{UsedSlots: 2, UsedPages: 2, CacheI: []int{1}, History: []int{1}, IssuedTrx: 1})

	v := m.OpenView() // 视图 1，limit=2
	if v != 1 {
		t.Fatalf("OpenView = %d, want 1", v)
	}
	mustOK(t, m.Begin(2))
	mustOK(t, m.Modify(2)) // 无 U 缓存，占用最后一个空闲槽位
	mustSnap(t, m, Snapshot{UsedSlots: 3, UsedPages: 3, CacheI: []int{1}, History: []int{1}, IssuedTrx: 1, OpenViews: 1})
	mustOK(t, m.Commit(2)) // trx_no=2，历史链 [1,2]
	mustSnap(t, m, Snapshot{UsedSlots: 3, UsedPages: 3, CacheI: []int{1}, History: []int{1, 2}, IssuedTrx: 2, OpenViews: 1})

	mustPurge(t, m, 10, []int{1}) // PL=2，回收 trx 1（U1 进 U 缓存），2 不小于 2 即停
	mustSnap(t, m, Snapshot{UsedSlots: 3, UsedPages: 3, CacheI: []int{1}, CacheU: []int{2}, History: []int{2}, IssuedTrx: 2, OpenViews: 1})

	mustOK(t, m.CloseView(v))
	mustPurge(t, m, 10, []int{2}) // PL=3，回收 trx 2，U2 成为 U 缓存栈顶
	mustSnap(t, m, Snapshot{UsedSlots: 3, UsedPages: 3, CacheI: []int{1}, CacheU: []int{2, 1}, IssuedTrx: 2})
}

// n 恰等于 3K/4 进缓存，多 1 条则整体释放。
func TestCacheBoundaryExactlyThreeQuarters(t *testing.T) {
	m := mustManager(t, 4, 4, 100)
	mustOK(t, m.Begin(1))
	for i := 0; i < 3; i++ { // n=3，4n=12 不大于 3K=12
		mustOK(t, m.Modify(1))
	}
	mustOK(t, m.Commit(1))       // U 段先入历史链
	mustPurge(t, m, 1, []int{1}) // 回收时 Release：n=3 进 U 缓存
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 1, CacheU: []int{3}, IssuedTrx: 1})

	mustOK(t, m.Begin(2))
	for i := 0; i < 4; i++ { // n=4，虽占 1 页但 4n=16>12，整体释放
		mustOK(t, m.Modify(2))
	}
	mustOK(t, m.Commit(2)) // tx2 复用了缓存段（n 清零后长到 4）
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 1, History: []int{2}, IssuedTrx: 2})
	mustPurge(t, m, 1, []int{2}) // 4n=16>12，整体释放，不进缓存
	mustSnap(t, m, Snapshot{IssuedTrx: 2})
}

// 多页段不进缓存。
func TestMultiPageSegmentNotCached(t *testing.T) {
	m := mustManager(t, 4, 2, 100)
	mustOK(t, m.Begin(1))
	for i := 0; i < 3; i++ { // n=3 -> 2 页
		mustOK(t, m.Modify(1))
	}
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 2})
	mustOK(t, m.Commit(1)) // 2 页段直接整体释放，历史链段被 Purge 后也不进缓存
	mustPurge(t, m, 1, []int{1})
	mustSnap(t, m, Snapshot{IssuedTrx: 1})
}

// 缓存段占着槽位，使另一种段申请失败（先查槽位）。
func TestCacheHoldsSlotBlocksOtherKind(t *testing.T) {
	m := mustManager(t, 1, 4, 100)
	mustOK(t, m.Begin(1))
	mustOK(t, m.Insert(1))
	mustOK(t, m.Commit(1)) // I 段进缓存，仍占唯一槽位
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 1, CacheI: []int{1}, IssuedTrx: 1})

	mustOK(t, m.Begin(2))
	mustCode(t, m.Modify(2), CodeNoFreeSlot) // U 缓存为空且无空闲槽位
	mustOK(t, m.Insert(2))                   // I 缓存可复用，不检查槽位
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 1, IssuedTrx: 1})
}

// 缓存后进先出复用。
func TestCacheLIFO(t *testing.T) {
	m := mustManager(t, 4, 4, 100)
	mustOK(t, m.Begin(1))
	mustOK(t, m.Modify(1)) // U_a: n=1
	mustOK(t, m.Commit(1))
	mustOK(t, m.Begin(2))
	mustOK(t, m.Modify(2))
	mustOK(t, m.Modify(2)) // U_b: n=2
	mustOK(t, m.Commit(2))
	mustPurge(t, m, 10, []int{1, 2}) // 缓存栈底->顶：[1, 2]
	mustSnap(t, m, Snapshot{UsedSlots: 2, UsedPages: 2, CacheU: []int{1, 2}, IssuedTrx: 2})

	mustOK(t, m.Begin(3))
	mustOK(t, m.Modify(3)) // 弹出栈顶 n=2 的段
	mustSnap(t, m, Snapshot{UsedSlots: 2, UsedPages: 2, CacheU: []int{1}, IssuedTrx: 2})
	mustOK(t, m.Begin(4))
	mustOK(t, m.Modify(4)) // 再弹出 n=1 的段
	mustSnap(t, m, Snapshot{UsedSlots: 2, UsedPages: 2, IssuedTrx: 2})
}

// 复用缓存段不耗槽位与页。
func TestReuseConsumesNoSlotOrPage(t *testing.T) {
	m := mustManager(t, 2, 4, 2)
	mustOK(t, m.Begin(1))
	mustOK(t, m.Insert(1))
	mustOK(t, m.Commit(1)) // I 缓存 1 段，槽位 1/2、页 1/2
	mustOK(t, m.Begin(2))
	mustOK(t, m.Insert(2)) // 复用缓存段，槽位页均不变
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 1, IssuedTrx: 1})
}

// 插入 undo 提交时立即 Release，更新 undo 入历史链。
func TestInsertReleasedAtCommitUpdateToHistory(t *testing.T) {
	m := mustManager(t, 4, 4, 100)
	mustOK(t, m.Begin(1))
	mustOK(t, m.Insert(1))
	mustOK(t, m.Modify(1))
	mustOK(t, m.Commit(1))
	mustSnap(t, m, Snapshot{UsedSlots: 2, UsedPages: 2, CacheI: []int{1}, History: []int{1}, IssuedTrx: 1})
}

// 无记录事务也消耗 trx_no。
func TestEmptyTxConsumesTrxNo(t *testing.T) {
	m := mustManager(t, 4, 4, 100)
	mustOK(t, m.Begin(1))
	mustOK(t, m.Commit(1))
	mustOK(t, m.Begin(2))
	mustOK(t, m.Modify(2))
	mustOK(t, m.Commit(2)) // trx_no=2
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 1, History: []int{2}, IssuedTrx: 2})
}

// Rollback 不消耗 trx_no，且段立即 Release。
func TestRollbackDoesNotConsumeTrxNo(t *testing.T) {
	m := mustManager(t, 4, 4, 100)
	mustOK(t, m.Begin(1))
	mustOK(t, m.Insert(1))
	mustOK(t, m.Modify(1))
	mustOK(t, m.Rollback(1)) // I 进缓存；U(n=1) 也进缓存；不发 trx_no
	mustSnap(t, m, Snapshot{UsedSlots: 2, UsedPages: 2, CacheI: []int{1}, CacheU: []int{1}})
	mustOK(t, m.Begin(2))
	mustOK(t, m.Commit(2))
	mustSnap(t, m, Snapshot{UsedSlots: 2, UsedPages: 2, CacheI: []int{1}, CacheU: []int{1}, IssuedTrx: 1})
}

// 视图 limit 恰等于段 trx_no 时不可回收（严格小于）。
func TestViewLimitEqualBlocksPurge(t *testing.T) {
	m := mustManager(t, 4, 4, 100)
	mustOK(t, m.Begin(1))
	mustOK(t, m.Commit(1)) // trx_no=1，无记录
	v := m.OpenView()      // limit = 已发 1 + 1 = 2
	mustOK(t, m.Begin(2))
	mustOK(t, m.Modify(2))
	mustOK(t, m.Commit(2))   // trx_no=2 入历史链
	mustPurge(t, m, 10, nil) // PL=2，头部 trx_no=2 不满足严格小于，即停
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 1, History: []int{2}, IssuedTrx: 2, OpenViews: 1})
	mustOK(t, m.CloseView(v))
	mustPurge(t, m, 10, []int{2}) // 无视图后 PL=3，可回收
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 1, CacheU: []int{1}, IssuedTrx: 2})
}

// 多个视图取最小 limit。
func TestMultipleViewsMinLimit(t *testing.T) {
	m := mustManager(t, 8, 4, 100)
	for id := 1; id <= 3; id++ { // trx_no 1,2,3 均入历史链
		mustOK(t, m.Begin(id))
		mustOK(t, m.Modify(id))
		mustOK(t, m.Commit(id))
	}
	v1 := m.OpenView() // limit=4
	mustOK(t, m.Begin(4))
	mustOK(t, m.Modify(4))
	mustOK(t, m.Commit(4)) // trx_no=4
	v2 := m.OpenView()     // limit=5
	if v1 == v2 {
		t.Fatalf("view ids must differ")
	}
	mustOK(t, m.CloseView(v1)) // 剩余视图 limit=5 -> PL=5
	mustPurge(t, m, 10, []int{1, 2, 3, 4})
	mustSnap(t, m, Snapshot{UsedSlots: 4, UsedPages: 4, CacheU: []int{1, 1, 1, 1}, IssuedTrx: 4, OpenViews: 1})

	// 再构造：两个视图同时打开，最小 limit 生效。
	m2 := mustManager(t, 8, 4, 100)
	mustOK(t, m2.Begin(1))
	mustOK(t, m2.Modify(1))
	mustOK(t, m2.Commit(1)) // trx 1
	va := m2.OpenView()     // limit=2
	mustOK(t, m2.Begin(2))
	mustOK(t, m2.Modify(2))
	mustOK(t, m2.Commit(2)) // trx 2
	vb := m2.OpenView()     // limit=3
	mustOK(t, m2.Begin(3))
	mustOK(t, m2.Modify(3))
	mustOK(t, m2.Commit(3)) // trx 3
	mustOK(t, m2.CloseView(vb))
	mustPurge(t, m2, 10, []int{1}) // PL=min(2)=2，仅回收 trx 1
	mustOK(t, m2.CloseView(va))
	mustPurge(t, m2, 10, []int{2, 3}) // 无视图，PL=4
}

// Purge 受 n 限制并在不满足处即停。
func TestPurgeLimitAndStop(t *testing.T) {
	m := mustManager(t, 8, 4, 100)
	for id := 1; id <= 4; id++ {
		mustOK(t, m.Begin(id))
		mustOK(t, m.Modify(id))
		mustOK(t, m.Commit(id))
	}
	mustPurge(t, m, 2, []int{1, 2}) // 至多 2 个
	mustSnap(t, m, Snapshot{UsedSlots: 4, UsedPages: 4, CacheU: []int{1, 1}, History: []int{3, 4}, IssuedTrx: 4})
	mustPurge(t, m, 1, []int{3})
	mustPurge(t, m, 5, []int{4})
	mustPurge(t, m, 5, nil) // 历史链已空

	// 头部不满足即停：无视图时 PL=已发+1，构造头部 trx 等于 PL 不可能，
	// 用视图使 PL 卡住头部。
	v := m.OpenView() // limit=5
	mustOK(t, m.Begin(5))
	mustOK(t, m.Modify(5))
	mustOK(t, m.Commit(5))   // trx 5
	mustPurge(t, m, 10, nil) // PL=5，头部 trx=5 不小于 5，即停
	mustOK(t, m.CloseView(v))
	mustPurge(t, m, 10, []int{5})
}

// 无槽位与页预算不足的先后，及被拒不残留段。
func TestSlotBeforePageAndNoResidue(t *testing.T) {
	// 槽位先检查：槽位满且页也满时，报无空闲槽位。
	m := mustManager(t, 1, 4, 1)
	mustOK(t, m.Begin(1))
	mustOK(t, m.Insert(1)) // 占满唯一槽位与唯一页
	mustOK(t, m.Commit(1)) // I 段进缓存，仍占槽位与页
	mustOK(t, m.Begin(2))
	mustCode(t, m.Modify(2), CodeNoFreeSlot) // 槽位、页都满，先报槽位
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 1, CacheI: []int{1}, IssuedTrx: 1})

	// 槽位有空但页满：报页预算不足。
	m2 := mustManager(t, 2, 4, 1)
	mustOK(t, m2.Begin(1))
	mustOK(t, m2.Insert(1))
	mustOK(t, m2.Commit(1)) // I 缓存占 1 槽 1 页
	mustOK(t, m2.Begin(2))
	mustCode(t, m2.Modify(2), CodePageBudget) // 槽位 1/2 可用，页 1/1 满
	// 被拒后不残留段：tx2 没有任何段，Rollback 后状态不变。
	mustOK(t, m2.Rollback(2))
	mustSnap(t, m2, Snapshot{UsedSlots: 1, UsedPages: 1, CacheI: []int{1}, IssuedTrx: 1})

	// 追加记录跨页时页预算不足：段保留但记录数不变。
	m3 := mustManager(t, 2, 2, 1)
	mustOK(t, m3.Begin(1))
	mustOK(t, m3.Insert(1))
	mustOK(t, m3.Insert(1))                   // n=2，1 页
	mustCode(t, m3.Insert(1), CodePageBudget) // 第 3 条需第 2 页，预算不足
	mustSnap(t, m3, Snapshot{UsedSlots: 1, UsedPages: 1})
	mustOK(t, m3.Rollback(1)) // n=2 的 I 段进缓存（4*2=8<=6? 否：3K=6，8>6，整体释放）
	mustSnap(t, m3, Snapshot{})
}

// 追加记录跨页边界时占页变化。
func TestPageBoundaryCrossing(t *testing.T) {
	m := mustManager(t, 4, 2, 10)
	mustOK(t, m.Begin(1))
	mustOK(t, m.Insert(1))
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 1})
	mustOK(t, m.Insert(1)) // n=2，恰满 1 页
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 1})
	mustOK(t, m.Insert(1)) // n=3，跨页 -> 2 页
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 2})
	mustOK(t, m.Insert(1))
	mustOK(t, m.Insert(1)) // n=5 -> 3 页
	mustSnap(t, m, Snapshot{UsedSlots: 1, UsedPages: 3})
	mustOK(t, m.Rollback(1)) // 多页段整体释放
	mustSnap(t, m, Snapshot{})
}

// 参数非法与各类错误码。
func TestErrorCodes(t *testing.T) {
	if _, err := NewManager(0, 1, 1); err == nil {
		t.Fatal("slots=0 should fail")
	}
	if _, err := NewManager(1001, 1, 1); err == nil {
		t.Fatal("slots=1001 should fail")
	}
	if _, err := NewManager(1, 0, 1); err == nil {
		t.Fatal("perPage=0 should fail")
	}
	if _, err := NewManager(1, 1, 0); err == nil {
		t.Fatal("pageBudget=0 should fail")
	}
	if _, err := NewManager(1, 1, 1_000_001); err == nil {
		t.Fatal("pageBudget=1e6+1 should fail")
	}

	m := mustManager(t, 2, 4, 10)
	mustCode(t, m.Begin(0), CodeInvalidParam)
	mustCode(t, m.Begin(1_000_001), CodeInvalidParam)
	mustOK(t, m.Begin(1))
	mustCode(t, m.Begin(1), CodeTxExists)
	mustCode(t, m.Insert(2), CodeTxNotFound)
	mustCode(t, m.Modify(2), CodeTxNotFound)
	mustCode(t, m.Commit(2), CodeTxNotFound)
	mustCode(t, m.Rollback(2), CodeTxNotFound)
	mustOK(t, m.Commit(1))
	mustCode(t, m.Begin(1), CodeTxExists) // 已终止也算已存在
	mustCode(t, m.Insert(1), CodeTxTerminated)
	mustCode(t, m.Modify(1), CodeTxTerminated)
	mustCode(t, m.Commit(1), CodeTxTerminated)
	mustCode(t, m.Rollback(1), CodeTxTerminated)
	mustCode(t, m.CloseView(99), CodeViewNotFound)
	v := m.OpenView()
	mustOK(t, m.CloseView(v))
	mustCode(t, m.CloseView(v), CodeViewNotFound) // 已关闭
	if _, err := m.Purge(0); err == nil {
		t.Fatal("Purge(0) should fail")
	}
	mustCode(t, func() error { _, err := m.Purge(-3); return err }(), CodeInvalidParam)
	// 被拒操作不改变状态。
	mustSnap(t, m, Snapshot{IssuedTrx: 1})
}

// 并发调用等价于某个串行顺序：不变量始终成立。
func TestConcurrentInvariants(t *testing.T) {
	m := mustManager(t, 16, 4, 64)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := base*1000 + i + 1
				if m.Begin(id) != nil {
					continue
				}
				m.Insert(id)
				m.Modify(id)
				m.Modify(id)
				if i%3 == 0 {
					m.Rollback(id)
				} else {
					m.Commit(id)
				}
				if i%5 == 0 {
					v := m.OpenView()
					m.Purge(2)
					m.CloseView(v)
				}
			}
		}(g)
	}
	wg.Wait()
	snap := m.Snapshot()
	if snap.UsedSlots < 0 || snap.UsedSlots > 16 {
		t.Fatalf("used slots %d out of range", snap.UsedSlots)
	}
	if snap.UsedPages < 0 || snap.UsedPages > 64 {
		t.Fatalf("used pages %d out of range", snap.UsedPages)
	}
	for i := 1; i < len(snap.History); i++ {
		if snap.History[i] <= snap.History[i-1] {
			t.Fatalf("history not strictly increasing: %v", snap.History)
		}
	}
	// 槽位守恒：活跃段 + 缓存段 + 历史段 = 已用槽位。
	active := 0
	m.mu.Lock()
	for _, tx := range m.txs {
		if tx.insert != nil {
			active++
		}
		if tx.update != nil {
			active++
		}
	}
	m.mu.Unlock()
	total := active + len(snap.CacheI) + len(snap.CacheU) + len(snap.History)
	if total != snap.UsedSlots {
		t.Fatalf("slot conservation violated: %d != %d", total, snap.UsedSlots)
	}
}
