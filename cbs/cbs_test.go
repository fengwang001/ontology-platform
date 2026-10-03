package cbs_test

import (
	"errors"
	"math/big"
	"sync"
	"testing"

	"ontology/cbs"
)

func mustAdd(t *testing.T, l *cbs.Ledger, id string, q, p uint64) {
	t.Helper()
	if err := l.Add(id, q, p); err != nil {
		t.Fatalf("Add(%q,%d,%d) = %v", id, q, p, err)
	}
}

func mustOK(t *testing.T, op string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s = %v, want nil", op, err)
	}
}

func mustErr(t *testing.T, op string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s = %v, want %v", op, err, want)
	}
}

func mustState(t *testing.T, l *cbs.Ledger, id string) cbs.Snapshot {
	t.Helper()
	snap, err := l.State(id)
	if err != nil {
		t.Fatalf("State(%q) = %v", id, err)
	}
	return snap
}

func checkSnap(t *testing.T, l *cbs.Ledger, id string, st cbs.State, q, d, w uint64) {
	t.Helper()
	snap := mustState(t, l, id)
	if snap.State != st || snap.Remaining != q || snap.Deadline != d || snap.Backlog != w {
		t.Fatalf("State(%q) = {%v q=%d d=%d w=%d}, want {%v q=%d d=%d w=%d}",
			id, snap.State, snap.Remaining, snap.Deadline, snap.Backlog, st, q, d, w)
	}
}

// TestSpecExample 逐条复现需求文档中的示例。
func TestSpecExample(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "S1", 3, 5)
	mustAdd(t, l, "S2", 2, 5)
	mustAdd(t, l, "S3", 1, 10)

	mustOK(t, "Wake(S1,0,1)", l.Wake("S1", 0, 1))
	mustOK(t, "Wake(S2,0,4)", l.Wake("S2", 0, 4)) // 3/5+2/5 = 1，恰好通过
	if got := l.Total().RatString(); got != "1" {
		t.Fatalf("Total = %s, want 1", got)
	}
	checkSnap(t, l, "S1", cbs.StateReady, 3, 5, 1)
	checkSnap(t, l, "S2", cbs.StateReady, 2, 5, 4)

	// S1 与 S2 并列 d=5，编号字节序小者 S1 可运行。
	mustOK(t, "Run(S1,0,1)", l.Run("S1", 0, 1))
	checkSnap(t, l, "S1", cbs.StateIdleOccupying, 2, 5, 0)
	if l.Now() != 1 {
		t.Fatalf("now = %d, want 1", l.Now())
	}
	// 2·5=10 < (5-1)·3=12，零松弛未到，仍占用 3/5。
	mustErr(t, "Wake(S3,1,1)", l.Wake("S3", 1, 1), cbs.ErrInsufficientBandwidth)
	// 时钟 2：10 >= (5-2)·3=9，S1 已释放，带宽可被他人使用。
	mustOK(t, "Wake(S3,2,1)", l.Wake("S3", 2, 1))
	checkSnap(t, l, "S3", cbs.StateReady, 1, 12, 1)

	// S2 恰耗尽预算：q 立即补回 2，d 5→10，w 剩 2，时钟 4。
	mustOK(t, "Run(S2,2,2)", l.Run("S2", 2, 2))
	checkSnap(t, l, "S2", cbs.StateReady, 2, 10, 2)
	if l.Now() != 4 {
		t.Fatalf("now = %d, want 4", l.Now())
	}
	// S1 已释放须重新接纳：0.4+0.1+0.6 > 1，被拒；旧 q、d 保留。
	mustErr(t, "Wake(S1,4,1)", l.Wake("S1", 4, 1), cbs.ErrInsufficientBandwidth)
	checkSnap(t, l, "S1", cbs.StateReleased, 2, 5, 0)
}

// TestZeroLaxityExactBoundary 验证 q·P == (d-now)·Q 时已释放。
func TestZeroLaxityExactBoundary(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "A", 2, 4) // 占用 1/2
	mustAdd(t, l, "B", 1, 2) // 占用 1/2，用于推进时钟
	mustOK(t, "Wake(A,0,1)", l.Wake("A", 0, 1))
	mustOK(t, "Run(A,0,1)", l.Run("A", 0, 1)) // q=1, d=4, w=0, 时钟 1
	// q·P = 1·4 = 4；now=2 时 (d-now)·Q = (4-2)·2 = 4，恰相等 → 已释放。
	mustOK(t, "Wake(B,2,1)", l.Wake("B", 2, 1)) // A 已释放，B 接纳后总带宽 1/2
	checkSnap(t, l, "A", cbs.StateReleased, 1, 4, 0)
}

// TestZeroLaxityOneShort 验证乘积差一仍占用。
func TestZeroLaxityOneShort(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "A", 2, 3)
	mustAdd(t, l, "B", 1, 10)
	mustOK(t, "Wake(A,0,1)", l.Wake("A", 0, 1))
	mustOK(t, "Run(A,0,1)", l.Run("A", 0, 1)) // q=1, d=3, w=0, 时钟 1
	// q·P = 1·3 = 3；(d-now)·Q = (3-1)·2 = 4，恰好多 1 → 仍占用。
	checkSnap(t, l, "A", cbs.StateIdleOccupying, 1, 3, 0)
	// 推进到时钟 2：(3-2)·2 = 2 <= 3 → 已释放。
	mustOK(t, "Wake(B,2,1)", l.Wake("B", 2, 1))
	checkSnap(t, l, "A", cbs.StateReleased, 1, 3, 0)
}

// TestIdleWakeKeepsBudgetAndDeadline 验证空闲占用者被唤醒时不重置 q 与 d。
func TestIdleWakeKeepsBudgetAndDeadline(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "A", 3, 5)
	mustOK(t, "Wake(A,0,1)", l.Wake("A", 0, 1))
	mustOK(t, "Run(A,0,1)", l.Run("A", 0, 1)) // q=2, d=5, w=0, 时钟 1
	// 2·5=10 < (5-1)·3=12 → 空闲占用。
	checkSnap(t, l, "A", cbs.StateIdleOccupying, 2, 5, 0)
	mustOK(t, "Wake(A,1,7)", l.Wake("A", 1, 7))
	checkSnap(t, l, "A", cbs.StateReady, 2, 5, 7) // q、d 保持不变
}

// TestReleasedWakeResets 验证已释放者被唤醒时重置 q=Q、d=now+P。
func TestReleasedWakeResets(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "A", 3, 5)
	mustAdd(t, l, "B", 1, 10)
	mustOK(t, "Wake(A,0,1)", l.Wake("A", 0, 1))
	mustOK(t, "Run(A,0,1)", l.Run("A", 0, 1)) // q=2, d=5, w=0, 时钟 1
	mustOK(t, "Wake(B,2,1)", l.Wake("B", 2, 1))
	checkSnap(t, l, "A", cbs.StateReleased, 2, 5, 0)
	mustOK(t, "Wake(A,2,4)", l.Wake("A", 2, 4))
	checkSnap(t, l, "A", cbs.StateReady, 3, 7, 4) // q=Q=3, d=2+5=7
}

// TestRefillExactAndMultiple 验证 q 归 0 恰好补充，以及 δ 跨越多次补充。
func TestRefillExactAndMultiple(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "A", 2, 5)
	mustOK(t, "Wake(A,0,7)", l.Wake("A", 0, 7))
	// δ=2 恰耗尽预算：q 立即补回 2，d 5→10，w 剩 5，时钟 2。
	mustOK(t, "Run(A,0,2)", l.Run("A", 0, 2))
	checkSnap(t, l, "A", cbs.StateReady, 2, 10, 5)
	// δ=5 跨越两次补充：q 2→1→0(补,d15)→1→0(补,d20)→1，w 归 0，时钟 7。
	mustOK(t, "Run(A,2,5)", l.Run("A", 2, 5))
	checkSnap(t, l, "A", cbs.StateIdleOccupying, 1, 20, 0)
	if l.Now() != 7 {
		t.Fatalf("now = %d, want 7", l.Now())
	}
}

// TestRefillWholePeriods 验证 δ 为 Q 的整数倍时 d 一次加多个 P 且 q 满格。
func TestRefillWholePeriods(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "A", 2, 5)
	mustOK(t, "Wake(A,0,6)", l.Wake("A", 0, 6))
	// δ=6 = 3 个完整预算：补充 3 次，d 5→20，q 满格 2，w 归 0。
	mustOK(t, "Run(A,0,6)", l.Run("A", 0, 6))
	checkSnap(t, l, "A", cbs.StateIdleOccupying, 2, 20, 0)
}

// TestNotEarliestDeadline 验证非最早截止者不可运行，并列取编号字节序小者。
func TestNotEarliestDeadline(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "A", 1, 5)
	mustAdd(t, l, "B", 1, 3)
	mustAdd(t, l, "C", 1, 5)
	mustOK(t, "Wake(A,0,1)", l.Wake("A", 0, 1)) // d=5
	mustOK(t, "Wake(B,0,1)", l.Wake("B", 0, 1)) // d=3
	mustOK(t, "Wake(C,0,1)", l.Wake("C", 0, 1)) // d=5
	// B 的 d=3 最早，A/C 均不可运行。
	mustErr(t, "Run(A,0,1)", l.Run("A", 0, 1), cbs.ErrNotEarliestDeadline)
	mustOK(t, "Run(B,0,1)", l.Run("B", 0, 1))
	// B 的 d 变为 6 后，A 与 C 并列 d=5，编号小者 A 优先。
	if id, ok := l.Next(); !ok || id != "A" {
		t.Fatalf("Next = %q,%v, want A,true", id, ok)
	}
	mustErr(t, "Run(C,1,1)", l.Run("C", 1, 1), cbs.ErrNotEarliestDeadline)
	mustOK(t, "Run(A,1,1)", l.Run("A", 1, 1))
}

// TestProductOverflow64Bit 验证 (d-now)·Q 溢出 64 位时零松弛判定仍正确。
//
// Q=2, P=1e6，累积 w=2e13 后一次运行耗尽：d ≈ 1e19，d-now ≈ 1e19，
// (d-now)·Q ≈ 2e19 > 2^64-1 ≈ 1.8e19，必须走 128 位路径。
func TestProductOverflow64Bit(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "BIG", 2, 1_000_000)
	for i := 0; i < 20_000; i++ {
		mustOK(t, "Wake(BIG)", l.Wake("BIG", 0, 1_000_000_000))
	}
	mustOK(t, "Run(BIG,0,2e13)", l.Run("BIG", 0, 20_000_000_000_000))
	snap := mustState(t, l, "BIG")
	if snap.Backlog != 0 || snap.Remaining != 2 {
		t.Fatalf("BIG = {q=%d w=%d}, want {q=2 w=0}", snap.Remaining, snap.Backlog)
	}
	// 用 big.Int 独立复核判定依据。
	lhs := new(big.Int).Mul(big.NewInt(int64(snap.Remaining)), big.NewInt(1_000_000))
	rhs := new(big.Int).Mul(
		new(big.Int).SetUint64(snap.Deadline-l.Now()), big.NewInt(2))
	if rhs.BitLen() <= 64 {
		t.Fatalf("(d-now)·Q = %s 未溢出 64 位，用例无效", rhs)
	}
	if lhs.Cmp(rhs) >= 0 {
		t.Fatalf("q·P=%s >= (d-now)·Q=%s，应为空闲占用", lhs, rhs)
	}
	if snap.State != cbs.StateIdleOccupying {
		t.Fatalf("State = %v, want IdleOccupying（128 位比较）", snap.State)
	}
	if got := l.Total().RatString(); got != "1/500000" {
		t.Fatalf("Total = %s, want 1/500000", got)
	}
}

// TestRejectedOpsDoNotMutate 验证被拒绝的操作不改变任何 q、d、w 与时钟。
func TestRejectedOpsDoNotMutate(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "A", 3, 5)
	mustAdd(t, l, "B", 2, 5)
	mustAdd(t, l, "C", 1, 10)
	mustOK(t, "Wake(A,0,1)", l.Wake("A", 0, 1))
	mustOK(t, "Wake(B,0,4)", l.Wake("B", 0, 4))
	mustOK(t, "Run(A,0,1)", l.Run("A", 0, 1)) // 时钟 1，A 空闲占用

	snapBefore := map[string]cbs.Snapshot{}
	for _, id := range []string{"A", "B", "C"} {
		snapBefore[id] = mustState(t, l, id)
	}
	nowBefore := l.Now()

	// 各类拒绝：带宽不足、时钟回退、非最早截止、不可运行、占用中、参数非法。
	mustErr(t, "Wake(C,1,1)", l.Wake("C", 1, 1), cbs.ErrInsufficientBandwidth)
	mustErr(t, "Wake(A,0,1)", l.Wake("A", 0, 1), cbs.ErrClockRegression)
	mustErr(t, "Run(A,1,1)", l.Run("A", 1, 1), cbs.ErrNotRunnable)
	mustErr(t, "Run(B,1,5)", l.Run("B", 1, 5), cbs.ErrNotRunnable)
	mustErr(t, "Remove(B)", l.Remove("B"), cbs.ErrBusy)
	mustErr(t, "Wake(A,1,0)", l.Wake("A", 1, 0), cbs.ErrInvalidParam)

	if l.Now() != nowBefore {
		t.Fatalf("now = %d, want %d（拒绝不得推进时钟）", l.Now(), nowBefore)
	}
	for id, want := range snapBefore {
		if got := mustState(t, l, id); got != want {
			t.Fatalf("State(%q) = %+v, want %+v（拒绝不得改状态）", id, got, want)
		}
	}
}

// TestWakeRetryAfterBandwidthFrees 验证已释放者 Wake 被拒后保留旧 q、d，
// 他人释放带宽后同一次 Wake 重试可通过。
func TestWakeRetryAfterBandwidthFrees(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "A", 3, 5)
	mustAdd(t, l, "B", 2, 5)
	mustAdd(t, l, "C", 1, 10)
	mustOK(t, "Wake(A,0,1)", l.Wake("A", 0, 1))
	mustOK(t, "Wake(B,0,4)", l.Wake("B", 0, 4))
	mustOK(t, "Run(A,0,1)", l.Run("A", 0, 1))
	mustOK(t, "Wake(C,2,1)", l.Wake("C", 2, 1)) // A 在时钟 2 已释放
	mustOK(t, "Run(B,2,2)", l.Run("B", 2, 2))   // 时钟 4
	mustErr(t, "Wake(A,4,1)", l.Wake("A", 4, 1), cbs.ErrInsufficientBandwidth)
	checkSnap(t, l, "A", cbs.StateReleased, 2, 5, 0) // 旧 q、d 保留
	// B、C 依次运行完毕；时钟 12 时 B（10>=(15-12)·2）与 C（10>=(22-12)·1）
	// 均已释放带宽，A 重试成功。
	mustOK(t, "Run(B,4,2)", l.Run("B", 4, 2)) // B w 归 0，时钟 6
	mustOK(t, "Run(C,6,1)", l.Run("C", 6, 1)) // C w 归 0，时钟 7
	mustOK(t, "Wake(A,12,1)", l.Wake("A", 12, 1))
	checkSnap(t, l, "A", cbs.StateReady, 3, 17, 1)
}

// TestErrorPrecedence 验证拒绝原因按固定顺序只报第一个。
func TestErrorPrecedence(t *testing.T) {
	l := cbs.NewLedger()
	mustAdd(t, l, "A", 1, 2)
	mustOK(t, "Wake(A,5,1)", l.Wake("A", 5, 1)) // 时钟 5

	// 参数非法优先于编号不存在与时钟回退。
	mustErr(t, "Wake(empty)", l.Wake("", 0, 1), cbs.ErrInvalidParam)
	mustErr(t, "Wake(work=0)", l.Wake("ZZ", 0, 0), cbs.ErrInvalidParam)
	mustErr(t, "Wake(work>1e9)", l.Wake("ZZ", 0, 1_000_000_001), cbs.ErrInvalidParam)
	mustErr(t, "Wake(now>1e15)", l.Wake("ZZ", 1_000_000_000_000_001, 1), cbs.ErrInvalidParam)
	// 编号不存在优先于时钟回退。
	mustErr(t, "Wake(ZZ,0,1)", l.Wake("ZZ", 0, 1), cbs.ErrNotFound)
	// 时钟回退优先于其他运行期检查。
	mustErr(t, "Wake(A,4,1)", l.Wake("A", 4, 1), cbs.ErrClockRegression)
	mustErr(t, "Run(A,4,1)", l.Run("A", 4, 1), cbs.ErrClockRegression)
	// Run：δ 越界为参数非法；δ>w 为不可运行。
	mustErr(t, "Run(A,5,0)", l.Run("A", 5, 0), cbs.ErrInvalidParam)
	mustErr(t, "Run(A,5,2)", l.Run("A", 5, 2), cbs.ErrNotRunnable)
	mustErr(t, "Run(ZZ,5,1)", l.Run("ZZ", 5, 1), cbs.ErrNotFound)
}

// TestAddAndRemove 验证 Add 的三类错误与 Remove 的占用中检查。
func TestAddAndRemove(t *testing.T) {
	l := cbs.NewLedger()
	mustErr(t, "Add(empty)", l.Add("", 1, 1), cbs.ErrInvalidParam)
	mustErr(t, "Add(Q=0)", l.Add("X", 0, 1), cbs.ErrInvalidParam)
	mustErr(t, "Add(Q>P)", l.Add("X", 3, 2), cbs.ErrInvalidParam)
	mustErr(t, "Add(P>1e6)", l.Add("X", 1, 1_000_001), cbs.ErrInvalidParam)

	mustAdd(t, l, "X", 1, 2)
	mustErr(t, "Add(dup)", l.Add("X", 1, 2), cbs.ErrDuplicateID)

	for i := 0; i < cbs.MaxServers-1; i++ {
		mustAdd(t, l, string(rune('a'+i)), 1, 1_000_000)
	}
	mustErr(t, "Add(full)", l.Add("overflow", 1, 1), cbs.ErrCapacityFull)

	// 从未唤醒者为已释放，可直接删除。
	mustOK(t, "Remove(X)", l.Remove("X"))
	mustErr(t, "Remove(X)", l.Remove("X"), cbs.ErrNotFound)
	mustErr(t, "Remove(empty)", l.Remove(""), cbs.ErrInvalidParam)

	// 就绪者不可删除；w 归 0 且零松弛未到时（空闲占用）也不可删除。
	mustAdd(t, l, "Y", 1, 2)
	mustOK(t, "Wake(Y,0,1)", l.Wake("Y", 0, 1))
	mustErr(t, "Remove(Y ready)", l.Remove("Y"), cbs.ErrBusy)
	mustOK(t, "Run(Y,0,1)", l.Run("Y", 0, 1)) // q 补满 d=4，w=0，时钟 1
	// q·P=2 < (4-1)·1=3 → 空闲占用，仍不可删。
	mustErr(t, "Remove(Y idle)", l.Remove("Y"), cbs.ErrBusy)
}

// TestConcurrent 并发调用所有操作与查询，配合 -race 检测数据竞争，
// 并随时校验不变量：占用带宽之和不超过 1，占用者 q 恒在 1..Q，空闲占用者 d>now。
func TestConcurrent(t *testing.T) {
	l := cbs.NewLedger()
	ids := []string{"a", "b", "c", "d"}
	for i, id := range ids {
		mustAdd(t, l, id, uint64(i+1), 8)
	}
	checkInvariants := func() {
		if l.Total().Cmp(big.NewRat(1, 1)) > 0 {
			t.Errorf("占用带宽之和 %s 超过 1", l.Total().RatString())
		}
		for _, id := range ids {
			snap, err := l.State(id)
			if err != nil {
				continue
			}
			switch snap.State {
			case cbs.StateReady, cbs.StateIdleOccupying:
				if snap.Remaining < 1 || snap.Remaining > snap.Q {
					t.Errorf("%s q=%d 越界 [1,%d]", id, snap.Remaining, snap.Q)
				}
				if snap.State == cbs.StateIdleOccupying && snap.Deadline <= l.Now() {
					t.Errorf("%s 空闲占用但 d=%d <= now=%d", id, snap.Deadline, l.Now())
				}
			}
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				id := ids[(g+i)%len(ids)]
				now := l.Now()
				switch (g + i) % 5 {
				case 0:
					_ = l.Wake(id, now, 1)
				case 1:
					_ = l.Run(id, now, 1)
				case 2:
					_, _ = l.State(id)
				case 3:
					_, _ = l.Next()
				default:
					_ = l.Total()
				}
				if i%50 == 0 {
					checkInvariants()
				}
			}
		}(g)
	}
	wg.Wait()
	checkInvariants()
}
