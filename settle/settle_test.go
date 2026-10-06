package settle_test

import (
	"errors"
	"testing"

	"ontology/instr"
	"ontology/settle"
)

func newBook(t *testing.T, U, rs, rb int64, A int) *instr.Book {
	t.Helper()
	b, err := instr.New(U, rs, rb, A)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func acct(st instr.State, name string) instr.AccountView {
	return st.Accounts[name]
}

func findIns(t *testing.T, st instr.State, id string) instr.Instruction {
	t.Helper()
	for _, ins := range st.Instructions {
		if ins.ID == id {
			return ins
		}
	}
	t.Fatalf("instruction %s not found", id)
	return instr.Instruction{}
}

// exampleBook 构造题目示例：U=100, rs=10, rb=5, A=2, 券价 11。
func exampleBook(t *testing.T) *instr.Book {
	t.Helper()
	b := newBook(t, 100, 10, 5, 2)
	must(t, b.SetPrice(0, "X", 11))
	must(t, b.Credit(0, "S", "X", 450))
	must(t, b.CreditCash(0, "B", 20_000))
	must(t, b.CreditCash(0, "C", 3_000))
	must(t, b.Instruct(0, "i1", "S", "B", "X", 1000, 10_005, 2))
	must(t, b.Instruct(0, "i2", "B", "C", "X", 600, 6_100, 2))
	return b
}

// TestSpecExample 逐日核对题目示例：部分交付、罚金向上取整、
// 链式使用当批到货、双方同时有责、日龄恰等 A 强制买入。
func TestSpecExample(t *testing.T) {
	b := exampleBook(t)

	// RunSettle(2)：i1 交 400 付 4002，S 罚 7；i2 交 200 付 2033，
	// B 罚 5、C 罚 3。
	must(t, settle.Run(b, 2))
	st := b.Snapshot()
	i1 := findIns(t, st, "i1")
	i2 := findIns(t, st, "i2")
	if i1.Delivered != 400 || i1.Paid != 4002 || i1.Status != instr.Open {
		t.Fatalf("day2 i1 = %+v", i1)
	}
	if i2.Delivered != 200 || i2.Paid != 2033 || i2.Status != instr.Open {
		t.Fatalf("day2 i2 = %+v", i2)
	}
	if got := acct(st, "S"); got.Holdings["X"] != 50 || got.Cash != 4002 || got.Payable != 7 {
		t.Fatalf("day2 S = %+v", got)
	}
	if got := acct(st, "B"); got.Holdings["X"] != 200 || got.Cash != 18_031 || got.Payable != 5 {
		t.Fatalf("day2 B = %+v", got)
	}
	if got := acct(st, "C"); got.Holdings["X"] != 200 || got.Cash != 967 || got.Payable != 3 {
		t.Fatalf("day2 C = %+v", got)
	}

	// RunSettle(3)：两条指令均交付 0；S 再罚 7，B 再罚 5，C 再罚 3。
	must(t, settle.Run(b, 3))
	st = b.Snapshot()
	i1 = findIns(t, st, "i1")
	i2 = findIns(t, st, "i2")
	if i1.Delivered != 400 || i1.Paid != 4002 || i1.Status != instr.Open {
		t.Fatalf("day3 i1 = %+v", i1)
	}
	if i2.Delivered != 200 || i2.Paid != 2033 || i2.Status != instr.Open {
		t.Fatalf("day3 i2 = %+v", i2)
	}
	if got := acct(st, "S").Payable; got != 14 {
		t.Fatalf("day3 S payable = %d, want 14", got)
	}
	if got := acct(st, "B").Payable; got != 10 {
		t.Fatalf("day3 B payable = %d, want 10", got)
	}
	if got := acct(st, "C").Payable; got != 6 {
		t.Fatalf("day3 C payable = %d, want 6", got)
	}

	// RunSettle(4)：日龄恰为 A=2，再记罚金后强制买入；
	// i1 赔付 597，i2 赔付 333。
	must(t, settle.Run(b, 4))
	st = b.Snapshot()
	i1 = findIns(t, st, "i1")
	i2 = findIns(t, st, "i2")
	if i1.Status != instr.BoughtIn || i2.Status != instr.BoughtIn {
		t.Fatalf("day4 status: i1=%v i2=%v", i1.Status, i2.Status)
	}
	if got := acct(st, "S"); got.Payable != 21+597 || got.Receivable != 0 {
		t.Fatalf("day4 S = %+v, want payable 618", got)
	}
	if got := acct(st, "B"); got.Payable != 15+333 || got.Receivable != 597 {
		t.Fatalf("day4 B = %+v, want payable 348 receivable 597", got)
	}
	if got := acct(st, "C"); got.Payable != 9 || got.Receivable != 333 {
		t.Fatalf("day4 C = %+v, want payable 9 receivable 333", got)
	}
	// 罚金与赔付不动现金与持券。
	if got := acct(st, "S"); got.Holdings["X"] != 50 || got.Cash != 4002 {
		t.Fatalf("day4 S = %+v", got)
	}
	if got := acct(st, "C"); got.Holdings["X"] != 200 || got.Cash != 967 {
		t.Fatalf("day4 C = %+v", got)
	}
	if got := b.Touched(); got != 6 {
		t.Fatalf("touched = %d, want 6 (2 instructions x 3 days)", got)
	}
}

// TestBuyerMaxMultipleBoundary 买方可付量的最大倍数边界：差 1 分付不起。
func TestBuyerMaxMultipleBoundary(t *testing.T) {
	cases := []struct {
		name          string
		cash          int64
		wantDelivered int64
		wantPaid      int64
	}{
		// k=500 需付 floor(10000*500/1000)=5000；4999 差 1 分，只能交 400。
		{"one-cent-short", 4999, 400, 4000},
		{"exact", 5000, 500, 5000},
		{"plenty", 10_000, 1000, 10_000},
		{"zero-cash", 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBook(t, 100, 10, 5, 2)
			must(t, b.SetPrice(0, "X", 10))
			must(t, b.Credit(0, "S", "X", 1000))
			if c.cash > 0 {
				must(t, b.CreditCash(0, "B", c.cash))
			}
			must(t, b.Instruct(0, "i1", "S", "B", "X", 1000, 10_000, 1))
			must(t, settle.Run(b, 1))
			i1 := findIns(t, b.Snapshot(), "i1")
			if i1.Delivered != c.wantDelivered || i1.Paid != c.wantPaid {
				t.Fatalf("cash=%d: delivered=%d paid=%d, want %d/%d",
					c.cash, i1.Delivered, i1.Paid, c.wantDelivered, c.wantPaid)
			}
		})
	}
}

// TestFinalPaymentExact 最后一次交付恰好付清 amount：
// 按累计比例向下取整，尾笔补齐全部舍入差额。
func TestFinalPaymentExact(t *testing.T) {
	b := newBook(t, 100, 10, 5, 5)
	must(t, b.SetPrice(0, "X", 10))
	must(t, b.Credit(0, "S", "X", 100))
	must(t, b.CreditCash(0, "B", 1000))
	must(t, b.Instruct(0, "i1", "S", "B", "X", 300, 1000, 1))
	// day1：交 100，付 floor(1000*100/300)=333。
	must(t, settle.Run(b, 1))
	i1 := findIns(t, b.Snapshot(), "i1")
	if i1.Delivered != 100 || i1.Paid != 333 {
		t.Fatalf("day1 i1 = %+v, want 100/333", i1)
	}
	// day2：补足持券后交付剩余 200，付 1000-333=667，恰好付清。
	must(t, b.Credit(2, "S", "X", 200))
	must(t, settle.Run(b, 2))
	st := b.Snapshot()
	i1 = findIns(t, st, "i1")
	if i1.Status != instr.Settled || i1.Delivered != 300 || i1.Paid != 1000 {
		t.Fatalf("day2 i1 = %+v, want settled 300/1000", i1)
	}
	if got := acct(st, "S").Cash; got != 1000 {
		t.Fatalf("S cash = %d, want 1000", got)
	}
	if got := acct(st, "B").Cash; got != 0 {
		t.Fatalf("B cash = %d, want 0", got)
	}
}

// TestChainedNoRetry 排在后面的指令可以使用当批刚收到的券或款，
// 但已处理过的指令当日不回头重试。
func TestChainedNoRetry(t *testing.T) {
	// 正向链：i1 S->B、i2 B->C，B 用刚到账的 100 股转付 C。
	b := newBook(t, 100, 10, 5, 5)
	must(t, b.SetPrice(0, "X", 10))
	must(t, b.Credit(0, "S", "X", 100))
	must(t, b.CreditCash(0, "B", 100))
	must(t, b.CreditCash(0, "C", 100))
	must(t, b.Instruct(0, "i1", "S", "B", "X", 100, 100, 1))
	must(t, b.Instruct(0, "i2", "B", "C", "X", 100, 100, 1))
	must(t, settle.Run(b, 1))
	st := b.Snapshot()
	if got := acct(st, "C").Holdings["X"]; got != 100 {
		t.Fatalf("chain: C holdings = %d, want 100", got)
	}
	if findIns(t, st, "i2").Status != instr.Settled {
		t.Fatalf("chain: i2 should settle, got %v", findIns(t, st, "i2").Status)
	}

	// 反向链：i1 B->C 排在 i2 S->B 之前；i1 处理时 B 无券，当日不回头。
	b2 := newBook(t, 100, 10, 5, 5)
	must(t, b2.SetPrice(0, "X", 10))
	must(t, b2.Credit(0, "S", "X", 100))
	must(t, b2.CreditCash(0, "B", 100))
	must(t, b2.CreditCash(0, "C", 100))
	must(t, b2.Instruct(0, "i1", "B", "C", "X", 100, 100, 1))
	must(t, b2.Instruct(0, "i2", "S", "B", "X", 100, 100, 1))
	must(t, settle.Run(b2, 1))
	st2 := b2.Snapshot()
	i1 := findIns(t, st2, "i1")
	if i1.Delivered != 0 || i1.Status != instr.Open {
		t.Fatalf("no-retry: i1 = %+v, want delivered 0 open", i1)
	}
	if got := acct(st2, "C").Holdings["X"]; got != 0 {
		t.Fatalf("no-retry: C holdings = %d, want 0", got)
	}
	if got := acct(st2, "B").Holdings["X"]; got != 100 {
		t.Fatalf("no-retry: B holdings = %d, want 100", got)
	}
}

// TestBuyInAgeExact 日龄恰等 A 买入，小 1 不买入。
func TestBuyInAgeExact(t *testing.T) {
	b := newBook(t, 100, 10, 5, 2)
	must(t, b.SetPrice(0, "X", 11))
	must(t, b.CreditCash(0, "B", 10_000))
	must(t, b.Instruct(0, "i1", "S", "B", "X", 100, 1000, 1))
	// day2：日龄 1 < A=2，不买入。
	must(t, settle.Run(b, 2))
	if got := findIns(t, b.Snapshot(), "i1").Status; got != instr.Open {
		t.Fatalf("age A-1: status = %v, want open", got)
	}
	// day3：日龄 2 = A，卖方有责，强制买入。
	must(t, settle.Run(b, 3))
	if got := findIns(t, b.Snapshot(), "i1").Status; got != instr.BoughtIn {
		t.Fatalf("age A: status = %v, want bought-in", got)
	}
}

// TestBuyerOnlyFaultCancel 仅买方有责时逾期取消，无赔付。
func TestBuyerOnlyFaultCancel(t *testing.T) {
	b := newBook(t, 100, 10, 5, 2)
	must(t, b.SetPrice(0, "X", 10))
	must(t, b.Credit(0, "S", "X", 1000))
	must(t, b.Instruct(0, "i1", "S", "B", "X", 100, 1000, 1))
	// 买方现金为 0：三日均为仅买方有责，罚金 ceil(100*10*5/10000)=1。
	must(t, settle.Run(b, 1))
	must(t, settle.Run(b, 2))
	must(t, settle.Run(b, 3)) // 日龄 2 = A，取消
	st := b.Snapshot()
	i1 := findIns(t, st, "i1")
	if i1.Status != instr.Cancelled {
		t.Fatalf("status = %v, want cancelled", i1.Status)
	}
	if got := acct(st, "B"); got.Payable != 3 || got.Receivable != 0 {
		t.Fatalf("B = %+v, want payable 3 receivable 0", got)
	}
	if got := acct(st, "S"); got.Payable != 0 || got.Receivable != 0 {
		t.Fatalf("S = %+v, want no penalty no compensation", got)
	}
	if got := acct(st, "S").Holdings["X"]; got != 1000 {
		t.Fatalf("S holdings = %d, want 1000 (cancel returns nothing)", got)
	}
}

// TestZeroCompensation 强制买入时 amount-paid 已覆盖市值，赔付为 0。
func TestZeroCompensation(t *testing.T) {
	b := newBook(t, 100, 10, 5, 2)
	must(t, b.SetPrice(0, "X", 10))
	must(t, b.CreditCash(0, "B", 2000))
	must(t, b.Instruct(0, "i1", "S", "B", "X", 100, 1000, 1))
	// 卖方始终无券：三日卖方有责，罚金 ceil(100*10*10/10000)=1。
	must(t, settle.Run(b, 1))
	must(t, settle.Run(b, 2))
	must(t, settle.Run(b, 3))
	st := b.Snapshot()
	if got := findIns(t, st, "i1").Status; got != instr.BoughtIn {
		t.Fatalf("status = %v, want bought-in", got)
	}
	// 赔付 max(0, 100*10-(1000-0)) = 0：应付账只有罚金 3。
	if got := acct(st, "S"); got.Payable != 3 || got.Receivable != 0 {
		t.Fatalf("S = %+v, want payable 3 receivable 0", got)
	}
	if got := acct(st, "B").Receivable; got != 0 {
		t.Fatalf("B receivable = %d, want 0", got)
	}
}

// TestRunSettleErrors RunSettle 的参数/日期/状态校验。
func TestRunSettleErrors(t *testing.T) {
	b := newBook(t, 100, 10, 5, 2)
	if err := settle.Run(b, -1); !errors.Is(err, instr.ErrParam) {
		t.Fatalf("want ErrParam, got %v", err)
	}
	if err := settle.Run(b, 1_000_001); !errors.Is(err, instr.ErrParam) {
		t.Fatalf("want ErrParam, got %v", err)
	}
	must(t, settle.Run(b, 5))
	if err := settle.Run(b, 5); !errors.Is(err, instr.ErrState) {
		t.Fatalf("repeat day: want ErrState, got %v", err)
	}
	if err := settle.Run(b, 4); !errors.Is(err, instr.ErrDate) {
		t.Fatalf("rollback: want ErrDate, got %v", err)
	}
	// 被拒的 RunSettle 不消耗日期。
	must(t, settle.Run(b, 6))
}

// TestTouched 触碰的指令数等于 sd<=day 的未了结指令数，
// 与已了结指令数和未到期指令数无关（10 条与 10000 条两档对照）。
func TestTouched(t *testing.T) {
	build := func(t *testing.T, settled, notDue int) *instr.Book {
		b := newBook(t, 100, 10, 5, 2)
		must(t, b.SetPrice(0, "X", 10))
		must(t, b.Credit(0, "S", "X", int64(settled)*100))
		must(t, b.CreditCash(0, "B", int64(settled)*100))
		for i := 0; i < settled; i++ {
			must(t, b.Instruct(0, "st"+itoa(i), "S", "B", "X", 100, 100, 1))
		}
		for i := 0; i < notDue; i++ {
			must(t, b.Instruct(0, "nd"+itoa(i), "S", "B", "X", 100, 100, 1_000_000))
		}
		// 3 条到期未了结（卖方无券）。
		for i := 0; i < 3; i++ {
			must(t, b.Instruct(0, "due"+itoa(i), "T", "B", "X", 100, 100, 2))
		}
		must(t, settle.Run(b, 1)) // 了结 settled 条
		return b
	}
	small := build(t, 10, 10)
	large := build(t, 10_000, 10_000)
	beforeSmall, beforeLarge := small.Touched(), large.Touched()
	if beforeSmall != 10 || beforeLarge != 10_000 {
		t.Fatalf("day1 touched: small=%d large=%d", beforeSmall, beforeLarge)
	}
	must(t, settle.Run(small, 2))
	must(t, settle.Run(large, 2))
	if got := small.Touched() - beforeSmall; got != 3 {
		t.Fatalf("small day2 touched = %d, want 3", got)
	}
	if got := large.Touched() - beforeLarge; got != 3 {
		t.Fatalf("large day2 touched = %d, want 3", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
