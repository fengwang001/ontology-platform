package margin

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, i, m, f int) *Engine {
	t.Helper()
	e, err := New(i, m, f)
	if err != nil {
		t.Fatalf("New(%d,%d,%d) error: %v", i, m, f, err)
	}
	return e
}

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func wantErr(t *testing.T, err, target error, ctx string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: want %v, got %v", ctx, target, err)
	}
}

func wantState(t *testing.T, e *Engine, a string, m, q, c int64) {
	t.Helper()
	got, err := e.AccountSnapshot(a)
	if err != nil {
		t.Fatalf("snapshot %s: %v", a, err)
	}
	want := Account{M: m, Q: q, C: c}
	if got != want {
		t.Fatalf("account %s state: want %+v, got %+v", a, want, got)
	}
}

func TestConstructorValidation(t *testing.T) {
	for _, c := range [][3]int{
		{500, 500, 100}, {500, 501, 100}, {10001, 500, 100},
		{0, 0, 100}, {1000, 500, -1}, {1000, 500, 10001},
	} {
		if _, err := New(c[0], c[1], c[2]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%v) want ErrInvalidArgument, got %v", c, err)
		}
	}
	for _, c := range [][3]int{{10000, 1, 0}, {2, 1, 10000}} {
		if _, err := New(c[0], c[1], c[2]); err != nil {
			t.Fatalf("New(%v) unexpected error: %v", c, err)
		}
	}
}

func TestInitialMarginExactAndShortByOne(t *testing.T) {
	e := mustNew(t, 1000, 500, 100)
	mustOK(t, e.Deposit("A", 100), "deposit 100")
	mustOK(t, e.Open("A", Long, 10, 100), "open exact")
	wantState(t, e, "A", 100, 10, 1000)

	e2 := mustNew(t, 1000, 500, 100)
	mustOK(t, e2.Deposit("B", 99), "deposit 99")
	wantErr(t, e2.Open("B", Long, 10, 100), ErrInsufficientFunds, "short by one")
	wantState(t, e2, "B", 99, 0, 0)

	e3 := mustNew(t, 1000, 500, 0)
	mustOK(t, e3.Deposit("A", 200), "deposit")
	mustOK(t, e3.Open("A", Long, 10, 100), "open 10")
	mustOK(t, e3.Open("A", Long, 10, 100), "open another 10")
	wantState(t, e3, "A", 200, 20, 2000)
}

func TestSpecExampleLong(t *testing.T) {
	e := mustNew(t, 1000, 500, 100)
	mustOK(t, e.Deposit("A", 100), "deposit")
	mustOK(t, e.Open("A", Long, 10, 100), "open")

	items, err := e.Mark(95)
	mustOK(t, err, "mark 95")
	if len(items) != 0 {
		t.Fatalf("Mark(95): want no liquidation, got %v", items)
	}
	items, err = e.Mark(94)
	mustOK(t, err, "mark 94")
	want := LiquidationItem{Account: "A", E: 40, Fine: 9, ADL: []ADLItem{}}
	if len(items) != 1 || !sameItem(items[0], want) {
		t.Fatalf("Mark(94): want %+v, got %+v", want, items)
	}
	wantState(t, e, "A", 31, 0, 0)
	if z := e.InsuranceFund(); z != 9 {
		t.Fatalf("Z want 9, got %d", z)
	}
}

// E 恰等于 R 不强平，小 1 强平；R 向上取整（14.85 → 15）。
func TestMaintenanceBoundary(t *testing.T) {
	e := mustNew(t, 501, 500, 0)
	mustOK(t, e.Deposit("EQ", 18), "deposit EQ")
	mustOK(t, e.Open("EQ", Long, 3, 100), "open EQ")
	mustOK(t, e.Deposit("LT", 17), "deposit LT")
	mustOK(t, e.Open("LT", Long, 3, 100), "open LT")

	items, _ := e.Mark(99)
	if len(items) != 1 || items[0].Account != "LT" || items[0].E != 14 {
		t.Fatalf("only LT (E=R-1) should liquidate, got %v", items)
	}
	wantState(t, e, "EQ", 18, 3, 300)
	wantState(t, e, "LT", 14, 0, 0)
}

// 空头强平、罚金向下取整、反向开仓为持仓冲突。
func TestShortLiquidation(t *testing.T) {
	e := mustNew(t, 2000, 500, 100)
	mustOK(t, e.Deposit("S", 200), "deposit")
	mustOK(t, e.Open("S", Short, 10, 100), "short open")
	wantErr(t, e.Open("S", Long, 1, 100), ErrPositionConflict, "reverse open")

	if items, _ := e.Mark(110); len(items) != 0 {
		t.Fatalf("Mark(110) should not liquidate, got %v", items)
	}
	items, _ := e.Mark(118)
	if len(items) != 1 || items[0].E != 20 || items[0].Fine != 11 {
		t.Fatalf("Mark(118) unexpected %v (fine floor(11.8)=11)", items)
	}
	wantState(t, e, "S", 9, 0, 0)
}

// 罚金大于 E 时只扣 E。
func TestFineCappedByEquity(t *testing.T) {
	e := mustNew(t, 1050, 1045, 10000)
	mustOK(t, e.Deposit("B", 105), "deposit b")
	mustOK(t, e.Open("B", Long, 1, 1000), "open b")
	items, _ := e.Mark(999)
	if len(items) != 1 || items[0].Fine != 104 {
		t.Fatalf("fine should be capped by E, got %v", items)
	}
	wantState(t, e, "B", 0, 0, 0)
	if z := e.InsuranceFund(); z != 104 {
		t.Fatalf("Z want 104, got %d", z)
	}
}

// 题面第二例：罚金→Z→ADL 时序、盈利率排序、取走改 C。
func TestSpecExampleADL(t *testing.T) {
	e := mustNew(t, 1000, 500, 100)
	mustOK(t, e.Deposit("L1", 85), "")
	mustOK(t, e.Open("L1", Long, 10, 85), "")
	mustOK(t, e.Deposit("L2", 100), "")
	mustOK(t, e.Open("L2", Long, 10, 100), "")
	mustOK(t, e.Deposit("S1", 100), "")
	mustOK(t, e.Open("S1", Short, 10, 100), "")
	mustOK(t, e.Deposit("S2", 20), "")
	mustOK(t, e.Open("S2", Short, 1, 200), "")

	items, err := e.Mark(80)
	mustOK(t, err, "mark")
	want := []LiquidationItem{
		{Account: "L1", E: 35, Fine: 8, ADL: []ADLItem{}},
		{Account: "L2", E: -100, Absorbed: 8, ADL: []ADLItem{{Account: "S2", Take: 92}}},
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %v", items)
	}
	for i := range want {
		if !sameItem(items[i], want[i]) {
			t.Fatalf("item %d want %+v got %+v", i, want[i], items[i])
		}
	}
	wantState(t, e, "L1", 27, 0, 0)
	wantState(t, e, "L2", 0, 0, 0)
	wantState(t, e, "S1", 100, -10, -1000)
	wantState(t, e, "S2", 20, -1, -108)
	if e.InsuranceFund() != 0 || e.BadDebt() != 0 {
		t.Fatalf("Z=%d B=%d", e.InsuranceFund(), e.BadDebt())
	}
}

// 盈利率相等按编号字节序；pnl 恰为 0 不入候选。
func TestADLTieAndZeroPnl(t *testing.T) {
	e := mustNew(t, 1000, 100, 0)
	mustOK(t, e.Deposit("L", 100), "")
	mustOK(t, e.Open("L", Long, 10, 100), "")
	mustOK(t, e.Deposit("B1", 1000), "")
	mustOK(t, e.Open("B1", Short, 10, 100), "")
	mustOK(t, e.Deposit("B2", 1000), "")
	mustOK(t, e.Open("B2", Short, 10, 100), "")
	mustOK(t, e.Deposit("Z0", 1000), "")
	mustOK(t, e.Open("Z0", Short, 10, 80), "")

	items, _ := e.Mark(80)
	wantItem := LiquidationItem{
		Account: "L", E: -100,
		ADL: []ADLItem{{Account: "B1", Take: 100}},
	}
	if len(items) != 1 || !sameItem(items[0], wantItem) {
		t.Fatalf("want %+v, got %+v", wantItem, items)
	}
	wantState(t, e, "B1", 1000, -10, -900)
	wantState(t, e, "B2", 1000, -10, -1000)
	wantState(t, e, "Z0", 1000, -10, -800)
}

// Z 时序：亏空账户在前、罚金账户在后时，后者的罚金不能吸收前者的亏空。
func TestInsuranceFundOrdering(t *testing.T) {
	e := mustNew(t, 1000, 900, 10000)
	// 亏空多头 A（编号小，先执行）：10@100, M=100, P=80 → E=-100。
	mustOK(t, e.Deposit("A", 100), "")
	mustOK(t, e.Open("A", Long, 10, 100), "")
	// 罚金多头 B（编号大，后执行）：1@100, M=27 → E=7, R=ceil(7.2)=8，入选且 E≥0。
	mustOK(t, e.Deposit("B", 27), "")
	mustOK(t, e.Open("B", Long, 1, 100), "")
	// 无对手方：A 的 100 亏空全部记 B；随后 B 的罚金 8 进入 Z。
	items, _ := e.Mark(80)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %v", items)
	}
	if !(items[0].Account == "A" && items[0].E == -100 && items[0].Absorbed == 0 &&
		items[0].BadDebt == 100 && len(items[0].ADL) == 0) {
		t.Fatalf("A item wrong: %+v", items[0])
	}
	if !(items[1].Account == "B" && items[1].E == 7 && items[1].Fine == 7) {
		t.Fatalf("B item wrong: %+v", items[1])
	}
	if e.InsuranceFund() != 7 || e.BadDebt() != 100 {
		t.Fatalf("Z want 7, B want 100; got Z=%d B=%d", e.InsuranceFund(), e.BadDebt())
	}
}

// Λ 一次定出：减仓只作用于非 Λ 对手方，且本次 Mark 不会新增强平对象。
func TestLambdaFixedDuringMark(t *testing.T) {
	e := mustNew(t, 1000, 500, 0)
	mustOK(t, e.Deposit("L", 100), "")
	mustOK(t, e.Open("L", Long, 10, 100), "")
	mustOK(t, e.Deposit("S", 90), "")
	mustOK(t, e.Open("S", Short, 10, 90), "")
	items, _ := e.Mark(80)
	if len(items) != 1 || items[0].Account != "L" {
		t.Fatalf("only L should be liquidated, got %v", items)
	}
	wantState(t, e, "S", 90, -10, -800)

	// 大单场景：取走全部浮盈后 S 的权益等于初始 M（恒 ≥ R），仍持仓；
	// 关键点是 Λ 中不出现 S，且取走明细完整。
	e2 := mustNew(t, 1000, 500, 0)
	mustOK(t, e2.Deposit("L", 1000), "")
	mustOK(t, e2.Open("L", Long, 10, 1000), "")
	mustOK(t, e2.Deposit("S", 5000), "")
	mustOK(t, e2.Open("S", Short, 10, 500), "")
	items2, _ := e2.Mark(80)
	if len(items2) != 1 || len(items2[0].ADL) != 1 ||
		items2[0].ADL[0] != (ADLItem{Account: "S", Take: 4200}) {
		t.Fatalf("unexpected items %+v", items2)
	}
	wantState(t, e2, "S", 5000, -10, -800)
}

// 候选用尽后剩余记入 B；E<0 时 f=0 且先由 Z 吸收。
func TestExhaustCandidatesBadDebt(t *testing.T) {
	e := mustNew(t, 1000, 500, 10000)
	// P=89: AF E=4, R=ceil(89*500/10000)=ceil(4.45)=5 → 入选，f=min(4,89)=4。
	mustOK(t, e.Deposit("AF", 15), "")
	mustOK(t, e.Open("AF", Long, 1, 100), "")
	seed, _ := e.Mark(89)
	if len(seed) != 1 || seed[0].Fine != 4 || e.InsuranceFund() != 4 {
		t.Fatalf("seed wrong: %v Z=%d", seed, e.InsuranceFund())
	}
	// 亏空账户 L：10@100, M=100 → P=80 E=-100。
	mustOK(t, e.Deposit("L", 100), "")
	mustOK(t, e.Open("L", Long, 10, 100), "")
	// 唯一盈利对手 S：4@90（空头开仓价高于标记价才浮盈），M=100 → E=140, R=16，pnl=40。
	mustOK(t, e.Deposit("S", 100), "")
	mustOK(t, e.Open("S", Short, 4, 90), "")

	items, _ := e.Mark(80)
	if len(items) != 1 {
		t.Fatalf("want only L liquidation, got %v", items)
	}
	// L: d=100, u=4, s=96；S 取 40 后用尽，余 56 记 B。
	lItem := items[0]
	wantL := LiquidationItem{
		Account: "L", E: -100, Absorbed: 4, BadDebt: 56,
		ADL: []ADLItem{{Account: "S", Take: 40}},
	}
	if !sameItem(lItem, wantL) {
		t.Fatalf("L want %+v, got %+v", wantL, lItem)
	}
	wantState(t, e, "S", 100, -4, -320) // -360+40
	if e.InsuranceFund() != 0 || e.BadDebt() != 56 {
		t.Fatalf("Z want 0 B want 56, got Z=%d B=%d", e.InsuranceFund(), e.BadDebt())
	}
}

// 两个亏空账户先后触发减仓时，第二次按改写后的 C 重新排序。
func TestADLReSortAfterFirstDeficit(t *testing.T) {
	// 初始（P=80）：S1: 10@100 pnl=200,|C|=1000 率0.2；S2: 1@200 pnl=120,|C|=200 率0.6。
	// 罚金账户 AF 先充 Z=5；L2 亏空 100 先吸收 5，s=95 取 S2 95 后，
	// S2: pnl=25,|C|=105 率≈0.238 仍高于 S1 的 0.2；L3 亏空 50 重排，
	// 取 S2 25 再取 S1 25。
	e := mustNew(t, 1000, 900, 10000)
	mustOK(t, e.Deposit("AF", 25), "")
	mustOK(t, e.Open("AF", Long, 1, 100), "") // P=80: E=5, R=4, f=min(5,80)=5
	mustOK(t, e.Deposit("L2", 100), "")
	mustOK(t, e.Open("L2", Long, 10, 100), "") // E=-100
	mustOK(t, e.Deposit("L3", 50), "")
	mustOK(t, e.Open("L3", Long, 5, 100), "") // C=500 要求 50；P=80 → E=-50
	mustOK(t, e.Deposit("S1", 1000), "")
	mustOK(t, e.Open("S1", Short, 10, 100), "")
	mustOK(t, e.Deposit("S2", 20), "")
	mustOK(t, e.Open("S2", Short, 1, 200), "")

	items, _ := e.Mark(80)
	if len(items) != 3 {
		t.Fatalf("want AF,L2,L3, got %v", items)
	}
	want0 := LiquidationItem{Account: "L2", E: -100, Absorbed: 5,
		ADL: []ADLItem{{Account: "S2", Take: 95}}}
	want1 := LiquidationItem{Account: "L3", E: -50,
		ADL: []ADLItem{{Account: "S2", Take: 25}, {Account: "S1", Take: 25}}}
	if !sameItem(items[1], want0) || !sameItem(items[2], want1) {
		t.Fatalf("want\n%+v\n%+v\ngot\n%+v\n%+v", want0, want1, items[1], items[2])
	}
	wantState(t, e, "S2", 20, -1, -80) // -200+95+25
	wantState(t, e, "S1", 1000, -10, -975)
}

// Close 盈利 M=E；亏损先由 Z 吸收再记 B，不触发减仓，仓位清零。
func TestCloseProfitAndLoss(t *testing.T) {
	e := mustNew(t, 1000, 500, 0)
	mustOK(t, e.Deposit("L", 100), "")
	mustOK(t, e.Open("L", Long, 10, 100), "")
	mustOK(t, e.Deposit("W", 100), "")
	mustOK(t, e.Open("W", Short, 10, 100), "") // 有盈利对手方也不触发 ADL

	// Close 亏损：P=80 → E=-100，Z=0 → u=0, B=100。
	if err := e.Close("L", 80); err != nil {
		t.Fatalf("close L: %v", err)
	}
	wantState(t, e, "L", 0, 0, 0)
	if e.BadDebt() != 100 || e.InsuranceFund() != 0 {
		t.Fatalf("B want 100 Z want 0, got %d %d", e.BadDebt(), e.InsuranceFund())
	}

	// 先给 Z 充值，再 Close 亏损由 Z 部分吸收。
	// W 在 P=120: E = 100 - 1200 + 1000 = -100。用罚金账户充值不便，
	// 直接再做一次带罚金的 Mark 建 Z。
	e2 := mustNew(t, 1000, 900, 100)
	mustOK(t, e2.Deposit("F", 27), "")
	mustOK(t, e2.Open("F", Long, 1, 100), "") // P=80: E=7, R=8 入选，f=floor(0.8)=0
	// f=0 无法充值 Z；改用 F=10000 → f=7。
	e2 = mustNew(t, 1000, 900, 10000)
	mustOK(t, e2.Deposit("F", 27), "")
	mustOK(t, e2.Open("F", Long, 1, 100), "")
	if items, _ := e2.Mark(80); len(items) != 1 || items[0].Fine != 7 {
		t.Fatalf("seed fine setup wrong: %v", items)
	}
	if e2.InsuranceFund() != 7 {
		t.Fatalf("seed Z want 7")
	}
	mustOK(t, e2.Deposit("L", 100), "")
	mustOK(t, e2.Open("L", Long, 10, 100), "")
	mustOK(t, e2.Close("L", 80), "") // E=-100：Z 吸收 7，B 记 93。
	if z, b := e2.InsuranceFund(), e2.BadDebt(); z != 0 || b != 93 {
		t.Fatalf("after close Z want 0 B want 93, got %d %d", z, b)
	}

	// Close 盈利：W 保留，P=80 → E=300。
	mustOK(t, e.Close("W", 80), "close W profit")
	wantState(t, e, "W", 300, 0, 0)
}

// Withdraw 仅无仓位时可用；各种拒绝原因区分与首因顺序。
func TestWithdrawAndErrors(t *testing.T) {
	e := mustNew(t, 1000, 500, 0)
	wantErr(t, e.Withdraw("ghost", 1), ErrAccountNotFound, "no account")
	mustOK(t, e.Deposit("A", 100), "")
	mustOK(t, e.Open("A", Long, 1, 100), "")
	wantErr(t, e.Withdraw("A", 1), ErrPositionConflict, "withdraw position")
	wantErr(t, e.Close("ghost", 100), ErrAccountNotFound, "close no account")
	wantErr(t, e.Withdraw("", 1), ErrInvalidArgument, "empty id")

	e2 := mustNew(t, 1000, 500, 0)
	mustOK(t, e2.Deposit("B", 50), "")
	wantErr(t, e2.Withdraw("B", 51), ErrInsufficientFunds, "over withdraw")
	mustOK(t, e2.Withdraw("B", 50), "withdraw all")
	wantErr(t, e2.Close("B", 100), ErrNoPosition, "close flat")
	wantErr(t, e2.Open("B", Direction(0), 1, 100), ErrInvalidArgument, "bad dir")
	wantErr(t, e2.Open("B", Long, 0, 100), ErrInvalidArgument, "n=0")
	wantErr(t, e2.Open("B", Long, 1, 0), ErrInvalidArgument, "p=0")
	wantErr(t, e2.Deposit("B", 0), ErrInvalidArgument, "x=0")
	if _, err := e2.Mark(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Mark(0) want invalid, got %v", err)
	}
	if mp := e2.MarkPrice(); mp != 0 {
		t.Fatalf("no successful mark yet, got %d", mp)
	}
	// 非法开仓不改变持仓上限校验：超额 |q|。
	mustOK(t, e2.Deposit("C", 1_000_000_000_000), "")
	mustOK(t, e2.Open("C", Long, 1_000_000, 1_000_000), "max qty open")
	wantErr(t, e2.Open("C", Long, 1, 1), ErrInvalidArgument, "qty overflow")
}

// 被拒绝的操作不改变任何状态。
func TestRejectedOpsDoNotMutate(t *testing.T) {
	e := mustNew(t, 1000, 500, 100)
	mustOK(t, e.Deposit("A", 100), "")
	mustOK(t, e.Open("A", Long, 10, 100), "")
	snapBefore, _ := e.AccountSnapshot("A")
	zBefore, bBefore := e.InsuranceFund(), e.BadDebt()

	_ = e.Open("A", Short, 1, 100)
	_ = e.Open("A", Long, 10, 100) // 保证金不足
	_ = e.Withdraw("A", 1)
	_ = e.Close("A", 0)
	_, _ = e.Mark(1_000_001)

	snapAfter, _ := e.AccountSnapshot("A")
	if snapAfter != snapBefore || e.InsuranceFund() != zBefore || e.BadDebt() != bBefore {
		t.Fatalf("state changed by rejected ops: before %+v after %+v", snapBefore, snapAfter)
	}
}
