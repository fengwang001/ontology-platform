package keyset

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// mustOK 断言操作成功，并打印输入与输出。
func mustOK(t *testing.T, op string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s => 期望成功，实际错误: %v", op, err)
	}
	t.Logf("%s => 成功", op)
}

// mustErr 断言操作以指定类别被拒绝，并打印判定依据。
func mustErr(t *testing.T, op string, err error, kind ErrKind) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("%s => 期望错误类别 %v，实际成功", op, kind)
	}
	var ke *Error
	if !errors.As(err, &ke) {
		t.Fatalf("%s => 期望 *Error，实际 %T: %v", op, err, err)
	}
	if ke.Kind != kind {
		t.Fatalf("%s => 期望错误类别 %v，实际 %v（%v）", op, kind, ke.Kind, ke)
	}
	t.Logf("%s => 拒绝，判定依据: %v", op, ke)
	return ke
}

// mustScan 执行扫描并打印输入、输出与判定依据。
func mustScan(t *testing.T, s *Set, tx, lo, hi int64, mode Mode, want []int64) {
	t.Helper()
	op := fmt.Sprintf("Scan(tx=%d, [%d,%d], %s)", tx, lo, hi, mode)
	got, err := s.Scan(tx, lo, hi, mode)
	mustOK(t, op, err)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s => 期望结果集 %v，实际 %v", op, want, got)
	}
	t.Logf("%s => 输出结果集 %v（符合预期 %v）", op, got, want)
}

// TestScanGapBoundaries 扫描后在 (p, lo) 内插入被拒，在 p 之下插入成功；
// 空隙端点在加锁时固定，不随后续插入移动。
func TestScanGapBoundaries(t *testing.T) {
	s, err := NewSet(10, 20, 30, 40)
	mustOK(t, "NewSet(10,20,30,40)", err)
	tx1, tx2 := s.Begin(), s.Begin()
	t.Logf("输入: 初始键=[10 20 30 40], tx1=%d, tx2=%d", tx1, tx2)

	mustScan(t, s, tx1, 20, 30, Shared, []int64{20, 30})
	t.Logf("判定依据: tx1 扫描 [20,30]，记录锁 {20,30}，空隙锁 (p,s)=(10,40)")

	ke := mustErr(t, "Insert(tx2, 15)（落在 (10,40) 内）", s.Insert(tx2, 15), ErrGapOccupied)
	if !reflect.DeepEqual(ke.Holders, []int64{tx1}) {
		t.Fatalf("期望持锁事务 [%d]，实际 %v", tx1, ke.Holders)
	}
	ke = mustErr(t, "Insert(tx2, 35)（落在 (hi,s)=(30,40) 内）", s.Insert(tx2, 35), ErrGapOccupied)
	if !reflect.DeepEqual(ke.Holders, []int64{tx1}) {
		t.Fatalf("期望持锁事务 [%d]，实际 %v", tx1, ke.Holders)
	}
	mustOK(t, "Insert(tx2, 5)（在 p=10 之下，不被拦截）", s.Insert(tx2, 5))
	mustOK(t, "Insert(tx2, 45)（在 s=40 之上，不被拦截）", s.Insert(tx2, 45))
	mustOK(t, "Commit(tx2)", s.Commit(tx2))

	// 端点固定：键 5 已存在，但 tx1 的空隙下端点仍是 10。
	tx3 := s.Begin()
	mustOK(t, "Insert(tx3, 8)（端点固定为 10，不随新键 5 收缩）", s.Insert(tx3, 8))
	mustOK(t, "Commit(tx3)", s.Commit(tx3))

	mustOK(t, "Commit(tx1)（释放全部锁）", s.Commit(tx1))
	tx4 := s.Begin()
	mustOK(t, "Insert(tx4, 15)（tx1 已提交，空隙锁已释放）", s.Insert(tx4, 15))
	mustOK(t, "Commit(tx4)", s.Commit(tx4))
}

// TestScanGapInfinity 边界不存在时空隙端点取正负无穷。
func TestScanGapInfinity(t *testing.T) {
	s, err := NewSet(10, 20)
	mustOK(t, "NewSet(10,20)", err)
	tx1, tx2 := s.Begin(), s.Begin()
	t.Logf("输入: 初始键=[10 20], tx1=%d, tx2=%d", tx1, tx2)

	mustScan(t, s, tx1, 5, 15, Shared, []int64{10})
	t.Logf("判定依据: p 不存在取负无穷，s=20，空隙锁 (-inf,20)")

	mustErr(t, "Insert(tx2, 3)（落在 (-inf,20) 内）", s.Insert(tx2, 3), ErrGapOccupied)
	mustOK(t, "Insert(tx2, 25)（在 s=20 之上）", s.Insert(tx2, 25))
}

// TestGetExistingKeyDoesNotBlockNeighbors 存在键的点读只加记录锁，
// 不拦截邻近插入。
func TestGetExistingKeyDoesNotBlockNeighbors(t *testing.T) {
	s, err := NewSet(10, 20, 30)
	mustOK(t, "NewSet(10,20,30)", err)
	tx1, tx2 := s.Begin(), s.Begin()
	t.Logf("输入: 初始键=[10 20 30], tx1=%d, tx2=%d", tx1, tx2)

	found, err := s.Get(tx1, 20, Exclusive)
	mustOK(t, "Get(tx1, 20, 排他)", err)
	if !found {
		t.Fatal("Get(tx1, 20) 期望命中，实际未命中")
	}
	t.Logf("Get(tx1, 20, 排他) => 输出 found=true，判定依据: 只对键 20 加排他记录锁，无空隙锁")

	mustOK(t, "Insert(tx2, 15)（邻近插入不被拦截）", s.Insert(tx2, 15))
	mustOK(t, "Insert(tx2, 25)（邻近插入不被拦截）", s.Insert(tx2, 25))
	ke := mustErr(t, "Get(tx2, 20, 共享)（与 tx1 的排他锁冲突）", func() error {
		_, err := s.Get(tx2, 20, Shared)
		return err
	}(), ErrRecordConflict)
	if !reflect.DeepEqual(ke.Holders, []int64{tx1}) {
		t.Fatalf("期望持锁事务 [%d]，实际 %v", tx1, ke.Holders)
	}
}

// TestGetMissingKeyBlocksGap 不存在键的点读只加 (p, s) 空隙锁并拦截其间插入。
func TestGetMissingKeyBlocksGap(t *testing.T) {
	s, err := NewSet(10, 20)
	mustOK(t, "NewSet(10,20)", err)
	tx1, tx2 := s.Begin(), s.Begin()
	t.Logf("输入: 初始键=[10 20], tx1=%d, tx2=%d", tx1, tx2)

	found, err := s.Get(tx1, 15, Shared)
	mustOK(t, "Get(tx1, 15, 共享)", err)
	if found {
		t.Fatal("Get(tx1, 15) 期望未命中，实际命中")
	}
	t.Logf("Get(tx1, 15, 共享) => 输出 found=false，判定依据: 只加 (p,s)=(10,20) 空隙锁，无记录锁")

	mustErr(t, "Insert(tx2, 12)（落在 (10,20) 内）", s.Insert(tx2, 12), ErrGapOccupied)
	mustErr(t, "Insert(tx2, 17)（落在 (10,20) 内）", s.Insert(tx2, 17), ErrGapOccupied)
	mustOK(t, "Insert(tx2, 5)（在 p=10 之下）", s.Insert(tx2, 5))
	mustOK(t, "Insert(tx2, 25)（在 s=20 之上）", s.Insert(tx2, 25))
}

// TestMutualGapScansBlockEachOther 两事务先后扫描同一空隙（空隙锁互不冲突），
// 之后互相向对方空隙内插入均被拒。
func TestMutualGapScansBlockEachOther(t *testing.T) {
	s, err := NewSet(10, 20, 40)
	mustOK(t, "NewSet(10,20,40)", err)
	tx1, tx2 := s.Begin(), s.Begin()
	t.Logf("输入: 初始键=[10 20 40], tx1=%d, tx2=%d", tx1, tx2)

	mustScan(t, s, tx1, 25, 35, Shared, nil)
	mustScan(t, s, tx2, 25, 35, Shared, nil)
	t.Logf("判定依据: 两次扫描都持空隙锁 (20,40)，空隙锁彼此永不冲突，均成功")

	ke := mustErr(t, "Insert(tx1, 30)（被 tx2 的空隙拦截）", s.Insert(tx1, 30), ErrGapOccupied)
	if !reflect.DeepEqual(ke.Holders, []int64{tx2}) {
		t.Fatalf("期望持锁事务 [%d]，实际 %v", tx2, ke.Holders)
	}
	ke = mustErr(t, "Insert(tx2, 28)（被 tx1 的空隙拦截）", s.Insert(tx2, 28), ErrGapOccupied)
	if !reflect.DeepEqual(ke.Holders, []int64{tx1}) {
		t.Fatalf("期望持锁事务 [%d]，实际 %v", tx1, ke.Holders)
	}
}

// TestScanConflictLeavesNoPartialLocks 扫描中途冲突则整体失败，不留任何锁。
func TestScanConflictLeavesNoPartialLocks(t *testing.T) {
	s, err := NewSet(10, 20, 30)
	mustOK(t, "NewSet(10,20,30)", err)
	tx1, tx2, tx3 := s.Begin(), s.Begin(), s.Begin()
	t.Logf("输入: 初始键=[10 20 30], tx1=%d, tx2=%d, tx3=%d", tx1, tx2, tx3)

	_, err = s.Get(tx1, 20, Exclusive)
	mustOK(t, "Get(tx1, 20, 排他)", err)

	ke := mustErr(t, "Scan(tx2, [10,30], 共享)（键 20 与他人排他锁冲突）", func() error {
		_, err := s.Scan(tx2, 10, 30, Shared)
		return err
	}(), ErrRecordConflict)
	if ke.Key != 20 || !reflect.DeepEqual(ke.Holders, []int64{tx1}) {
		t.Fatalf("期望冲突键 20、持锁事务 [%d]，实际 key=%d holders=%v", tx1, ke.Key, ke.Holders)
	}
	t.Logf("判定依据: 扫描先整体校验再加锁，tx2 在键 10 上不得留下部分锁")

	// 若 tx2 在键 10/30 上留有共享锁，tx3 的排他扫描必然冲突。
	mustScan(t, s, tx3, 10, 10, Exclusive, []int64{10})
	mustScan(t, s, tx3, 30, 30, Exclusive, []int64{30})
	t.Logf("判定依据: tx3 能对 10、30 加排他锁，证明文件 tx2 未留任何锁")
}

// TestAbortUndoesInsert 中止释放全部锁并撤销它的全部插入。
func TestAbortUndoesInsert(t *testing.T) {
	s, err := NewSet(10, 20)
	mustOK(t, "NewSet(10,20)", err)
	tx1, tx2 := s.Begin(), s.Begin()
	t.Logf("输入: 初始键=[10 20], tx1=%d, tx2=%d", tx1, tx2)

	mustOK(t, "Insert(tx1, 15)", s.Insert(tx1, 15))
	mustOK(t, "Abort(tx1)", s.Abort(tx1))
	t.Logf("判定依据: 中止撤销插入，键 15 消失，排他记录锁释放")

	found, err := s.Get(tx2, 15, Shared)
	mustOK(t, "Get(tx2, 15, 共享)", err)
	if found {
		t.Fatal("Get(tx2, 15) 期望未命中（插入已撤销），实际命中")
	}
	t.Logf("Get(tx2, 15) => 输出 found=false（插入已撤销）")
	mustOK(t, "Abort(tx2)", s.Abort(tx2))

	tx3 := s.Begin()
	mustOK(t, "Insert(tx3, 15)（键已撤销且锁已释放，可再插入）", s.Insert(tx3, 15))
	mustOK(t, "Commit(tx3)", s.Commit(tx3))

	tx4 := s.Begin()
	mustScan(t, s, tx4, 0, 100, Shared, []int64{10, 15, 20})
}

// TestValidationOrder 校验顺序：事务不存在 -> 已提交/已中止 -> 非法模式 -> 范围颠倒。
func TestValidationOrder(t *testing.T) {
	s, err := NewSet(10)
	mustOK(t, "NewSet(10)", err)
	t.Log("输入: 初始键=[10]，依次触发各类非法操作")

	mustErr(t, "Scan(999, ...)（事务不存在）", func() error {
		_, err := s.Scan(999, 0, 1, Shared)
		return err
	}(), ErrTxNotFound)
	mustErr(t, "Insert(999, 5)（事务不存在）", s.Insert(999, 5), ErrTxNotFound)
	mustErr(t, "Commit(999)（事务不存在）", s.Commit(999), ErrTxNotFound)

	tx := s.Begin()
	mustOK(t, "Commit(tx)", s.Commit(tx))
	// 已提交 + 非法模式 + 范围颠倒同时成立，只报「已提交」。
	mustErr(t, "Scan(已提交tx, [5,1], 非法模式)（只报已提交）", func() error {
		_, err := s.Scan(tx, 5, 1, Mode(99))
		return err
	}(), ErrTxCommitted)
	mustErr(t, "Abort(已提交tx)", s.Abort(tx), ErrTxCommitted)

	tx2 := s.Begin()
	mustOK(t, "Abort(tx2)", s.Abort(tx2))
	mustErr(t, "Get(已中止tx2, 10, 共享)（只报已中止）", func() error {
		_, err := s.Get(tx2, 10, Shared)
		return err
	}(), ErrTxAborted)
	mustErr(t, "Commit(已中止tx2)", s.Commit(tx2), ErrTxAborted)

	tx3 := s.Begin()
	// 非法模式与范围颠倒同时成立，只报「非法模式」。
	mustErr(t, "Scan(tx3, [5,1], 非法模式)（只报非法模式）", func() error {
		_, err := s.Scan(tx3, 5, 1, Mode(99))
		return err
	}(), ErrInvalidMode)
	mustErr(t, "Get(tx3, 10, 非法模式)", func() error {
		_, err := s.Get(tx3, 10, Mode(-1))
		return err
	}(), ErrInvalidMode)
	mustErr(t, "Scan(tx3, [5,1], 共享)（范围颠倒）", func() error {
		_, err := s.Scan(tx3, 5, 1, Shared)
		return err
	}(), ErrRangeReversed)
	t.Log("判定依据: 校验按 事务不存在 -> 已提交/已中止 -> 非法模式 -> 范围颠倒 顺序短路")

	// 非法操作不得改变状态：tx3 应仍活跃可用。
	mustScan(t, s, tx3, 0, 100, Shared, []int64{10})
}

// TestRecordLockCompatibility 记录锁仅共享与共享兼容；
// 共享升级排他要求别的事务不持该键的锁。
func TestRecordLockCompatibility(t *testing.T) {
	s, err := NewSet(10)
	mustOK(t, "NewSet(10)", err)
	tx1, tx2, tx3 := s.Begin(), s.Begin(), s.Begin()
	t.Logf("输入: 初始键=[10], tx1=%d, tx2=%d, tx3=%d", tx1, tx2, tx3)

	_, err = s.Get(tx1, 10, Shared)
	mustOK(t, "Get(tx1, 10, 共享)", err)
	_, err = s.Get(tx2, 10, Shared)
	mustOK(t, "Get(tx2, 10, 共享)（共享与共享兼容）", err)

	ke := mustErr(t, "Get(tx3, 10, 排他)（与两把共享锁冲突）", func() error {
		_, err := s.Get(tx3, 10, Exclusive)
		return err
	}(), ErrRecordConflict)
	if !reflect.DeepEqual(ke.Holders, []int64{tx1, tx2}) {
		t.Fatalf("期望持锁事务 [%d %d]，实际 %v", tx1, tx2, ke.Holders)
	}

	ke = mustErr(t, "Get(tx1, 10, 排他)（升级要求别的事务不持锁）", func() error {
		_, err := s.Get(tx1, 10, Exclusive)
		return err
	}(), ErrRecordConflict)
	if !reflect.DeepEqual(ke.Holders, []int64{tx2}) {
		t.Fatalf("期望持锁事务 [%d]（不含自己），实际 %v", tx2, ke.Holders)
	}

	mustOK(t, "Commit(tx2)（释放共享锁）", s.Commit(tx2))
	_, err = s.Get(tx1, 10, Exclusive)
	mustOK(t, "Get(tx1, 10, 排他)（仅剩自己，共享升级为排他）", err)
	_, err = s.Get(tx1, 10, Shared)
	mustOK(t, "Get(tx1, 10, 共享)（自己的锁不与自己冲突，保持排他）", err)
	t.Log("判定依据: 同一键上任意时刻的锁持有者彼此相容")
}

// TestRepeatScanNoPhantom 同一事务对同一范围重复扫描结果集相同（自己的插入除外）。
func TestRepeatScanNoPhantom(t *testing.T) {
	s, err := NewSet(10, 20, 30, 40, 50)
	mustOK(t, "NewSet(10,20,30,40,50)", err)
	tx1, tx2 := s.Begin(), s.Begin()
	t.Logf("输入: 初始键=[10 20 30 40 50], tx1=%d, tx2=%d", tx1, tx2)

	mustScan(t, s, tx1, 15, 45, Shared, []int64{20, 30, 40})
	mustErr(t, "Insert(tx2, 25)（他事务插入被空隙锁拦截）", s.Insert(tx2, 25), ErrGapOccupied)
	mustScan(t, s, tx1, 15, 45, Shared, []int64{20, 30, 40})
	t.Log("判定依据: 重复扫描结果集相同，无幻读")

	mustOK(t, "Insert(tx1, 35)（自己的空隙不拦截自己）", s.Insert(tx1, 35))
	mustScan(t, s, tx1, 15, 45, Shared, []int64{20, 30, 35, 40})
	t.Log("判定依据: 自己的插入对自己的后续扫描可见")
}

// TestReplayDeterministic 相同操作序列重放结果完全相同。
func TestReplayDeterministic(t *testing.T) {
	script := func() []string {
		s, err := NewSet(10, 20, 30)
		if err != nil {
			t.Fatalf("NewSet: %v", err)
		}
		var out []string
		record := func(format string, args ...any) {
			out = append(out, fmt.Sprintf(format, args...))
		}
		tx1, tx2 := s.Begin(), s.Begin()
		keys, err := s.Scan(tx1, 5, 25, Shared)
		record("scan1=%v err=%v", keys, err)
		record("insert(tx2,15)=%v", s.Insert(tx2, 15))
		_, err = s.Get(tx2, 20, Exclusive)
		record("get(tx2,20,X)=%v", err)
		record("insert(tx2,35)=%v", s.Insert(tx2, 35))
		record("commit(tx2)=%v", s.Commit(tx2))
		record("abort(tx1)=%v", s.Abort(tx1))
		tx3 := s.Begin()
		keys, err = s.Scan(tx3, 0, 100, Exclusive)
		record("scan3=%v err=%v", keys, err)
		return out
	}
	first, second := script(), script()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放结果不一致:\n第一次: %v\n第二次: %v", first, second)
	}
	t.Logf("两次重放输出完全一致: %v", first)
}

// TestConcurrent 并发调用各操作，配合 -race 验证线程安全与锁相容不变量。
func TestConcurrent(t *testing.T) {
	s, err := NewSet(0, 1000)
	mustOK(t, "NewSet(0,1000)", err)

	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for i := int64(0); i < 50; i++ {
				tx := s.Begin()
				lo := (base*50 + i) % 200
				if _, err := s.Scan(tx, lo, lo+50, Shared); err != nil {
					var ke *Error
					if !errors.As(err, &ke) {
						t.Errorf("非预期错误类型: %v", err)
					}
				}
				if _, err := s.Get(tx, lo+7, Shared); err != nil {
					var ke *Error
					if !errors.As(err, &ke) {
						t.Errorf("非预期错误类型: %v", err)
					}
				}
				// 每个事务插入互不相同的键，避免「键已存在」噪音。
				key := 10000 + base*50 + i
				if err := s.Insert(tx, key); err != nil {
					t.Errorf("插入专属键 %d 不应失败: %v", key, err)
				}
				if i%2 == 0 {
					if err := s.Commit(tx); err != nil {
						t.Errorf("Commit: %v", err)
					}
				} else {
					if err := s.Abort(tx); err != nil {
						t.Errorf("Abort: %v", err)
					}
				}
			}
		}(int64(w))
	}
	wg.Wait()

	// 一致性收尾：提交的插入（偶数 i）应全部可见，中止的（奇数 i）应全部撤销。
	tx := s.Begin()
	got, err := s.Scan(tx, 0, 20000, Shared)
	mustOK(t, "最终全量扫描", err)
	set := make(map[int64]bool, len(got))
	for _, k := range got {
		set[k] = true
	}
	for w := int64(0); w < workers; w++ {
		for i := int64(0); i < 50; i++ {
			key := 10000 + w*50 + i
			want := i%2 == 0
			if set[key] != want {
				t.Fatalf("键 %d 存在性=%v，期望 %v", key, set[key], want)
			}
		}
	}
	t.Logf("并发收尾一致: 提交的插入全部可见，中止的全部撤销，最终键数=%d", len(got))
}
