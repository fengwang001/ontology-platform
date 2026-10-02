package margin

import (
	"errors"
	"reflect"
	"testing"
)

func mustEngine(t *testing.T, i, mm, f int64) *Engine {
	t.Helper()
	e, err := NewEngine(i, mm, f)
	if err != nil {
		t.Fatalf("NewEngine(%d, %d, %d): %v", i, mm, f, err)
	}
	return e
}

func mustDeposit(t *testing.T, e *Engine, id string, x int64) {
	t.Helper()
	if err := e.Deposit(id, x); err != nil {
		t.Fatalf("Deposit(%s, %d): %v", id, x, err)
	}
}

func mustOpen(t *testing.T, e *Engine, id string, dir Side, n, p int64) {
	t.Helper()
	if err := e.Open(id, dir, n, p); err != nil {
		t.Fatalf("Open(%s, %d, %d, %d): %v", id, dir, n, p, err)
	}
}

func mustMark(t *testing.T, e *Engine, p int64) []Liquidation {
	t.Helper()
	recs, err := e.Mark(p)
	if err != nil {
		t.Fatalf("Mark(%d): %v", p, err)
	}
	return recs
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got error %v, want %v", what, err, want)
	}
}

func checkAccount(t *testing.T, e *Engine, id string, m, q, c int64) {
	t.Helper()
	gm, gq, gc, ok := e.GetAccount(id)
	if !ok {
		t.Fatalf("account %s: not found", id)
	}
	if gm != m || gq != q || gc != c {
		t.Fatalf("account %s: got (M=%d, q=%d, C=%d), want (M=%d, q=%d, C=%d)",
			id, gm, gq, gc, m, q, c)
	}
}

func checkFund(t *testing.T, e *Engine, z, b int64) {
	t.Helper()
	if gz := e.InsuranceFund(); gz != z {
		t.Fatalf("insurance fund: got %d, want %d", gz, z)
	}
	if gb := e.BadDebt(); gb != b {
		t.Fatalf("bad debt: got %d, want %d", gb, b)
	}
}

func checkRecords(t *testing.T, got, want []Liquidation) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("liquidation records:\n got %+v\nwant %+v", got, want)
	}
}

// 开仓后 M 恰等于要求时通过，少 1 报资金不足且不改变状态。
func TestOpenMarginExactAndShortByOne(t *testing.T) {
	e := mustEngine(t, 1000, 500, 100)
	mustDeposit(t, e, "A", 100)
	// ceil(1000*1000/10000) = 100，恰等通过。
	mustOpen(t, e, "A", Long, 10, 100)
	checkAccount(t, e, "A", 100, 10, 1000)

	e2 := mustEngine(t, 1000, 500, 100)
	mustDeposit(t, e2, "A", 99)
	wantErr(t, "Open short by one", e2.Open("A", Long, 10, 100), ErrInsufficientFunds)
	checkAccount(t, e2, "A", 99, 0, 0)
}

// 规格示例一：R 向上取整（ceil(47.5)=48），E=50 不强平；
// E=40 < R=47 强平，罚金向下取整 floor(940*100/10000)=9。
func TestSpecExampleLong(t *testing.T) {
	e := mustEngine(t, 1000, 500, 100)
	mustDeposit(t, e, "A", 100)
	mustOpen(t, e, "A", Long, 10, 100)

	checkRecords(t, mustMark(t, e, 95), nil) // E=50 >= R=48
	checkAccount(t, e, "A", 100, 10, 1000)

	checkRecords(t, mustMark(t, e, 94), []Liquidation{
		{Account: "A", Equity: 40, Fee: 9, FundUsed: 0, ADL: nil, BadDebt: 0},
	})
	checkAccount(t, e, "A", 31, 0, 0)
	checkFund(t, e, 9, 0)
	if p, ok := e.MarkPrice(); !ok || p != 94 {
		t.Fatalf("mark price: got (%d, %v), want (94, true)", p, ok)
	}
}

// 空头强平：价格上涨使权益低于维持要求。
func TestShortLiquidation(t *testing.T) {
	e := mustEngine(t, 1000, 500, 100)
	mustDeposit(t, e, "S", 100)
	mustOpen(t, e, "S", Short, 10, 100)

	// E = 100 - 1050 + 1000 = 50，R = ceil(1050*500/10000) = 53，强平。
	// f = min(50, floor(1050*100/10000)=10) = 10。
	checkRecords(t, mustMark(t, e, 105), []Liquidation{
		{Account: "S", Equity: 50, Fee: 10, FundUsed: 0, ADL: nil, BadDebt: 0},
	})
	checkAccount(t, e, "S", 40, 0, 0)
	checkFund(t, e, 10, 0)
}

// E 恰等于 R 时不强平（自行构造：I=900, Mm=500, M=98, P=95 时 E=R=48）。
func TestEquityEqualsRequirementNoLiquidation(t *testing.T) {
	e := mustEngine(t, 900, 500, 100)
	mustDeposit(t, e, "A", 98)
	mustOpen(t, e, "A", Long, 10, 100) // 需 ceil(1000*900/10000)=90 <= 98

	checkRecords(t, mustMark(t, e, 95), nil) // E = 98+950-1000 = 48 = R
	checkAccount(t, e, "A", 98, 10, 1000)

	// 价格再降 1：E=38 < R=47，强平。
	checkRecords(t, mustMark(t, e, 94), []Liquidation{
		{Account: "A", Equity: 38, Fee: 9, FundUsed: 0, ADL: nil, BadDebt: 0},
	})
	checkAccount(t, e, "A", 29, 0, 0)
	checkFund(t, e, 9, 0)
}

// E 比 R 小 1 时强平（I=501, Mm=500, M=59, P=99 时 E=49, R=50）。
func TestEquityOneBelowRequirement(t *testing.T) {
	e := mustEngine(t, 501, 500, 0)
	mustDeposit(t, e, "A", 59)
	mustOpen(t, e, "A", Long, 10, 100) // 需 ceil(1000*501/10000)=51 <= 59

	checkRecords(t, mustMark(t, e, 99), []Liquidation{
		{Account: "A", Equity: 49, Fee: 0, FundUsed: 0, ADL: nil, BadDebt: 0},
	})
	checkAccount(t, e, "A", 49, 0, 0)
	checkFund(t, e, 0, 0)
}

// 罚金大于 E 时只扣 E（F=10000 时罚金上限 940 远大于 E=40）。
func TestFeeCappedByEquity(t *testing.T) {
	e := mustEngine(t, 1000, 500, 10000)
	mustDeposit(t, e, "A", 100)
	mustOpen(t, e, "A", Long, 10, 100)

	checkRecords(t, mustMark(t, e, 94), []Liquidation{
		{Account: "A", Equity: 40, Fee: 40, FundUsed: 0, ADL: nil, BadDebt: 0},
	})
	checkAccount(t, e, "A", 0, 0, 0)
	checkFund(t, e, 40, 0)
}

// E 为负时 f=0，亏空先由 Z 吸收，无候选时余额记入 B。
func TestNegativeEquityFundAbsorbs(t *testing.T) {
	e := mustEngine(t, 1000, 500, 100)
	mustDeposit(t, e, "X", 100)
	mustOpen(t, e, "X", Long, 10, 100)
	mustMark(t, e, 94) // X 强平，Z=9
	checkFund(t, e, 9, 0)

	mustDeposit(t, e, "Y", 100)
	mustOpen(t, e, "Y", Long, 10, 100)
	// E = 100+800-1000 = -100，d=100，u=min(9,100)=9，s=91，无候选，B=91。
	checkRecords(t, mustMark(t, e, 80), []Liquidation{
		{Account: "Y", Equity: -100, Fee: 0, FundUsed: 9, ADL: nil, BadDebt: 91},
	})
	checkAccount(t, e, "Y", 0, 0, 0)
	checkFund(t, e, 0, 91)
}
