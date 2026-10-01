package nkl

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// mustErr 断言 err 为指定类别的 *Error，holder 非 0 时同时断言持锁事务。
func mustErr(t *testing.T, err error, kind Kind, holder TxID) {
	t.Helper()
	var nerr *Error
	if !errors.As(err, &nerr) {
		t.Fatalf("期望 *Error，实际 err=%v", err)
	}
	if nerr.Kind != kind {
		t.Fatalf("期望错误类别 %d，实际 %d（%v）", kind, nerr.Kind, nerr)
	}
	if holder != 0 && nerr.Holder != holder {
		t.Fatalf("期望持锁事务 %d，实际 %d（%v）", holder, nerr.Holder, nerr)
	}
	t.Logf("输出: %v", nerr)
}

// mustOK 断言操作成功。
func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际 err=%v", err)
	}
	t.Logf("输出: 成功")
}

// mustScan 断言扫描成功且结果与 want 一致。
func mustScan(t *testing.T, s *Set, tx TxID, lo, hi int64, mode Mode, want []int64) {
	t.Helper()
	t.Logf("输入: tx%d.Scan([%d,%d], %s)", tx, lo, hi, mode)
	got, err := s.Scan(tx, lo, hi, mode)
	if err != nil {
		t.Fatalf("期望扫描成功，实际 err=%v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("期望扫描结果 %v，实际 %v", want, got)
	}
	t.Logf("输出: 键集合 %v", got)
}

// TestScanGapBlocksInsertBetweenPAndLo 扫描后在 p 与 lo 之间的插入被拒，
// 而在 p 之下的插入成功。
func TestScanGapBlocksInsertBetweenPAndLo(t *testing.T) {
	s := NewSet(10, 20, 30)
	t1 := s.Begin()
	mustScan(t, s, t1, 20, 30, Shared, []int64{20, 30})
	t.Log("判定依据: 小于 20 的最大现存键 p=10，大于 30 的最小现存键不存在（正无穷），" +
		"故 tx1 持有空隙锁 (10, +∞) 与 20、30 的共享记录锁")

	t2 := s.Begin()
	t.Logf("输入: tx%d.Insert(15)，15 落在 (10, +∞) 内", t2)
	mustErr(t, s.Insert(t2, 15), ErrGapOccupied, t1)
	t.Logf("判定依据: 15 落在 tx%d 持有的空隙锁 (10, +∞) 内，间隙被占", t1)

	t3 := s.Begin()
	t.Logf("输入: tx%d.Insert(25)，25 落在 (10, +∞) 内", t3)
	mustErr(t, s.Insert(t3, 25), ErrGapOccupied, t1)
	t.Logf("判定依据: 25 同样落在 tx%d 的空隙锁区间内", t1)

	t4 := s.Begin()
	t.Logf("输入: tx%d.Insert(5)，5 在 p=10 之下", t4)
	mustOK(t, s.Insert(t4, 5))
	t.Log("判定依据: 5 不在任何他事务的空隙锁区间内，插入成功并持 5 的排他记录锁")
}

// TestGetExistingDoesNotBlockNeighborInsert 存在键的点读只加记录锁，
// 不拦截邻近插入。
func TestGetExistingDoesNotBlockNeighborInsert(t *testing.T) {
	s := NewSet(10, 20, 30)
	t1 := s.Begin()
	t.Logf("输入: tx%d.Get(20, 共享)，20 现存", t1)
	found, err := s.Get(t1, 20, Shared)
	mustOK(t, err)
	if !found {
		t.Fatal("期望命中现存键 20")
	}
	t.Log("输出: 命中=true")
	t.Log("判定依据: 键存在只加记录锁，不加空隙锁")

	t2 := s.Begin()
	t.Logf("输入: tx%d.Insert(15)，紧邻 20 之下", t2)
	mustOK(t, s.Insert(t2, 15))
	t.Log("判定依据: tx1 未持任何空隙锁，邻近插入不被拦截")

	t3 := s.Begin()
	t.Logf("输入: tx%d.Insert(25)，紧邻 20 之上", t3)
	mustOK(t, s.Insert(t3, 25))
	t.Log("判定依据: 同上，插入成功")

	t4 := s.Begin()
	t.Logf("输入: tx%d.Get(20, 排他)，tx%d 持 20 的共享锁", t4, t1)
	_, err = s.Get(t4, 20, Exclusive)
	mustErr(t, err, ErrLockConflict, t1)
	t.Log("判定依据: 共享与排他不兼容，记录锁冲突")
}

// TestGetMissingBlocksItsGap 不存在键的点读只加其所在空隙的锁，
// 拦截落在该间隙内的插入。
func TestGetMissingBlocksItsGap(t *testing.T) {
	s := NewSet(10, 20, 30)
	t1 := s.Begin()
	t.Logf("输入: tx%d.Get(15, 共享)，15 不存在", t1)
	found, err := s.Get(t1, 15, Shared)
	mustOK(t, err)
	if found {
		t.Fatal("期望未命中不存在的键 15")
	}
	t.Log("输出: 命中=false")
	t.Log("判定依据: 15 的现存前驱 p=10、后继 s=20，tx1 持有空隙锁 (10, 20)")

	t2 := s.Begin()
	t.Logf("输入: tx%d.Insert(15)", t2)
	mustErr(t, s.Insert(t2, 15), ErrGapOccupied, t1)
	t.Log("判定依据: 15 落在 (10, 20) 内，间隙被占")

	t3 := s.Begin()
	t.Logf("输入: tx%d.Insert(12)", t3)
	mustErr(t, s.Insert(t3, 12), ErrGapOccupied, t1)
	t.Log("判定依据: 12 同样落在 (10, 20) 内")

	t4 := s.Begin()
	t.Logf("输入: tx%d.Insert(25)，落在未加锁的间隙 (20, 30)", t4)
	mustOK(t, s.Insert(t4, 25))
	t.Log("判定依据: (20, 30) 无他事务空隙锁，插入成功")
}

// TestTwoScansSameGapMutualInsertRejected 两事务先后扫描同一空隙，
// 空隙锁互不冲突，但随后互相插入均被拒。
func TestTwoScansSameGapMutualInsertRejected(t *testing.T) {
	s := NewSet(10, 20)
	t1 := s.Begin()
	mustScan(t, s, t1, 11, 19, Shared, []int64{})
	t.Log("判定依据: [11,19] 内无现存键，tx1 持空隙锁 (10, 20)")

	t2 := s.Begin()
	mustScan(t, s, t2, 11, 19, Shared, []int64{})
	t.Log("判定依据: 空隙锁彼此永不冲突，tx2 同样获得 (10, 20)")

	t.Logf("输入: tx%d.Insert(15)", t1)
	mustErr(t, s.Insert(t1, 15), ErrGapOccupied, t2)
	t.Logf("判定依据: 15 落在 tx%d 的空隙锁 (10, 20) 内", t2)

	t.Logf("输入: tx%d.Insert(15)", t2)
	mustErr(t, s.Insert(t2, 15), ErrGapOccupied, t1)
	t.Logf("判定依据: 15 落在 tx%d 的空隙锁 (10, 20) 内", t1)
}

// TestScanConflictLeavesNoLocks 扫描中途遇记录锁冲突则整体失败，
// 不留下任何记录锁或空隙锁。
func TestScanConflictLeavesNoLocks(t *testing.T) {
	s := NewSet(10, 20, 30)
	t1 := s.Begin()
	t.Logf("输入: tx%d.Get(20, 排他)", t1)
	_, err := s.Get(t1, 20, Exclusive)
	mustOK(t, err)
	t.Log("判定依据: tx1 持 20 的排他记录锁")

	t2 := s.Begin()
	t.Logf("输入: tx%d.Scan([10,30], 共享)，范围内 20 被 tx%d 排他锁定", t2, t1)
	_, err = s.Scan(t2, 10, 30, Shared)
	mustErr(t, err, ErrLockConflict, t1)
	t.Log("判定依据: 扫描整体失败，10、30 的记录锁与空隙锁都不得留下")

	t3 := s.Begin()
	t.Logf("输入: tx%d.Get(10, 排他)，验证 tx%d 未残留 10 的共享锁", t3, t2)
	_, err = s.Get(t3, 10, Exclusive)
	mustOK(t, err)
	t.Log("判定依据: 若失败扫描留下共享锁，排他加锁必然冲突")

	t4 := s.Begin()
	t.Logf("输入: tx%d.Get(30, 排他)，验证 tx%d 未残留 30 的共享锁", t4, t2)
	_, err = s.Get(t4, 30, Exclusive)
	mustOK(t, err)

	t5 := s.Begin()
	t.Logf("输入: tx%d.Insert(15)，验证 tx%d 未残留空隙锁", t5, t2)
	mustOK(t, s.Insert(t5, 15))
	t.Log("判定依据: 失败扫描若留下空隙锁，插入必被拦截")
}

// TestAbortUndoesInserts 中止释放全部锁并撤销该事务的全部插入。
func TestAbortUndoesInserts(t *testing.T) {
	s := NewSet(10, 30)
	t1 := s.Begin()
	t.Logf("输入: tx%d.Insert(20)", t1)
	mustOK(t, s.Insert(t1, 20))
	t.Logf("输入: tx%d.Insert(15)", t1)
	mustOK(t, s.Insert(t1, 15))
	t.Logf("输入: tx%d.Abort()", t1)
	mustOK(t, s.Abort(t1))
	t.Log("判定依据: 中止撤销 15、20 两个插入并释放全部锁")

	t2 := s.Begin()
	mustScan(t, s, t2, 10, 30, Shared, []int64{10, 30})
	t.Log("判定依据: 扫描结果不含已撤销的 15、20")
	mustOK(t, s.Commit(t2))

	t3 := s.Begin()
	t.Logf("输入: tx%d.Get(20, 共享)，20 已被撤销", t3)
	found, err := s.Get(t3, 20, Shared)
	mustOK(t, err)
	if found {
		t.Fatal("期望 20 已不存在")
	}
	t.Log("输出: 命中=false")
	mustOK(t, s.Commit(t3))

	t4 := s.Begin()
	t.Logf("输入: tx%d.Insert(20)，键已撤销且 tx%d 的锁已释放", t4, t1)
	mustOK(t, s.Insert(t4, 20))
	t.Log("判定依据: 中止后键位与锁全部还原，可重新插入")
}

// TestRepeatableScan 同一事务提交前对同一范围重复扫描结果集相同
// （自己的插入除外）。
func TestRepeatableScan(t *testing.T) {
	s := NewSet(10, 20, 30)
	t1 := s.Begin()
	mustScan(t, s, t1, 15, 25, Shared, []int64{20})
	t.Log("判定依据: tx1 持空隙锁 (10, 30)，覆盖整个 [15,25]")

	t2 := s.Begin()
	t.Logf("输入: tx%d.Insert(18)，试图制造幻读", t2)
	mustErr(t, s.Insert(t2, 18), ErrGapOccupied, t1)
	t.Log("判定依据: 18 落在 tx1 的空隙锁 (10, 30) 内，幻读被阻止")

	mustScan(t, s, t1, 15, 25, Shared, []int64{20})
	t.Log("判定依据: 重复扫描结果集与首次相同")

	t.Logf("输入: tx%d.Insert(18)，自己的空隙锁不拦截自己", t1)
	mustOK(t, s.Insert(t1, 18))
	mustScan(t, s, t1, 15, 25, Shared, []int64{18, 20})
	t.Log("判定依据: 自己的插入可见，属规则允许的例外")
}

// TestValidationOrder 事务不存在、已提交、已中止、模式无效、范围颠倒
// 按此顺序只报第一个并整体拒绝。
func TestValidationOrder(t *testing.T) {
	s := NewSet(10, 20)

	t.Log("输入: tx999.Scan([30,10], 无效模式)，事务不存在")
	_, err := s.Scan(999, 30, 10, Mode(7))
	mustErr(t, err, ErrTxNotFound, 0)
	t.Log("判定依据: 事务不存在优先于模式与范围校验")

	t1 := s.Begin()
	mustOK(t, s.Commit(t1))
	t.Logf("输入: tx%d.Scan([30,10], 无效模式)，事务已提交", t1)
	_, err = s.Scan(t1, 30, 10, Mode(7))
	mustErr(t, err, ErrTxCommitted, 0)
	t.Log("判定依据: 已提交优先于模式与范围校验")

	t2 := s.Begin()
	mustOK(t, s.Abort(t2))
	t.Logf("输入: tx%d.Scan([30,10], 无效模式)，事务已中止", t2)
	_, err = s.Scan(t2, 30, 10, Mode(7))
	mustErr(t, err, ErrTxAborted, 0)
	t.Log("判定依据: 已中止优先于模式与范围校验")

	t3 := s.Begin()
	t.Logf("输入: tx%d.Scan([30,10], 无效模式)", t3)
	_, err = s.Scan(t3, 30, 10, Mode(7))
	mustErr(t, err, ErrInvalidMode, 0)
	t.Log("判定依据: 模式无效优先于范围颠倒")

	t.Logf("输入: tx%d.Scan([30,10], 共享)", t3)
	_, err = s.Scan(t3, 30, 10, Shared)
	mustErr(t, err, ErrRangeReversed, 0)
	t.Log("判定依据: lo 大于 hi 报范围颠倒")

	t.Logf("输入: tx%d.Get(10, 无效模式)", t3)
	_, err = s.Get(t3, 10, Mode(9))
	mustErr(t, err, ErrInvalidMode, 0)

	t.Log("输入: tx999.Insert(15)，事务不存在")
	mustErr(t, s.Insert(999, 15), ErrTxNotFound, 0)
	t.Logf("输入: tx%d.Commit()，事务已提交", t1)
	mustErr(t, s.Commit(t1), ErrTxCommitted, 0)
	t.Logf("输入: tx%d.Abort()，事务已提交", t1)
	mustErr(t, s.Abort(t1), ErrTxCommitted, 0)
	t.Logf("输入: tx%d.Commit()，事务已中止", t2)
	mustErr(t, s.Commit(t2), ErrTxAborted, 0)
}

// TestLockCompatibilityAndUpgrade 记录锁相容矩阵与共享升级排他。
func TestLockCompatibilityAndUpgrade(t *testing.T) {
	s := NewSet(10)
	t1, t2, t3 := s.Begin(), s.Begin(), s.Begin()

	t.Logf("输入: tx%d.Get(10, 共享)", t1)
	_, err := s.Get(t1, 10, Shared)
	mustOK(t, err)
	t.Logf("输入: tx%d.Get(10, 共享)", t2)
	_, err = s.Get(t2, 10, Shared)
	mustOK(t, err)
	t.Log("判定依据: 共享与共享兼容")

	t.Logf("输入: tx%d.Get(10, 排他)", t3)
	_, err = s.Get(t3, 10, Exclusive)
	mustErr(t, err, ErrLockConflict, t1)
	t.Log("判定依据: 共享与排他不兼容，报最小持锁事务号保证结果确定")

	t.Logf("输入: tx%d.Get(10, 排他)，共享升级排他", t1)
	_, err = s.Get(t1, 10, Exclusive)
	mustErr(t, err, ErrLockConflict, t2)
	t.Log("判定依据: 升级要求别的事务不持该键的锁，tx2 仍持共享锁")

	mustOK(t, s.Commit(t2))
	t.Logf("输入: tx%d.Get(10, 排他)，tx%d 已释放", t1, t2)
	_, err = s.Get(t1, 10, Exclusive)
	mustOK(t, err)
	t.Log("判定依据: 他人锁已释放，升级成功；提交释放全部锁")
}

// TestReplayDeterminism 相同操作序列重放结果完全相同。
func TestReplayDeterminism(t *testing.T) {
	script := func() []string {
		s := NewSet(10, 20, 30)
		var log []string
		record := func(format string, args ...any) {
			log = append(log, fmt.Sprintf(format, args...))
		}
		t1, t2, t3 := s.Begin(), s.Begin(), s.Begin()
		keys, err := s.Scan(t1, 15, 25, Shared)
		record("tx%d.Scan([15,25],S) -> %v, %v", t1, keys, err)
		record("tx%d.Insert(18) -> %v", t2, s.Insert(t2, 18))
		record("tx%d.Insert(5) -> %v", t2, s.Insert(t2, 5))
		found, err := s.Get(t3, 20, Exclusive)
		record("tx%d.Get(20,X) -> %v, %v", t3, found, err)
		keys, err = s.Scan(t3, 0, 100, Exclusive)
		record("tx%d.Scan([0,100],X) -> %v, %v", t3, keys, err)
		record("tx%d.Insert(10) -> %v", t2, s.Insert(t2, 10))
		record("tx%d.Abort() -> %v", t2, s.Abort(t2))
		keys, err = s.Scan(t1, 15, 25, Shared)
		record("tx%d.Scan([15,25],S) -> %v, %v", t1, keys, err)
		record("tx%d.Commit() -> %v", t1, s.Commit(t1))
		keys, err = s.Scan(t3, 0, 100, Exclusive)
		record("tx%d.Scan([0,100],X) -> %v, %v", t3, keys, err)
		record("tx%d.Commit() -> %v", t3, s.Commit(t3))
		return log
	}
	first := script()
	second := script()
	t.Logf("输入: 固定操作序列重放两次\n第一次输出:\n  %s\n第二次输出:\n  %s",
		strings.Join(first, "\n  "), strings.Join(second, "\n  "))
	if !reflect.DeepEqual(first, second) {
		t.Fatal("两次重放结果不一致")
	}
	t.Log("判定依据: 冲突时取最小持锁事务号、单互斥锁串行化，重放结果完全相同")
}

// TestConcurrentAccess 扫描、点读、插入、提交、中止并发调用，
// 在 -race 下验证无数据竞争，并校验任意时刻同键记录锁持有者相容。
func TestConcurrentAccess(t *testing.T) {
	s := NewSet(0, 10, 20, 30, 40, 50)
	const workers = 8
	const rounds = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				tx := s.Begin()
				lo := int64((seed + r) % 5 * 10)
				mode := Shared
				if (seed+r)%2 == 0 {
					mode = Exclusive
				}
				_, _ = s.Scan(tx, lo, lo+20, mode)
				_, _ = s.Get(tx, lo+5, mode)
				_ = s.Insert(tx, int64(1000+seed*rounds+r))
				if (seed+r)%3 == 0 {
					_ = s.Abort(tx)
				} else {
					_ = s.Commit(tx)
				}
			}
		}(w)
	}
	wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	for key, holders := range s.rec {
		exclusive := 0
		for _, m := range holders {
			if m == Exclusive {
				exclusive++
			}
		}
		if exclusive > 1 || (exclusive == 1 && len(holders) > 1) {
			t.Fatalf("键 %d 的记录锁持有者不相容: %v", key, holders)
		}
	}
	t.Logf("输出: %d 个协程各执行 %d 轮混合操作后，全部记录锁持有者相容", workers, rounds)
	t.Log("判定依据: 排他锁至多一人持有且不与共享并存，-race 无数据竞争")
}
