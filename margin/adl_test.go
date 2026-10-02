package margin

import "testing"

// 规格示例二：候选按盈利率而非绝对浮盈排序（S2 的 120/200 高于
// S1 的 200/1000），前面账户的罚金能被后面账户的亏空吸收。
func TestSpecExampleADL(t *testing.T) {
	e := mustEngine(t, 1000, 500, 100)
	mustDeposit(t, e, "L1", 85)
	mustOpen(t, e, "L1", Long, 10, 85) // 需 ceil(850*1000/10000)=85
	mustDeposit(t, e, "L2", 100)
	mustOpen(t, e, "L2", Long, 10, 100)
	mustDeposit(t, e, "S1", 100)
	mustOpen(t, e, "S1", Short, 10, 100)
	mustDeposit(t, e, "S2", 20)
	mustOpen(t, e, "S2", Short, 1, 200) // 需 ceil(200*1000/10000)=20

	// Mark(80)：L1 E=35<40，L2 E=-100<40，S1 E=300、S2 E=140 不入选。
	checkRecords(t, mustMark(t, e, 80), []Liquidation{
		{Account: "L1", Equity: 35, Fee: 8, FundUsed: 0, ADL: nil, BadDebt: 0},
		{Account: "L2", Equity: -100, Fee: 0, FundUsed: 8,
			ADL: []ADLEntry{{Account: "S2", Take: 92}}, BadDebt: 0},
	})
	checkAccount(t, e, "L1", 27, 0, 0)
	checkAccount(t, e, "L2", 0, 0, 0)
	checkAccount(t, e, "S1", 100, -10, -1000) // 未被减仓
	checkAccount(t, e, "S2", 20, -1, -108)    // C 由 -200 变为 -108
	checkFund(t, e, 0, 0)
}

// 盈利率相等时按账户编号字节序小者在前。
func TestADLTieBreakByID(t *testing.T) {
	e := mustEngine(t, 1000, 500, 0)
	mustDeposit(t, e, "B1", 100)
	mustOpen(t, e, "B1", Short, 10, 100) // pnl=110, |C|=1000, 率 0.11
	mustDeposit(t, e, "A2", 200)
	mustOpen(t, e, "A2", Short, 20, 100) // pnl=220, |C|=2000, 率 0.11
	mustDeposit(t, e, "L", 100)
	mustOpen(t, e, "L", Long, 10, 100)

	// L 在 P=89 时 E=-10，d=10，Z=0，s=10；两者盈利率相等，A2 在前。
	checkRecords(t, mustMark(t, e, 89), []Liquidation{
		{Account: "L", Equity: -10, Fee: 0, FundUsed: 0,
			ADL: []ADLEntry{{Account: "A2", Take: 10}}, BadDebt: 0},
	})
	checkAccount(t, e, "A2", 200, -20, -1990)
	checkAccount(t, e, "B1", 100, -10, -1000)
	checkFund(t, e, 0, 0)
}

// 浮盈恰为 0 的账户不入选候选。
func TestZeroPnlNotCandidate(t *testing.T) {
	e := mustEngine(t, 1000, 500, 0)
	mustDeposit(t, e, "L", 110)
	mustOpen(t, e, "L", Long, 10, 110)
	mustDeposit(t, e, "S", 89)
	mustOpen(t, e, "S", Short, 10, 89)

	// P=89：L 的 E=-100，s=100；S 的 pnl = -890+890 = 0，不入选，B=100。
	checkRecords(t, mustMark(t, e, 89), []Liquidation{
		{Account: "L", Equity: -100, Fee: 0, FundUsed: 0, ADL: nil, BadDebt: 100},
	})
	checkAccount(t, e, "S", 89, -10, -890)
	checkFund(t, e, 0, 100)
}

// 候选用尽后剩余记入 B。
func TestADLExhaustedBadDebt(t *testing.T) {
	e := mustEngine(t, 1000, 500, 0)
	mustDeposit(t, e, "L", 100)
	mustOpen(t, e, "L", Long, 10, 100)
	mustDeposit(t, e, "S", 10)
	mustOpen(t, e, "S", Short, 1, 100)

	// P=80：L 的 E=-100，s=100；S 的 pnl=20，take=20，余 80 记入 B。
	checkRecords(t, mustMark(t, e, 80), []Liquidation{
		{Account: "L", Equity: -100, Fee: 0, FundUsed: 0,
			ADL: []ADLEntry{{Account: "S", Take: 20}}, BadDebt: 80},
	})
	checkAccount(t, e, "S", 10, -1, -80)
	checkFund(t, e, 0, 80)
}

// 两个亏空账户先后触发减仓时，第二次用改写后的 C 重新排序：
// 第一次从 S2 取走后 S2 的盈利率降到与 S1 相等，第二次按编号 S1 在前。
func TestTwoBankruptReSort(t *testing.T) {
	e := mustEngine(t, 1000, 500, 0)
	mustDeposit(t, e, "L1", 100)
	mustOpen(t, e, "L1", Long, 10, 100)
	mustDeposit(t, e, "L2", 100)
	mustOpen(t, e, "L2", Long, 10, 100)
	mustDeposit(t, e, "S1", 100)
	mustOpen(t, e, "S1", Short, 10, 100) // pnl=110, |C|=1000, 率 0.110
	mustDeposit(t, e, "S2", 101)
	mustOpen(t, e, "S2", Short, 10, 101) // pnl=120, |C|=1010, 率约 0.119

	checkRecords(t, mustMark(t, e, 89), []Liquidation{
		{Account: "L1", Equity: -10, Fee: 0, FundUsed: 0,
			ADL: []ADLEntry{{Account: "S2", Take: 10}}, BadDebt: 0},
		// S2 被取走 10 后 pnl=110、|C|=1000，与 S1 相等，按编号 S1 在前。
		{Account: "L2", Equity: -10, Fee: 0, FundUsed: 0,
			ADL: []ADLEntry{{Account: "S1", Take: 10}}, BadDebt: 0},
	})
	checkAccount(t, e, "S1", 100, -10, -990)
	checkAccount(t, e, "S2", 101, -10, -1000)
	checkFund(t, e, 0, 0)
}

// Λ 一次定出：减仓把候选 L 的权益压到维持要求之下，本次 Mark 也不再
// 将其入选强平；下一次 Mark 才按新状态重新判定。
func TestLambdaFixedCandidateNotReliquidated(t *testing.T) {
	e := mustEngine(t, 10000, 9000, 0)
	mustDeposit(t, e, "S", 1000)
	mustOpen(t, e, "S", Short, 10, 100) // 需 M >= 1000
	mustDeposit(t, e, "L", 100)
	mustOpen(t, e, "L", Long, 10, 10) // 需 M >= 100

	// P=300：S 的 E=-1000，s=1000；L 的 pnl=2900，take=1000。
	// 减仓后 L 的权益 2000 < R=2700，但因 Λ 在 Mark 开始时已定出，
	// 本次不强平 L。
	checkRecords(t, mustMark(t, e, 300), []Liquidation{
		{Account: "S", Equity: -1000, Fee: 0, FundUsed: 0,
			ADL: []ADLEntry{{Account: "L", Take: 1000}}, BadDebt: 0},
	})
	checkAccount(t, e, "L", 100, 10, 1100) // 持仓仍在，C 被改写
	checkFund(t, e, 0, 0)

	// 同一价格再次 Mark：L 的 E=2000 < R=2700，此时才强平。
	checkRecords(t, mustMark(t, e, 300), []Liquidation{
		{Account: "L", Equity: 2000, Fee: 0, FundUsed: 0, ADL: nil, BadDebt: 0},
	})
	checkAccount(t, e, "L", 2000, 0, 0)
}

// 罚金的时序：排在前面账户的罚金能被后面账户的亏空吸收，反过来不能。
func TestFundFeeTiming(t *testing.T) {
	// 缴费者 A 排在亏空者 Z 前：Z 能吸收到 A 的罚金。
	e := mustEngine(t, 1000, 500, 100)
	mustDeposit(t, e, "A", 210)
	mustOpen(t, e, "A", Long, 10, 100) // P=80 时 E=10，f=8
	mustDeposit(t, e, "Z", 100)
	mustOpen(t, e, "Z", Long, 10, 100) // P=80 时 E=-100，d=100

	checkRecords(t, mustMark(t, e, 80), []Liquidation{
		{Account: "A", Equity: 10, Fee: 8, FundUsed: 0, ADL: nil, BadDebt: 0},
		{Account: "Z", Equity: -100, Fee: 0, FundUsed: 8, ADL: nil, BadDebt: 92},
	})
	checkFund(t, e, 0, 92)

	// 亏空者 A 排在缴费者 Z 前：A 吸收不到 Z 的罚金，全部记入 B。
	e2 := mustEngine(t, 1000, 500, 100)
	mustDeposit(t, e2, "A", 100)
	mustOpen(t, e2, "A", Long, 10, 100) // P=80 时 E=-100，d=100
	mustDeposit(t, e2, "Z", 210)
	mustOpen(t, e2, "Z", Long, 10, 100) // P=80 时 E=10，f=8

	checkRecords(t, mustMark(t, e2, 80), []Liquidation{
		{Account: "A", Equity: -100, Fee: 0, FundUsed: 0, ADL: nil, BadDebt: 100},
		{Account: "Z", Equity: 10, Fee: 8, FundUsed: 0, ADL: nil, BadDebt: 0},
	})
	checkFund(t, e2, 8, 100)
}

// Close 亏损先由 Z 吸收再记 B，不触发自动减仓、不收平仓费。
func TestCloseLossFundThenBadDebt(t *testing.T) {
	e := mustEngine(t, 1000, 500, 100)
	mustDeposit(t, e, "X", 100)
	mustOpen(t, e, "X", Long, 10, 100)
	mustMark(t, e, 94) // X 强平，Z=9
	checkFund(t, e, 9, 0)

	// 盈利对手方 S：若 Close 触发减仓会被改写，用于验证不触发。
	mustDeposit(t, e, "S", 100)
	mustOpen(t, e, "S", Short, 10, 100)

	mustDeposit(t, e, "Y", 100)
	mustOpen(t, e, "Y", Long, 10, 100)
	if err := e.Close("Y", 80); err != nil {
		t.Fatalf("Close(Y, 80): %v", err)
	}
	// E=-100：u=min(9,100)=9，B=91；S 的 C 不变。
	checkAccount(t, e, "Y", 0, 0, 0)
	checkAccount(t, e, "S", 100, -10, -1000)
	checkFund(t, e, 0, 91)
}

// Close 盈利时 M 变为 E，之后可全额取出。
func TestCloseProfitAndWithdraw(t *testing.T) {
	e := mustEngine(t, 1000, 500, 100)
	mustDeposit(t, e, "A", 100)
	mustOpen(t, e, "A", Long, 10, 100)
	if err := e.Close("A", 110); err != nil {
		t.Fatalf("Close(A, 110): %v", err)
	}
	checkAccount(t, e, "A", 200, 0, 0)
	checkFund(t, e, 0, 0)
	if err := e.Withdraw("A", 200); err != nil {
		t.Fatalf("Withdraw(A, 200): %v", err)
	}
	checkAccount(t, e, "A", 0, 0, 0)
}
