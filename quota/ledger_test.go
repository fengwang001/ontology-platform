package quota

import (
	"errors"
	"sync"
	"testing"
)

func mustMkdir(t *testing.T, l *Ledger, p int) int {
	t.Helper()
	id, err := l.Mkdir(p)
	if err != nil {
		t.Fatalf("Mkdir(%d): %v", p, err)
	}
	return id
}

func mustAddFile(t *testing.T, l *Ledger, p int, size int64) int {
	t.Helper()
	id, err := l.AddFile(p, size)
	if err != nil {
		t.Fatalf("AddFile(%d, %d): %v", p, size, err)
	}
	return id
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustUsage(t *testing.T, l *Ledger, d int) (bytes, entries, reserved int64) {
	t.Helper()
	b, e, r, err := l.Usage(d)
	if err != nil {
		t.Fatalf("Usage(%d): %v", d, err)
	}
	return b, e, r
}

func wantUsage(t *testing.T, l *Ledger, d int, wb, we, wr int64) {
	t.Helper()
	b, e, r := mustUsage(t, l, d)
	if b != wb || e != we || r != wr {
		t.Fatalf("Usage(%d) = (%d, %d, %d), want (%d, %d, %d)", d, b, e, r, wb, we, wr)
	}
}

func wantErrIs(t *testing.T, err error, sentinel error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", sentinel)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v is not %v", err, sentinel)
	}
}

func wantQuotaErr(t *testing.T, err error, sentinel error, dir int) {
	t.Helper()
	wantErrIs(t, err, sentinel)
	var qe *QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("error %v is not a QuotaError", err)
	}
	if qe.Dir != dir {
		t.Fatalf("quota violation dir = %d, want %d (err=%v)", qe.Dir, dir, err)
	}
}

func TestSmoke(t *testing.T) {
	l := NewLedger()
	id, err := l.Mkdir(RootID)
	if err != nil || id != 1 {
		t.Fatalf("Mkdir: id=%d err=%v", id, err)
	}
}

// 用量恰等于限额允许，超 1 拒绝（字节与条目两个维度）。
func TestQuotaExactAndOverflow(t *testing.T) {
	l := NewLedger()
	mustNoErr(t, l.SetQuota(RootID, 10, -1))
	mustAddFile(t, l, RootID, 10) // E(0)=10，恰等于限额
	_, err := l.AddFile(RootID, 1)
	wantQuotaErr(t, err, ErrByteQuotaExceeded, RootID)
	wantUsage(t, l, RootID, 10, 1, 0)

	l2 := NewLedger()
	mustNoErr(t, l2.SetQuota(RootID, -1, 2))
	mustMkdir(t, l2, RootID)
	mustMkdir(t, l2, RootID) // 条目数=2，恰等于限额
	_, err = l2.Mkdir(RootID)
	wantQuotaErr(t, err, ErrEntryQuotaExceeded, RootID)
	wantUsage(t, l2, RootID, 0, 2, 0)
}

// 同一目录先字节后条目数：两者都会超限时只报字节。
func TestByteCheckedBeforeEntry(t *testing.T) {
	l := NewLedger()
	mustNoErr(t, l.SetQuota(RootID, 0, 0))
	_, err := l.AddFile(RootID, 1)
	wantQuotaErr(t, err, ErrByteQuotaExceeded, RootID)
}

// 自近到远报告违规目录：最近的违规祖先优先。
func TestNearestViolatingDirReported(t *testing.T) {
	l := NewLedger()
	a := mustMkdir(t, l, RootID)
	b := mustMkdir(t, l, a)
	mustNoErr(t, l.SetQuota(a, 2, -1))
	mustNoErr(t, l.SetQuota(RootID, 3, -1))
	// AddFile(b, 4)：a 先违规（4>2），尽管 root 也会违规（4>3）。
	_, err := l.AddFile(b, 4)
	wantQuotaErr(t, err, ErrByteQuotaExceeded, a)

	// 放宽 a，root 成为违规者。
	mustNoErr(t, l.SetQuota(a, 5, -1))
	_, err = l.AddFile(b, 4)
	wantQuotaErr(t, err, ErrByteQuotaExceeded, RootID)

	// 条目维度同理。
	mustNoErr(t, l.SetQuota(a, -1, 2))
	mustNoErr(t, l.SetQuota(RootID, -1, 5))
	_, err = l.Mkdir(b) // a 的条目数 1→2 允许；再试一次应报 a
	mustNoErr(t, err)
	_, err = l.Mkdir(b)
	wantQuotaErr(t, err, ErrEntryQuotaExceeded, a)
}

// AddFile 用预留抵扣后的净增量：取等允许、超 1 拒绝。
func TestAddFileReservationNetDelta(t *testing.T) {
	l := NewLedger()
	mustNoErr(t, l.SetQuota(RootID, 100, -1))
	d := mustMkdir(t, l, RootID)
	mustNoErr(t, l.Reserve(d, 60)) // E(0)=60
	f1 := mustAddFile(t, l, d, 50) // c=50，净增 0，r_d 剩 10
	if f1 != 2 {
		t.Fatalf("f1 id = %d, want 2", f1)
	}
	wantUsage(t, l, RootID, 50, 2, 10)
	wantUsage(t, l, d, 50, 1, 10)

	mustAddFile(t, l, d, 50) // c=10，净增 40，E(0) 恰为 100
	wantUsage(t, l, RootID, 100, 3, 0)

	_, err := l.AddFile(d, 1) // 净增 1，101>100
	wantQuotaErr(t, err, ErrByteQuotaExceeded, RootID)
	wantUsage(t, l, RootID, 100, 3, 0)
}

// 预留大于 size 时只用掉 size。
func TestReservationLargerThanSize(t *testing.T) {
	l := NewLedger()
	mustNoErr(t, l.SetQuota(RootID, 100, -1))
	d := mustMkdir(t, l, RootID)
	mustNoErr(t, l.Reserve(d, 100)) // E(0)=100 恰好顶满
	mustAddFile(t, l, d, 30)        // c=30，净增 0
	wantUsage(t, l, RootID, 30, 2, 70)
	wantUsage(t, l, d, 30, 1, 70)
}

// Reserve 恰到上限允许，超 1 拒绝。
func TestReserveExactAndOverflow(t *testing.T) {
	l := NewLedger()
	mustNoErr(t, l.SetQuota(RootID, 10, -1))
	mustNoErr(t, l.Reserve(RootID, 10))
	wantUsage(t, l, RootID, 0, 0, 10)
	err := l.Reserve(RootID, 1)
	wantQuotaErr(t, err, ErrByteQuotaExceeded, RootID)
	wantUsage(t, l, RootID, 0, 0, 10)
}

// Release 恰等 r_d 允许，超 1 报预留不足。
func TestReleaseExactAndOverflow(t *testing.T) {
	l := NewLedger()
	mustNoErr(t, l.Reserve(RootID, 10))
	err := l.Release(RootID, 11)
	wantErrIs(t, err, ErrInsufficientReservation)
	mustNoErr(t, l.Release(RootID, 10))
	wantUsage(t, l, RootID, 0, 0, 0)
	err = l.Release(RootID, 1)
	wantErrIs(t, err, ErrInsufficientReservation)
}

// Resize 只算差值；限额已满时减小仍允许。
func TestResizeDeltaOnly(t *testing.T) {
	l := NewLedger()
	mustNoErr(t, l.SetQuota(RootID, 10, -1))
	f := mustAddFile(t, l, RootID, 10) // E(0)=10 顶满
	err := l.Resize(f, 20)             // 增量 10，超限
	wantQuotaErr(t, err, ErrByteQuotaExceeded, RootID)
	mustNoErr(t, l.Resize(f, 5)) // 减小：限额已满仍允许
	wantUsage(t, l, RootID, 5, 1, 0)
	mustNoErr(t, l.Resize(f, 10)) // 增量 5，恰好顶满
	err = l.Resize(f, 11)
	wantQuotaErr(t, err, ErrByteQuotaExceeded, RootID)
	mustNoErr(t, l.Resize(f, 10)) // 增量 0 恒允许
}

// SetQuota 恰等于当前 E 允许，差 1 报「低于现有用量」。
func TestSetQuotaBelowUsage(t *testing.T) {
	l := NewLedger()
	mustAddFile(t, l, RootID, 10)
	mustNoErr(t, l.SetQuota(RootID, 10, -1))
	err := l.SetQuota(RootID, 9, -1)
	wantErrIs(t, err, ErrBelowUsage)
	var be *BelowUsageError
	if !errors.As(err, &be) || be.Dir != RootID || !be.Byte {
		t.Fatalf("BelowUsageError = %+v", be)
	}
	// 条目维度同理。
	mustNoErr(t, l.SetQuota(RootID, -1, 1))
	err = l.SetQuota(RootID, -1, 0)
	wantErrIs(t, err, ErrBelowUsage)
	// 限额对有效字节生效：预留计入 E。
	mustNoErr(t, l.Reserve(RootID, 5)) // E(0)=15
	err = l.SetQuota(RootID, 14, -1)
	wantErrIs(t, err, ErrBelowUsage)
	mustNoErr(t, l.SetQuota(RootID, 15, -1))
}

// Remove 目录退还其自身预留；Remove 文件退还字节与条目。
func TestRemoveRefunds(t *testing.T) {
	l := NewLedger()
	d := mustMkdir(t, l, RootID)
	mustNoErr(t, l.Reserve(d, 50))
	f := mustAddFile(t, l, RootID, 20)
	wantUsage(t, l, RootID, 20, 2, 50)
	mustNoErr(t, l.Remove(d)) // 空目录，退还 r_d=50
	wantUsage(t, l, RootID, 20, 1, 0)
	mustNoErr(t, l.Remove(f))
	wantUsage(t, l, RootID, 0, 0, 0)
	// 编号不回退。
	id := mustMkdir(t, l, RootID)
	if id != 3 {
		t.Fatalf("id = %d, want 3", id)
	}
}

// Rename：公共祖先限额恰等于当前有效字节仍成功（公共祖先不核对）。
func TestRenameCommonAncestorNotChecked(t *testing.T) {
	l := NewLedger()
	a := mustMkdir(t, l, RootID)
	b := mustMkdir(t, l, a)
	mustAddFile(t, l, b, 10)
	mustNoErr(t, l.SetQuota(RootID, 10, -1)) // E(0)=10 恰顶满
	mustNoErr(t, l.SetQuota(a, 10, -1))
	// 把 b 从 a 移到 root：root 是公共祖先，不核对；a 退还。
	mustNoErr(t, l.Rename(b, RootID))
	wantUsage(t, l, RootID, 10, 3, 0)
	wantUsage(t, l, a, 0, 0, 0)
	wantUsage(t, l, b, 10, 1, 0)
}

// Rename：新增链上的目录限额差 1 失败，恰好等于则成功。
func TestRenameNewChainChecked(t *testing.T) {
	l := NewLedger()
	a := mustMkdir(t, l, RootID)
	c := mustMkdir(t, l, RootID)
	f := mustAddFile(t, l, a, 10)
	mustNoErr(t, l.SetQuota(c, 9, -1))
	err := l.Rename(f, c) // 新链只有 c：E 增量 10 > 9
	wantQuotaErr(t, err, ErrByteQuotaExceeded, c)
	wantUsage(t, l, a, 10, 1, 0)
	wantUsage(t, l, c, 0, 0, 0)
	mustNoErr(t, l.SetQuota(c, 10, -1))
	mustNoErr(t, l.Rename(f, c))
	wantUsage(t, l, a, 0, 0, 0)
	wantUsage(t, l, c, 10, 1, 0)
	wantUsage(t, l, RootID, 10, 3, 0)
}

// Rename：目录带走内部全部条目、字节与预留，旧链逐级退还。
func TestRenameDirMovesEverything(t *testing.T) {
	l := NewLedger()
	a := mustMkdir(t, l, RootID)  // 1
	b := mustMkdir(t, l, a)       // 2
	f := mustAddFile(t, l, b, 30) // 3
	g := mustAddFile(t, l, a, 20) // 4
	mustNoErr(t, l.Reserve(b, 7)) // b 自身预留
	mustNoErr(t, l.Reserve(a, 5)) // a 自身预留
	c := mustMkdir(t, l, RootID)  // 5
	d := mustMkdir(t, l, c)       // 6
	// a: bytes=50, entries=3(b,f,g), R=12；root: bytes=50, entries=6, R=12
	wantUsage(t, l, a, 50, 3, 12)
	wantUsage(t, l, RootID, 50, 6, 12)

	mustNoErr(t, l.Rename(a, d))
	// 旧链（root）与 新链（d,c,root）的公共祖先 root 不变。
	wantUsage(t, l, RootID, 50, 6, 12)
	wantUsage(t, l, c, 50, 5, 12) // a 及其内部全部计入
	wantUsage(t, l, d, 50, 4, 12) // a 自身算 d 的一个条目
	wantUsage(t, l, a, 50, 3, 12) // a 自身子树不变
	wantUsage(t, l, b, 30, 1, 7)
	_ = f
	_ = g
}

// Rename：旧链逐级退还（多级祖先）。
func TestRenameRefundsOldChainLevelByLevel(t *testing.T) {
	l := NewLedger()
	a := mustMkdir(t, l, RootID)
	b := mustMkdir(t, l, a)
	c := mustMkdir(t, l, b)
	f := mustAddFile(t, l, c, 40)
	mustNoErr(t, l.Reserve(c, 6))
	// 把 f 从 c 直接移到 root：c、b、a 逐级退还，root 不变（公共）。
	mustNoErr(t, l.Rename(f, RootID))
	wantUsage(t, l, c, 0, 0, 6)
	wantUsage(t, l, b, 0, 1, 6)
	wantUsage(t, l, a, 0, 2, 6)
	wantUsage(t, l, RootID, 40, 4, 6)
}

// Rename：p 恰为当前父目录时成功且什么都不改。
func TestRenameNoopSameParent(t *testing.T) {
	l := NewLedger()
	a := mustMkdir(t, l, RootID)
	f := mustAddFile(t, l, a, 10)
	mustNoErr(t, l.Rename(f, a))
	mustNoErr(t, l.Rename(a, RootID))
	wantUsage(t, l, RootID, 10, 2, 0)
	wantUsage(t, l, a, 10, 1, 0)
}

// 拒绝顺序：参数非法先于节点不存在，节点不存在先于类型不符。
func TestErrorOrdering(t *testing.T) {
	l := NewLedger()
	f := mustAddFile(t, l, RootID, 1)

	// 参数非法优先于节点不存在。
	_, err := l.AddFile(999, -1)
	wantErrIs(t, err, ErrInvalidArgument)
	wantErrIs(t, l.SetQuota(999, -2, 0), ErrInvalidArgument)
	wantErrIs(t, l.Reserve(999, 0), ErrInvalidArgument)
	wantErrIs(t, l.Release(999, -3), ErrInvalidArgument)
	wantErrIs(t, l.Resize(999, -1), ErrInvalidArgument)

	// 节点不存在先于类型不符；涉及两个节点时先 x 后 p。
	wantErrIs(t, l.SetQuota(999, 0, 0), ErrNotFound)
	wantErrIs(t, l.Reserve(999, 1), ErrNotFound)
	wantErrIs(t, l.Resize(999, 1), ErrNotFound)
	wantErrIs(t, l.Remove(999), ErrNotFound)
	wantErrIs(t, l.Rename(999, f), ErrNotFound) // x 不存在，即使 p 是文件也先报不存在
	wantErrIs(t, l.Rename(f, 999), ErrNotFound) // x 存在、p 不存在

	// 类型不符。
	_, err = l.Mkdir(f)
	wantErrIs(t, err, ErrTypeMismatch)
	_, err = l.AddFile(f, 1)
	wantErrIs(t, err, ErrTypeMismatch)
	wantErrIs(t, l.SetQuota(f, 1, 1), ErrTypeMismatch)
	wantErrIs(t, l.Reserve(f, 1), ErrTypeMismatch)
	wantErrIs(t, l.Release(f, 1), ErrTypeMismatch)
	wantErrIs(t, l.Resize(RootID, 1), ErrTypeMismatch)
	wantErrIs(t, l.Rename(f, f), ErrTypeMismatch) // p 是文件
}

// 结构错误：根、非空目录、移入自身或子孙。
func TestStructuralErrors(t *testing.T) {
	l := NewLedger()
	a := mustMkdir(t, l, RootID)
	b := mustMkdir(t, l, a)
	f := mustAddFile(t, l, b, 1)

	wantErrIs(t, l.Remove(RootID), ErrRoot)
	wantErrIs(t, l.Rename(RootID, a), ErrRoot)
	wantErrIs(t, l.Remove(a), ErrNotEmpty)
	wantErrIs(t, l.Remove(b), ErrNotEmpty)
	wantErrIs(t, l.Rename(a, a), ErrCycle)
	wantErrIs(t, l.Rename(a, b), ErrCycle)

	// 被拒绝的操作不改变任何状态。
	wantUsage(t, l, RootID, 1, 3, 0)
	wantUsage(t, l, a, 1, 2, 0)
	mustNoErr(t, l.Remove(f))
	mustNoErr(t, l.Remove(b))
	mustNoErr(t, l.Remove(a))
	wantUsage(t, l, RootID, 0, 0, 0)
}

// 题目中的完整示例场景。
func TestExampleScenario(t *testing.T) {
	l := NewLedger()
	mustNoErr(t, l.SetQuota(RootID, 100, -1))
	d1 := mustMkdir(t, l, RootID) // 1
	d2 := mustMkdir(t, l, RootID) // 2
	mustNoErr(t, l.Reserve(d1, 60))
	wantUsage(t, l, RootID, 0, 2, 60)

	_, err := l.AddFile(d2, 50) // 60+50>100，违规目录为 0
	wantQuotaErr(t, err, ErrByteQuotaExceeded, RootID)

	id3, err := l.AddFile(d1, 50) // c=50，E 不变，r_1 剩 10
	mustNoErr(t, err)
	if id3 != 3 {
		t.Fatalf("id = %d, want 3", id3)
	}
	wantUsage(t, l, RootID, 50, 3, 10)

	id4, err := l.AddFile(d1, 50) // c=10，净增 40，E(0) 恰为 100
	mustNoErr(t, err)
	if id4 != 4 {
		t.Fatalf("id = %d, want 4", id4)
	}
	wantUsage(t, l, RootID, 100, 4, 0)

	wantErrIs(t, l.SetQuota(RootID, 99, -1), ErrBelowUsage)
	mustNoErr(t, l.SetQuota(RootID, 100, -1))

	// Batch[Mkdir(0), AddFile(5,1)] 在下标 1 失败并整批回滚。
	idx, err := l.Batch([]Op{OpMkdir(RootID), OpAddFile(5, 1)})
	if idx != 1 {
		t.Fatalf("batch fail index = %d, want 1", idx)
	}
	wantErrIs(t, err, ErrBatchFailed)
	wantErrIs(t, err, ErrByteQuotaExceeded)
	wantUsage(t, l, RootID, 100, 4, 0) // 用量回滚

	id5, err := l.Mkdir(RootID) // 编号 5 未被批处理消耗
	mustNoErr(t, err)
	if id5 != 5 {
		t.Fatalf("id = %d, want 5", id5)
	}
}

// Batch 中途失败：用量、预留、限额、编号全部回滚。
func TestBatchRollbackRestoresEverything(t *testing.T) {
	l := NewLedger()
	mustNoErr(t, l.SetQuota(RootID, 100, -1))
	d := mustMkdir(t, l, RootID) // 1
	mustNoErr(t, l.Reserve(d, 10))
	wantUsage(t, l, RootID, 0, 1, 10)

	idx, err := l.Batch([]Op{
		OpSetQuota(RootID, 50, -1), // 改限额
		OpReserve(d, 20),           // 改预留
		OpAddFile(d, 5),            // 改用量（消耗编号 2）
		OpRelease(d, 100),          // 预留不足：失败
	})
	if idx != 3 {
		t.Fatalf("batch fail index = %d, want 3", idx)
	}
	wantErrIs(t, err, ErrBatchFailed)
	wantErrIs(t, err, ErrInsufficientReservation)

	// 限额、预留、用量全部恢复。
	wantUsage(t, l, RootID, 0, 1, 10)
	mustNoErr(t, l.Reserve(d, 90)) // 限额仍是 100：E(0)=100 允许
	err = l.Reserve(d, 1)
	wantQuotaErr(t, err, ErrByteQuotaExceeded, RootID)

	// 编号未被批内成功的 AddFile 消耗。
	id, err := l.AddFile(d, 5)
	mustNoErr(t, err)
	if id != 2 {
		t.Fatalf("id = %d, want 2", id)
	}
}

// Batch 内引用前序新建节点的编号。
func TestBatchReferencesEarlierNodes(t *testing.T) {
	l := NewLedger()
	idx, err := l.Batch([]Op{
		OpMkdir(RootID),     // 1
		OpMkdir(1),          // 2
		OpAddFile(2, 30),    // 3
		OpReserve(1, 10),    //
		OpResize(3, 40),     //
		OpRename(2, RootID), //
		OpSetQuota(2, 40, -1),
		OpRelease(1, 10),
	})
	if idx != -1 || err != nil {
		t.Fatalf("batch: idx=%d err=%v", idx, err)
	}
	wantUsage(t, l, RootID, 40, 3, 0)
	wantUsage(t, l, 1, 0, 0, 0)
	wantUsage(t, l, 2, 40, 1, 0)
}

// 空批报参数非法。
func TestBatchEmptyInvalid(t *testing.T) {
	l := NewLedger()
	idx, err := l.Batch(nil)
	if idx != -1 {
		t.Fatalf("idx = %d, want -1", idx)
	}
	wantErrIs(t, err, ErrInvalidArgument)
	if errors.Is(err, ErrBatchFailed) {
		t.Fatalf("empty batch should not be ErrBatchFailed")
	}
}

// Batch 失败错误同时可判出「批处理失败」与底层原因，并保留下标。
func TestBatchErrorWrapping(t *testing.T) {
	l := NewLedger()
	mustNoErr(t, l.SetQuota(RootID, 0, -1))
	idx, err := l.Batch([]Op{OpAddFile(RootID, 1)})
	if idx != 0 {
		t.Fatalf("idx = %d, want 0", idx)
	}
	wantErrIs(t, err, ErrBatchFailed)
	wantErrIs(t, err, ErrByteQuotaExceeded)
	var be *BatchError
	if !errors.As(err, &be) || be.Index != 0 {
		t.Fatalf("BatchError = %+v", be)
	}
	var qe *QuotaError
	if !errors.As(err, &qe) || qe.Dir != RootID {
		t.Fatalf("QuotaError = %+v", qe)
	}
}

// 并发调用：结果等价于某个串行顺序，最终聚合与结构一致。
func TestConcurrentOps(t *testing.T) {
	l := NewLedger()
	mustNoErr(t, l.SetQuota(RootID, 1<<40, 1<<20))
	const workers = 8
	const rounds = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				switch (w + i) % 6 {
				case 0:
					id, err := l.Mkdir(RootID)
					if err == nil {
						_ = l.Reserve(id, 1)
						_ = l.Release(id, 1)
					}
				case 1:
					id, err := l.AddFile(RootID, 1)
					if err == nil {
						_ = l.Resize(id, 2)
					}
				case 2:
					_, _, _, _ = l.Usage(RootID)
				case 3:
					id, err := l.AddFile(RootID, 3)
					if err == nil {
						_ = l.Remove(id)
					}
				case 4:
					_, _ = l.Batch([]Op{OpMkdir(RootID), OpAddFile(RootID, 1)})
				case 5:
					id, err := l.Mkdir(RootID)
					if err == nil {
						_ = l.Rename(id, RootID) // 无操作改名
					}
				}
			}
		}(w)
	}
	wg.Wait()

	// 最终不变量：每个目录的聚合等于从结构重新累加的结果。
	l.mu.RLock()
	defer l.mu.RUnlock()
	for id, n := range l.st.nodes {
		if !n.dir {
			continue
		}
		var bytes, entries, reserved int64
		for mid, m := range l.st.nodes {
			if mid == id {
				continue
			}
			for cur := mid; cur != -1; cur = l.st.nodes[cur].parent {
				if cur == id {
					entries++
					if !m.dir {
						bytes += m.size
					} else {
						reserved += m.r
					}
					break
				}
			}
		}
		reserved += n.r
		if n.bytes != bytes || n.entries != entries || n.reserved != reserved {
			t.Fatalf("dir %d aggregates = (%d,%d,%d), recomputed = (%d,%d,%d)",
				id, n.bytes, n.entries, n.reserved, bytes, entries, reserved)
		}
		if n.byteLimit >= 0 && n.bytes+n.reserved > n.byteLimit {
			t.Fatalf("dir %d exceeds byte limit", id)
		}
		if n.entryLimit >= 0 && n.entries > n.entryLimit {
			t.Fatalf("dir %d exceeds entry limit", id)
		}
	}
}
