package interest

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, start int64) *Account {
	t.Helper()
	a, err := New(start)
	if err != nil {
		t.Fatalf("New(%d) 失败: %v", start, err)
	}
	return a
}

func mustErrKind(t *testing.T, err error, kind ErrKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %v，实际成功", kind)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("错误类型不是 *Error: %v", err)
	}
	if e.Kind != kind {
		t.Fatalf("期望错误种类 %v，实际 %v（%v）", kind, e.Kind, err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际失败: %v", err)
	}
}

// 余数结转：题目示例的两次结息。
func TestSettleRemainderCarry(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 1000000))
	mustOK(t, a.SetRate(0, 365))

	i, bal, rem, err := a.Settle(10)
	mustOK(t, err)
	if i != 1013 || bal != 1001013 || rem != 3200000 {
		t.Fatalf("第一次结息: got i=%d bal=%d R=%d, want i=1013 bal=1001013 R=3200000", i, bal, rem)
	}

	i, bal, rem, err = a.Settle(20)
	mustOK(t, err)
	if i != 1015 || bal != 1002028 || rem != 2897450 {
		t.Fatalf("第二次结息: got i=%d bal=%d R=%d, want i=1015 bal=1002028 R=2897450", i, bal, rem)
	}
	recs := a.Records()
	want := []Record{{Start: 0, Date: 10, I: 1013, R: 3200000}, {Start: 10, Date: 20, I: 1015, R: 2897450}}
	if !reflect.DeepEqual(recs, want) {
		t.Fatalf("结息记录: got %+v, want %+v", recs, want)
	}
}

// N 恰能被 D 整除时余数为 0。
func TestSettleExactDivision(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 3600000))
	mustOK(t, a.SetRate(0, 100))
	// N = 1 天 × 3600000 × 100 = 360000000 = 100 × D
	i, bal, rem, err := a.Settle(1)
	mustOK(t, err)
	if i != 100 || bal != 3600100 || rem != 0 {
		t.Fatalf("got i=%d bal=%d R=%d, want i=100 bal=3600100 R=0", i, bal, rem)
	}
}

// 覆盖天数恰为 3660 通过，3661 以参数非法拒绝。
func TestSettleSpanLimit(t *testing.T) {
	a := mustNew(t, 0)
	_, _, _, err := a.Settle(3660)
	mustOK(t, err)

	b := mustNew(t, 0)
	_, _, _, err = b.Settle(3661)
	mustErrKind(t, err, ErrInvalidArg)
	if b.Prev() != 0 || b.Balance() != 0 || b.Remainder() != 0 || len(b.Records()) != 0 {
		t.Fatalf("被拒绝的 Settle 改变了状态")
	}
}

// 利率在区间中途调整：SetRate 当日起即用新利率，前一日仍用旧利率。
func TestRateChangeMidInterval(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 1000000))
	mustOK(t, a.SetRate(0, 100))
	mustOK(t, a.SetRate(5, 200))
	// 第 0..4 天按 100，第 5..9 天按 200：N = 5×1e6×100 + 5×1e6×200 = 1.5e9
	i, _, rem, err := a.Settle(10)
	mustOK(t, err)
	// 1.5e9 / 3.6e6 = 416 余 2400000
	if i != 416 || rem != 2400000 {
		t.Fatalf("got i=%d R=%d, want i=416 R=2400000", i, rem)
	}
}

// 同日多次存取只按日终余额计。
func TestSameDayMultipleFlows(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 100))
	mustOK(t, a.Deposit(0, 200))
	mustOK(t, a.Withdraw(0, 50))
	mustOK(t, a.SetRate(0, 360))
	// 第 0 天日终余额 250：N = 250 × 360 = 90000
	i, bal, rem, err := a.Settle(1)
	mustOK(t, err)
	if i != 0 || rem != 90000 || bal != 250 {
		t.Fatalf("got i=%d bal=%d R=%d, want i=0 bal=250 R=90000", i, bal, rem)
	}
}

// 结息日当天的存款不计入本次而计入下次。
func TestDepositOnSettleDay(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 1000000))
	mustOK(t, a.SetRate(0, 360))
	mustOK(t, a.Deposit(10, 500000))
	// 覆盖第 0..9 天，第 10 天的存款不影响：N = 10 × 1e6 × 360 = 3.6e9
	i, bal, rem, err := a.Settle(10)
	mustOK(t, err)
	if i != 1000 || rem != 0 || bal != 1501000 {
		t.Fatalf("第一次: got i=%d bal=%d R=%d, want i=1000 bal=1501000 R=0", i, bal, rem)
	}
	// 下次结息覆盖第 10 天，日终余额含当日存款与已贷记利息
	i, bal, rem, err = a.Settle(11)
	mustOK(t, err)
	// N = 1 × 1501000 × 360 = 540360000 → i = 150, R = 360000
	if i != 150 || rem != 360000 || bal != 1501150 {
		t.Fatalf("第二次: got i=%d bal=%d R=%d, want i=150 bal=1501150 R=360000", i, bal, rem)
	}
}

// 取款至 0。
func TestWithdrawToZero(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 500))
	mustOK(t, a.Withdraw(1, 500))
	if a.Balance() != 0 {
		t.Fatalf("余额应为 0，实际 %d", a.Balance())
	}
	mustErrKind(t, a.Withdraw(2, 1), ErrInsufficient)
}

// 时序倒退、空区间、区间未结息必须可区分。
func TestErrorKindDistinction(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(5, 100))
	mustErrKind(t, a.Deposit(3, 100), ErrOutOfOrder) // 时序倒退

	_, _, _, err := a.Settle(10)
	mustOK(t, err)
	_, _, _, err = a.Settle(10)
	mustErrKind(t, err, ErrEmptyInterval) // d == prev，空区间
	_, _, _, err = a.Settle(9)
	mustErrKind(t, err, ErrOutOfOrder) // d < m，时序倒退优先于空区间

	// 区间未结息：d >= prev
	_, _, _, err = a.Correct(10, 100)
	mustErrKind(t, err, ErrNotSettled)
	_, _, _, err = a.Correct(15, 100)
	mustErrKind(t, err, ErrNotSettled)

	// 参数非法优先于时序倒退：覆盖天数超 3660 且 d < m
	b := mustNew(t, 0)
	mustOK(t, b.Deposit(5000, 1))
	_, _, _, err = b.Settle(4999)
	mustErrKind(t, err, ErrInvalidArg)
}

// 冲正题目示例：变更列表、新余额、新余数与总积数守恒。
func TestCorrectExample(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 1000000))
	mustOK(t, a.SetRate(0, 365))
	_, _, _, err := a.Settle(10)
	mustOK(t, err)
	_, _, _, err = a.Settle(20)
	mustOK(t, err)

	bal, rem, changes, err := a.Correct(5, 1000000)
	mustOK(t, err)
	if bal != 2003550 || rem != 548000 {
		t.Fatalf("got bal=%d R=%d, want bal=2003550 R=548000", bal, rem)
	}
	want := []Change{
		{Date: 10, OldI: 1013, NewI: 1520, OldR: 3200000, NewR: 3000000},
		{Date: 20, OldI: 1015, NewI: 2030, OldR: 2897450, NewR: 548000},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("变更列表: got %+v, want %+v", changes, want)
	}
	// 总积数守恒：5475000000 + 7305548000 = 3550 × D + 548000
	total := int64(5475000000 + 7305548000)
	if total != 3550*D+548000 {
		t.Fatalf("总积数校验失败")
	}
	var sumI int64
	for _, r := range a.Records() {
		sumI += r.I
	}
	if sumI != 3550 || sumI*D+rem != total {
		t.Fatalf("已结息总额 %d 与余数 %d 不满足积数守恒", sumI, rem)
	}
}

// 冲正日恰等于某条记录的结息日：该记录不受影响；恰为其前一天：受影响。
func TestCorrectBoundaryDate(t *testing.T) {
	build := func(t *testing.T) *Account {
		a := mustNew(t, 0)
		mustOK(t, a.Deposit(0, 1000000))
		mustOK(t, a.SetRate(0, 365))
		_, _, _, err := a.Settle(10)
		mustOK(t, err)
		_, _, _, err = a.Settle(20)
		mustOK(t, err)
		return a
	}

	// d = 10 恰为第一条记录的结息日：第一条不受影响，只重算第二条
	a := build(t)
	bal, _, changes, err := a.Correct(10, 1000000)
	mustOK(t, err)
	if len(changes) != 1 || changes[0].Date != 20 {
		t.Fatalf("d=10 应只影响结息日 20 的记录: got %+v", changes)
	}
	recs := a.Records()
	if recs[0].I != 1013 || recs[0].R != 3200000 {
		t.Fatalf("第一条记录不应变化: got %+v", recs[0])
	}
	// 第二条：第 10..19 天日终余额 1001013+1000000，N = 3200000 + 10×2001013×365
	// = 3200000 + 7303697450 = 7306897450 → i = 2029, R = 2497450
	if recs[1].I != 2029 || recs[1].R != 2497450 {
		t.Fatalf("第二条记录: got %+v, want I=2029 R=2497450", recs[1])
	}
	if bal != 1000000+1000000+1013+2029 {
		t.Fatalf("余额: got %d", bal)
	}

	// d = 9 为第一条记录覆盖的最后一天：两条都受影响
	b := build(t)
	_, _, changes, err = b.Correct(9, 1000000)
	mustOK(t, err)
	if len(changes) != 2 || changes[0].Date != 10 || changes[1].Date != 20 {
		t.Fatalf("d=9 应影响两条记录: got %+v", changes)
	}
}

// 负的 delta 使某日日终余额恰为 0 通过，为 -1 被拒。
func TestCorrectNegativeDelta(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 100))
	_, _, _, err := a.Settle(10)
	mustOK(t, err)

	// 第 0 天起余额恰为 0：通过，且利息不变（B_k 全为 0，r = 0）
	bal, rem, changes, err := a.Correct(0, -100)
	mustOK(t, err)
	if bal != 0 || rem != 0 || len(changes) != 0 {
		t.Fatalf("got bal=%d R=%d changes=%+v, want bal=0 R=0 无变更", bal, rem, changes)
	}

	// 再冲 -1：第 0..9 天日终余额为 -1，整个冲正不生效
	snapBal, snapRem, snapRecs := a.Balance(), a.Remainder(), a.Records()
	_, _, _, err = a.Correct(0, -1)
	mustErrKind(t, err, ErrCorrectNegative)
	if a.Balance() != snapBal || a.Remainder() != snapRem || !reflect.DeepEqual(a.Records(), snapRecs) {
		t.Fatalf("被拒绝的冲正改变了状态")
	}
}

// 冲正使利息不变但余数改变的记录仍出现在列表；i 与 R 均不变的不出现。
func TestCorrectChangeListRules(t *testing.T) {
	// 利息不变（仍为 0）但余数改变 → 出现在列表
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 10000))
	mustOK(t, a.SetRate(0, 1))
	_, _, _, err := a.Settle(1) // N = 10000，i = 0，R = 10000
	mustOK(t, err)
	_, rem, changes, err := a.Correct(0, 10000)
	mustOK(t, err)
	// N = 20000，i = 0 不变，R = 20000 改变
	want := []Change{{Date: 1, OldI: 0, NewI: 0, OldR: 10000, NewR: 20000}}
	if !reflect.DeepEqual(changes, want) || rem != 20000 {
		t.Fatalf("got changes=%+v R=%d, want %+v R=20000", changes, rem, want)
	}

	// 受影响区间内利率为 0 → 重算结果不变 → 不出现在列表
	b := mustNew(t, 0)
	mustOK(t, b.Deposit(0, 1000))
	mustOK(t, b.SetRate(0, 5))
	_, _, _, err = b.Settle(10) // N = 10×1000×5 = 50000，i = 0，R = 50000
	mustOK(t, err)
	mustOK(t, b.SetRate(10, 0))
	_, _, _, err = b.Settle(20) // 第 10..19 天 r = 0，N = 50000，i = 0，R = 50000
	mustOK(t, err)
	_, rem, changes, err = b.Correct(15, 5000)
	mustOK(t, err)
	if len(changes) != 0 || rem != 50000 {
		t.Fatalf("利率为 0 的区间重算不变，应无变更: got %+v R=%d", changes, rem)
	}
}

// 被拒绝的操作不得改变余额、利率、prev、余数、m 与任何结息记录。
func TestRejectedOpsKeepState(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 1000000))
	mustOK(t, a.SetRate(0, 365))
	_, _, _, err := a.Settle(10)
	mustOK(t, err)

	type state struct {
		bal, rem, prev int64
		recs           []Record
	}
	snap := func() state { return state{a.Balance(), a.Remainder(), a.Prev(), a.Records()} }
	before := snap()
	check := func() {
		t.Helper()
		after := snap()
		if after.bal != before.bal || after.rem != before.rem || after.prev != before.prev ||
			!reflect.DeepEqual(after.recs, before.recs) {
			t.Fatalf("被拒绝的操作改变了状态: before=%+v after=%+v", before, after)
		}
	}

	mustErrKind(t, a.Deposit(11, 0), ErrInvalidArg)               // x 越界
	mustErrKind(t, a.Deposit(11, 1000000001), ErrInvalidArg)      // x 越界
	mustErrKind(t, a.Deposit(-1, 100), ErrInvalidArg)             // d 越界
	mustErrKind(t, a.Deposit(9, 100), ErrOutOfOrder)              // 时序倒退
	mustErrKind(t, a.Deposit(10, 1000000000), ErrBalanceOverflow) // 1001013 + 1e9 > 1e9
	mustErrKind(t, a.Withdraw(10, 1001014), ErrInsufficient)      // 超过当前余额
	mustErrKind(t, a.SetRate(10, 10001), ErrInvalidArg)           // r 越界
	_, _, _, err = a.Settle(10)
	mustErrKind(t, err, ErrEmptyInterval) // 空区间
	_, _, _, err = a.Settle(4000)
	mustErrKind(t, err, ErrInvalidArg) // 覆盖 3990 天 > 3660
	_, _, _, err = a.Correct(0, 0)
	mustErrKind(t, err, ErrInvalidArg) // delta 为 0
	_, _, _, err = a.Correct(0, 1000000001)
	mustErrKind(t, err, ErrInvalidArg) // |delta| 越界
	_, _, _, err = a.Correct(10, 100)
	mustErrKind(t, err, ErrNotSettled) // d >= prev
	_, _, _, err = a.Correct(0, -2000000)
	mustErrKind(t, err, ErrCorrectNegative) // 冲正后余额为负
	check()
}

// 并发调用：结果等价于某个串行顺序，不变式始终成立。
func TestConcurrent(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 900000000))
	mustOK(t, a.SetRate(0, 365))
	_, _, _, err := a.Settle(100)
	mustOK(t, err)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				d := int64(100 + k)
				switch k % 4 {
				case 0:
					_ = a.Deposit(d, 1)
				case 1:
					_ = a.Withdraw(d, 1)
				case 2:
					_, _, _, _ = a.Settle(d + 1)
				case 3:
					_, _, _, _ = a.Correct(int64(g), 1)
				}
				_ = a.Balance()
				_ = a.Remainder()
				_ = a.Records()
			}
		}(g)
	}
	wg.Wait()

	// 串行不变式：余额 = 净额 + 已贷记利息；0 <= R < D
	recs := a.Records()
	var sumI int64
	for _, r := range recs {
		sumI += r.I
	}
	if a.Remainder() < 0 || a.Remainder() >= D {
		t.Fatalf("余数 %d 不在 [0, D) 内", a.Remainder())
	}
	if len(recs) > 0 && recs[len(recs)-1].R != a.Remainder() {
		t.Fatalf("余数与最后一条结息记录不一致")
	}
	if len(recs) > 0 && recs[len(recs)-1].Date != a.Prev() {
		t.Fatalf("prev 与最后一条结息记录不一致")
	}
	t.Logf("并发结束: balance=%d R=%d 已贷记利息=%d 记录数=%d", a.Balance(), a.Remainder(), sumI, len(recs))
}
