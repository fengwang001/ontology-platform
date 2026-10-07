package chargeback_test

import (
	"errors"
	"testing"

	"ontology/chargeback"
)

func testConfig() chargeback.Config {
	return chargeback.Config{
		FraudWindowDays:       30,
		NotReceivedWindowDays: 45,
		DuplicateWindowDays:   60,
		DuplicateMatchDays:    10,
		ResponseWindowDays:    7,
		ReviewWindowDays:      5,
		ArbitrationFee:        15,
	}
}

func codeOf(err error) chargeback.ErrCode {
	var ce *chargeback.Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	return -1
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，得到错误: %v", err)
	}
}

func mustErr(t *testing.T, err error, want chargeback.ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %v，得到成功", want)
	}
	if got := codeOf(err); got != want {
		t.Fatalf("期望错误 %v，得到 %v (%v)", want, got, err)
	}
}

func mustBalance(t *testing.T, e *chargeback.Engine, now int, merchant string, want int64) {
	t.Helper()
	got, err := e.MerchantBalance(now, merchant)
	mustOK(t, err)
	if got != want {
		t.Fatalf("商户 %s 余额: 期望 %d，得到 %d", merchant, want, got)
	}
}

func mustIssuer(t *testing.T, e *chargeback.Engine, now int, want int64) {
	t.Helper()
	got, err := e.IssuerBalance(now)
	mustOK(t, err)
	if got != want {
		t.Fatalf("发卡行账: 期望 %d，得到 %d", want, got)
	}
}

func mustPending(t *testing.T, e *chargeback.Engine, now int, want int64) {
	t.Helper()
	got, err := e.PendingHeld(now)
	mustOK(t, err)
	if got != want {
		t.Fatalf("待决扣回款: 期望 %d，得到 %d", want, got)
	}
}

func mustDisputable(t *testing.T, e *chargeback.Engine, now int, txn string, want int64) {
	t.Helper()
	got, err := e.DisputableAmount(now, txn)
	mustOK(t, err)
	if got != want {
		t.Fatalf("交易 %s 可拒付余额: 期望 %d，得到 %d", txn, want, got)
	}
}

func mustState(t *testing.T, e *chargeback.Engine, now int, caseID string, want chargeback.State) {
	t.Helper()
	got, err := e.CaseState(now, caseID)
	mustOK(t, err)
	if got != want {
		t.Fatalf("案件 %s 状态: 期望 %v，得到 %v", caseID, want, got)
	}
}

func addTxn(t *testing.T, e *chargeback.Engine, now int, id string, settle int, amount int64) {
	t.Helper()
	mustOK(t, e.AddTransaction(now, chargeback.Transaction{
		ID: id, SettleDay: settle, Amount: amount, CardID: "card-" + id, MerchantID: "mch",
	}))
}

// 提起窗口：恰等于窗口天数允许，超一天报窗口已过。
func TestDisputeWindowBoundary(t *testing.T) {
	e := chargeback.New(testConfig())
	addTxn(t, e, 10, "t1", 10, 100)

	// 恰等于窗口（10 + 30 = 40）：允许。
	mustOK(t, e.OpenDispute(40, "c1", "t1", chargeback.ReasonFraud, 50))
	mustState(t, e, 40, "c1", chargeback.StateOpened)

	// 差一天（41）：窗口已过。
	mustErr(t, e.OpenDispute(41, "c2", "t1", chargeback.ReasonFraud, 10), chargeback.ErrWindowExpired)
}

// 应诉截止日：第 R 天应诉有效，第 R+1 天起逾期放弃。
func TestResponseDeadlineBoundary(t *testing.T) {
	e := chargeback.New(testConfig())
	addTxn(t, e, 0, "t1", 0, 300)
	mustOK(t, e.OpenDispute(5, "c1", "t1", chargeback.ReasonFraud, 100))
	mustOK(t, e.OpenDispute(5, "c2", "t1", chargeback.ReasonFraud, 100))

	// 截止日当天（5 + 7 = 12）应诉：有效。
	mustOK(t, e.Respond(12, "c1"))
	mustState(t, e, 12, "c1", chargeback.StateAwaitingReview)

	// c2 在截止日次日（13）：已逾期放弃，应诉报状态不允许。
	mustState(t, e, 13, "c2", chargeback.StateClosedIssuerWin)
	mustErr(t, e.Respond(13, "c2"), chargeback.ErrInvalidState)

	// 逾期放弃的资金：已扣回款项不返还，归发卡行。
	mustBalance(t, e, 13, "mch", -200)
	mustIssuer(t, e, 13, 100)
	mustPending(t, e, 13, 100) // 只剩 c1 的待决扣回款
}

// 审阅截止日：第 P 天接受有效，第 P+1 天起视为默认接受。
func TestReviewDeadlineBoundary(t *testing.T) {
	e := chargeback.New(testConfig())
	addTxn(t, e, 0, "t1", 0, 300)
	mustOK(t, e.OpenDispute(0, "c1", "t1", chargeback.ReasonFraud, 100))
	mustOK(t, e.OpenDispute(0, "c2", "t1", chargeback.ReasonFraud, 100))
	mustOK(t, e.Respond(3, "c1"))
	mustOK(t, e.Respond(3, "c2"))

	// 截止日当天（3 + 5 = 8）接受：有效，商户胜，款项返还。
	mustOK(t, e.Accept(8, "c1"))
	mustState(t, e, 8, "c1", chargeback.StateClosedMerchantWin)
	mustBalance(t, e, 8, "mch", -100)

	// c2 在次日（9）：已默认接受，显式接受报状态不允许。
	mustState(t, e, 9, "c2", chargeback.StateClosedMerchantWin)
	mustErr(t, e.Accept(9, "c2"), chargeback.ErrInvalidState)

	// 默认接受的资金结果与显式接受完全相同：款项返还。
	mustBalance(t, e, 9, "mch", 0)
	mustIssuer(t, e, 9, 0)
	mustPending(t, e, 9, 0)
	mustDisputable(t, e, 9, "t1", 300)
}

// 逾期与操作同日：截止日次日案件已终结，任何操作一律状态不允许。
func TestOperationOnTimeoutDay(t *testing.T) {
	e := chargeback.New(testConfig())
	addTxn(t, e, 0, "t1", 0, 100)
	mustOK(t, e.OpenDispute(0, "c1", "t1", chargeback.ReasonFraud, 100))

	// 应诉期最后一日仍应诉有效；次日（8 = 0+7+1）起逾期。
	mustState(t, e, 7, "c1", chargeback.StateOpened)
	mustState(t, e, 8, "c1", chargeback.StateClosedIssuerWin)
	mustErr(t, e.Respond(8, "c1"), chargeback.ErrInvalidState)
	mustErr(t, e.Accept(8, "c1"), chargeback.ErrInvalidState)
	mustErr(t, e.PreArbitrate(8, "c1"), chargeback.ErrInvalidState)
	mustErr(t, e.Rule(8, "c1", chargeback.OutcomeIssuerWin), chargeback.ErrInvalidState)
}

// 多案件并存，合计恰好用尽可拒付余额；再提一笔即超额。
func TestMultipleCasesExactBalance(t *testing.T) {
	e := chargeback.New(testConfig())
	addTxn(t, e, 0, "t1", 0, 100)

	mustOK(t, e.OpenDispute(1, "c1", "t1", chargeback.ReasonFraud, 40))
	mustDisputable(t, e, 1, "t1", 60)
	mustOK(t, e.OpenDispute(2, "c2", "t1", chargeback.ReasonNotReceived, 60))
	mustDisputable(t, e, 2, "t1", 0)

	// 余额已用尽，哪怕 1 个最小单位也超额。
	mustErr(t, e.OpenDispute(3, "c3", "t1", chargeback.ReasonFraud, 1), chargeback.ErrExceedsDisputable)

	// c1 商户胜后释放 40，可再提起。
	mustOK(t, e.Respond(4, "c1"))
	mustOK(t, e.Accept(5, "c1"))
	mustDisputable(t, e, 5, "t1", 40)
	// 同原因（欺诈）已有商户胜案件，不得再提；换未收货原因。
	mustErr(t, e.OpenDispute(6, "c3", "t1", chargeback.ReasonFraud, 40), chargeback.ErrDuplicateCase)
	mustOK(t, e.OpenDispute(6, "c3", "t1", chargeback.ReasonNotReceived, 40))
	mustDisputable(t, e, 6, "t1", 0)
}

// 重复扣款：依据交易的匹配窗口恰等/差一天、占用与释放。
func TestDuplicateBasisRules(t *testing.T) {
	cfg := testConfig()
	e := chargeback.New(cfg)
	// 同卡同商户同金额的两笔交易：结算日差恰为 DuplicateMatchDays。
	mustOK(t, e.AddTransaction(0, chargeback.Transaction{
		ID: "base", SettleDay: 0, Amount: 200, CardID: "card", MerchantID: "mch",
	}))
	mustOK(t, e.AddTransaction(10, chargeback.Transaction{
		ID: "dup1", SettleDay: 10, Amount: 200, CardID: "card", MerchantID: "mch",
	}))
	// 差 10 天恰等：成立。
	mustOK(t, e.OpenDispute(20, "c1", "dup1", chargeback.ReasonDuplicate, 200))

	// 结算日差 11 > 10：无依据。
	mustOK(t, e.AddTransaction(21, chargeback.Transaction{
		ID: "dup2", SettleDay: 21, Amount: 200, CardID: "card", MerchantID: "mch",
	}))

	// base 已被 c1 占用，dup2 找不到其他依据（与 base 差 21 天也超窗）。
	mustErr(t, e.OpenDispute(30, "c2", "dup2", chargeback.ReasonDuplicate, 200), chargeback.ErrNoBasis)

	// c1 商户胜后释放 base；但 dup2 与 base 差 21 天仍超窗，依旧无依据。
	mustOK(t, e.Respond(25, "c1"))
	mustOK(t, e.Accept(26, "c1"))
	mustErr(t, e.OpenDispute(30, "c2", "dup2", chargeback.ReasonDuplicate, 200), chargeback.ErrNoBasis)

	// 新交易 dup3 与 base 差 8 天：base 已释放，可支撑。
	mustOK(t, e.AddTransaction(33, chargeback.Transaction{
		ID: "dup3", SettleDay: 8, Amount: 200, CardID: "card", MerchantID: "mch",
	}))
	mustOK(t, e.OpenDispute(40, "c3", "dup3", chargeback.ReasonDuplicate, 200))

	// c3 进行中占用 base；dup4 与 base 差 5 天但无可用依据。
	mustOK(t, e.AddTransaction(40, chargeback.Transaction{
		ID: "dup4", SettleDay: 5, Amount: 200, CardID: "card", MerchantID: "mch",
	}))
	mustErr(t, e.OpenDispute(41, "c4", "dup4", chargeback.ReasonDuplicate, 200), chargeback.ErrNoBasis)

	// c3 以发卡行胜告终仍占用 base（未以商户胜告终）。
	mustOK(t, e.Respond(42, "c3"))
	mustOK(t, e.PreArbitrate(43, "c3"))
	mustOK(t, e.Rule(44, "c3", chargeback.OutcomeIssuerWin))
	mustErr(t, e.OpenDispute(45, "c4", "dup4", chargeback.ReasonDuplicate, 200), chargeback.ErrNoBasis)
}

// 预仲裁两种结果的资金账：商户胜返还款项且发卡行承担仲裁费；
// 发卡行胜得款且商户承担仲裁费（余额允许为负）。
func TestPreArbitrationOutcomes(t *testing.T) {
	e := chargeback.New(testConfig())
	addTxn(t, e, 0, "t1", 0, 100)
	addTxn(t, e, 0, "t2", 0, 100)
	mustOK(t, e.OpenDispute(1, "c1", "t1", chargeback.ReasonFraud, 100))
	mustOK(t, e.OpenDispute(1, "c2", "t2", chargeback.ReasonFraud, 100))
	mustOK(t, e.Respond(2, "c1"))
	mustOK(t, e.Respond(2, "c2"))
	mustOK(t, e.PreArbitrate(3, "c1"))
	mustOK(t, e.PreArbitrate(3, "c2"))

	// 商户胜：款项返还，发卡行账记 -15 仲裁费。
	mustOK(t, e.Rule(100, "c1", chargeback.OutcomeMerchantWin))
	mustBalance(t, e, 100, "mch", -100)
	mustIssuer(t, e, 100, -15)
	mustPending(t, e, 100, 100)
	mustDisputable(t, e, 100, "t1", 100)

	// 发卡行胜：款项归发卡行，商户另付 15 仲裁费。
	mustOK(t, e.Rule(200, "c2", chargeback.OutcomeIssuerWin))
	mustBalance(t, e, 200, "mch", -115)
	mustIssuer(t, e, 200, 85) // -15 + 100
	mustPending(t, e, 200, 0)
	// 发卡行胜的案件仍占用可拒付余额。
	mustDisputable(t, e, 200, "t2", 0)

	fees, err := e.TotalFees(200)
	mustOK(t, err)
	if fees != 30 {
		t.Fatalf("累计仲裁费: 期望 30，得到 %d", fees)
	}
}

// 被拒绝的操作不得改变任何案件、资金与时钟。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	e := chargeback.New(testConfig())
	addTxn(t, e, 0, "t1", 0, 100)
	mustOK(t, e.OpenDispute(5, "c1", "t1", chargeback.ReasonFraud, 100))

	// 一系列被拒绝的操作：超额、不存在、状态不允许、时钟回退。
	mustErr(t, e.OpenDispute(6, "c2", "t1", chargeback.ReasonFraud, 1), chargeback.ErrExceedsDisputable)
	mustErr(t, e.OpenDispute(6, "c3", "nope", chargeback.ReasonFraud, 1), chargeback.ErrTransactionNotFound)
	mustErr(t, e.Accept(6, "c1"), chargeback.ErrInvalidState)
	mustErr(t, e.Respond(6, "ghost"), chargeback.ErrCaseNotFound)
	mustErr(t, e.Respond(4, "c1"), chargeback.ErrClockRegression)

	// 时钟未被拒绝操作推进：仍可在 now=5 合法应诉。
	mustOK(t, e.Respond(5, "c1"))
	mustBalance(t, e, 5, "mch", -100)
	mustPending(t, e, 5, 100)
	mustIssuer(t, e, 5, 0)
	mustDisputable(t, e, 5, "t1", 0)
	// 不存在的案件查询不受影响。
	_, err := e.CaseState(5, "c2")
	mustErr(t, err, chargeback.ErrCaseNotFound)
}

// 时钟回退：now 小于上一次被接受操作的 now 时报错，且不改变时钟。
func TestClockRegression(t *testing.T) {
	e := chargeback.New(testConfig())
	addTxn(t, e, 10, "t1", 5, 100)
	mustErr(t, e.OpenDispute(9, "c1", "t1", chargeback.ReasonFraud, 10), chargeback.ErrClockRegression)
	mustErr(t, e.AddTransaction(3, chargeback.Transaction{
		ID: "t2", SettleDay: 3, Amount: 1, CardID: "c", MerchantID: "mch",
	}), chargeback.ErrClockRegression)
	// 时钟仍停在 10：now=10 的操作可接受。
	mustOK(t, e.OpenDispute(10, "c1", "t1", chargeback.ReasonFraud, 10))
}

// 错误优先级：多项条件同时违反时只报优先级最高的第一个。
func TestErrorPriority(t *testing.T) {
	e := chargeback.New(testConfig())
	addTxn(t, e, 0, "t1", 0, 100)
	mustOK(t, e.OpenDispute(1, "c1", "t1", chargeback.ReasonFraud, 90))

	// 参数非法 > 时钟回退：金额为 0 且时钟回退，报参数非法。
	mustErr(t, e.OpenDispute(0, "c2", "t1", chargeback.ReasonFraud, 0), chargeback.ErrInvalidArgument)
	// 时钟回退 > 交易不存在。
	mustErr(t, e.OpenDispute(0, "c2", "ghost", chargeback.ReasonFraud, 10), chargeback.ErrClockRegression)
	// 交易不存在 > 窗口已过（交易都不存在，无所谓窗口）。
	mustErr(t, e.OpenDispute(100, "c2", "ghost", chargeback.ReasonFraud, 10), chargeback.ErrTransactionNotFound)
	// 窗口已过 > 超出可拒付余额：窗口已过且金额超额，报窗口已过。
	mustErr(t, e.OpenDispute(31, "c2", "t1", chargeback.ReasonFraud, 200), chargeback.ErrWindowExpired)
	// 重复提起 > 超出可拒付余额：构造同交易同原因商户胜案件。
	mustOK(t, e.Respond(2, "c1"))
	mustOK(t, e.Accept(3, "c1"))
	mustErr(t, e.OpenDispute(4, "c2", "t1", chargeback.ReasonFraud, 200), chargeback.ErrDuplicateCase)
	// 超出可拒付余额 > 无依据：重复扣款金额超额时先报超额。
	mustOK(t, e.AddTransaction(4, chargeback.Transaction{
		ID: "t2", SettleDay: 1, Amount: 50, CardID: "card-t1", MerchantID: "mch",
	}))
	mustErr(t, e.OpenDispute(5, "c3", "t2", chargeback.ReasonDuplicate, 60), chargeback.ErrExceedsDisputable)

	// 其余操作：参数非法 > 时钟回退 > 案件不存在 > 状态不允许。
	mustErr(t, e.Rule(0, "", chargeback.OutcomeNone), chargeback.ErrInvalidArgument)
	mustErr(t, e.Respond(0, "c1"), chargeback.ErrClockRegression)
	mustErr(t, e.Respond(5, "ghost"), chargeback.ErrCaseNotFound)
	mustErr(t, e.Rule(5, "c1", chargeback.OutcomeMerchantWin), chargeback.ErrInvalidState)
}

// 重复提起：同交易同原因已有商户胜案件则拒绝；其他原因不受影响。
func TestDuplicateCaseAfterMerchantWin(t *testing.T) {
	e := chargeback.New(testConfig())
	addTxn(t, e, 0, "t1", 0, 100)
	mustOK(t, e.OpenDispute(1, "c1", "t1", chargeback.ReasonFraud, 50))
	mustOK(t, e.Respond(2, "c1"))
	mustOK(t, e.Accept(3, "c1"))

	// 同原因再提：重复提起。
	mustErr(t, e.OpenDispute(4, "c2", "t1", chargeback.ReasonFraud, 10), chargeback.ErrDuplicateCase)
	// 其他原因不受影响。
	mustOK(t, e.OpenDispute(4, "c3", "t1", chargeback.ReasonNotReceived, 50))
}

// 显式接受与逾期默认接受的资金结果必须完全相同。
func TestAcceptEqualsAutoAccept(t *testing.T) {
	build := func(explicit bool) *chargeback.Engine {
		e := chargeback.New(testConfig())
		mustOK(t, e.AddTransaction(0, chargeback.Transaction{
			ID: "t1", SettleDay: 0, Amount: 100, CardID: "card", MerchantID: "mch",
		}))
		mustOK(t, e.OpenDispute(1, "c1", "t1", chargeback.ReasonFraud, 70))
		mustOK(t, e.Respond(2, "c1"))
		if explicit {
			mustOK(t, e.Accept(7, "c1")) // 截止日 2+5=7 当天
		}
		return e
	}
	explicit, lazy := build(true), build(false)
	for _, now := range []int{8, 50} {
		for _, got := range []struct {
			name string
			a, b int64
		}{
			{"商户余额", mustBal(t, explicit, now), mustBal(t, lazy, now)},
			{"发卡行账", mustIss(t, explicit, now), mustIss(t, lazy, now)},
			{"待决扣回款", mustPend(t, explicit, now), mustPend(t, lazy, now)},
		} {
			if got.a != got.b {
				t.Fatalf("now=%d %s: 显式接受 %d != 默认接受 %d", now, got.name, got.a, got.b)
			}
		}
	}
}

func mustBal(t *testing.T, e *chargeback.Engine, now int) int64 {
	t.Helper()
	v, err := e.MerchantBalance(now, "mch")
	mustOK(t, err)
	return v
}

func mustIss(t *testing.T, e *chargeback.Engine, now int) int64 {
	t.Helper()
	v, err := e.IssuerBalance(now)
	mustOK(t, err)
	return v
}

func mustPend(t *testing.T, e *chargeback.Engine, now int) int64 {
	t.Helper()
	v, err := e.PendingHeld(now)
	mustOK(t, err)
	return v
}

// 预仲裁一经发起，应诉与接受不可再用；裁决只能对预仲裁中的案件。
func TestPreArbitrationLocksOps(t *testing.T) {
	e := chargeback.New(testConfig())
	addTxn(t, e, 0, "t1", 0, 100)
	mustOK(t, e.OpenDispute(1, "c1", "t1", chargeback.ReasonFraud, 100))

	// 未应诉前不能裁决。
	mustErr(t, e.Rule(2, "c1", chargeback.OutcomeMerchantWin), chargeback.ErrInvalidState)
	// 应诉后不可再次应诉、不可撤回。
	mustOK(t, e.Respond(2, "c1"))
	mustErr(t, e.Respond(3, "c1"), chargeback.ErrInvalidState)
	// 预仲裁每案仅限一次。
	mustOK(t, e.PreArbitrate(3, "c1"))
	mustErr(t, e.PreArbitrate(4, "c1"), chargeback.ErrInvalidState)
	// 预仲裁后应诉与接受不可再用。
	mustErr(t, e.Respond(4, "c1"), chargeback.ErrInvalidState)
	mustErr(t, e.Accept(4, "c1"), chargeback.ErrInvalidState)
	// 裁决无时限：很远未来仍可裁决。
	mustOK(t, e.Rule(100000, "c1", chargeback.OutcomeMerchantWin))
	// 终局后一切操作状态不允许。
	mustErr(t, e.Rule(100001, "c1", chargeback.OutcomeIssuerWin), chargeback.ErrInvalidState)
	mustErr(t, e.Accept(100001, "c1"), chargeback.ErrInvalidState)
}

// 资金守恒：任意时刻 商户余额 + 发卡行账 + 待决扣回款 + 累计仲裁费 = 0。
func TestConservationInvariant(t *testing.T) {
	e := chargeback.New(testConfig())
	check := func(now int) {
		t.Helper()
		sum, err := e.LedgerSummary(now)
		mustOK(t, err)
		if !sum.Balanced() {
			t.Fatalf("now=%d 资金不守恒: %+v", now, sum)
		}
	}
	addTxn(t, e, 0, "t1", 0, 500)
	check(0)
	mustOK(t, e.OpenDispute(1, "c1", "t1", chargeback.ReasonFraud, 100))
	check(1)
	mustOK(t, e.OpenDispute(2, "c2", "t1", chargeback.ReasonNotReceived, 200))
	check(2)
	mustOK(t, e.Respond(3, "c1"))
	check(3)
	mustOK(t, e.PreArbitrate(4, "c1"))
	check(4)
	mustOK(t, e.Rule(5, "c1", chargeback.OutcomeIssuerWin))
	check(5)
	// c2 应诉期逾期（2+7=9，第 10 天起放弃）。
	check(10)
	check(1000)
}
