package options

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustEngine(t *testing.T, kind Kind, k, mult, thresh int64) *Engine {
	t.Helper()
	e, err := NewEngine(kind, k, mult, thresh)
	if err != nil {
		t.Fatalf("NewEngine(%v, %d, %d, %d) 失败: %v", kind, k, mult, thresh, err)
	}
	return e
}

func mustTrade(t *testing.T, e *Engine, buyer, seller string, n int64) int64 {
	t.Helper()
	seq, err := e.Trade(buyer, seller, n)
	if err != nil {
		t.Fatalf("Trade(%s, %s, %d) 失败: %v", buyer, seller, n, err)
	}
	return seq
}

func mustMargin(t *testing.T, e *Engine, acct string, g int64) {
	t.Helper()
	if err := e.Margin(acct, g); err != nil {
		t.Fatalf("Margin(%s, %d) 失败: %v", acct, g, err)
	}
}

func mustSettle(t *testing.T, e *Engine, s int64) *Settlement {
	t.Helper()
	st, err := e.Settle(s)
	if err != nil {
		t.Fatalf("Settle(%d) 失败: %v", s, err)
	}
	return st
}

func checkErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: 得到错误 %v，期望 %+v", what, err, want)
	}
}

func netMap(st *Settlement) map[string]int64 {
	m := make(map[string]int64, len(st.NetCash))
	for _, c := range st.NetCash {
		m[c.Account] = c.Net
	}
	return m
}

func sumNet(st *Settlement) int64 {
	var sum int64
	for _, c := range st.NetCash {
		sum += c.Net
	}
	return sum
}

// 规格例题一：认购，Abstain(B) 使 Q 变小，Y 被部分指派并违约。
func TestExampleCallWithAbstain(t *testing.T) {
	e := mustEngine(t, Call, 100, 10, 1)
	if got := mustTrade(t, e, "A", "X", 3); got != 1 {
		t.Fatalf("seq = %d, 期望 1", got)
	}
	if got := mustTrade(t, e, "B", "Y", 5); got != 2 {
		t.Fatalf("seq = %d, 期望 2", got)
	}
	if got := mustTrade(t, e, "A", "Z", 2); got != 3 {
		t.Fatalf("seq = %d, 期望 3", got)
	}
	if err := e.Abstain("B"); err != nil {
		t.Fatalf("Abstain(B) 失败: %v", err)
	}
	mustMargin(t, e, "X", 60)
	mustMargin(t, e, "Y", 25)

	st := mustSettle(t, e, 102)
	if st.Intrinsic != 2 || st.TotalExercised != 5 {
		t.Fatalf("v=%d Q=%d, 期望 v=2 Q=5", st.Intrinsic, st.TotalExercised)
	}
	wantEx := []Exercise{{Account: "A", Qty: 5}}
	if !reflect.DeepEqual(st.Exercises, wantEx) {
		t.Fatalf("行权清单 = %+v, 期望 %+v", st.Exercises, wantEx)
	}
	wantAs := []Assignment{
		{Seq: 1, Account: "X", Qty: 3},
		{Seq: 2, Account: "Y", Qty: 2},
		{Seq: 3, Account: "Z", Qty: 0},
	}
	if !reflect.DeepEqual(st.Assignments, wantAs) {
		t.Fatalf("指派清单 = %+v, 期望 %+v", st.Assignments, wantAs)
	}
	wantDef := []Default{{Account: "Y", Amount: 15}}
	if !reflect.DeepEqual(st.Defaults, wantDef) {
		t.Fatalf("违约清单 = %+v, 期望 %+v", st.Defaults, wantDef)
	}
	wantNet := map[string]int64{"A": 85, "B": 0, "X": -60, "Y": -25, "Z": 0}
	if got := netMap(st); !reflect.DeepEqual(got, wantNet) {
		t.Fatalf("净现金 = %+v, 期望 %+v", got, wantNet)
	}
	if sum := sumNet(st); sum != 0 {
		t.Fatalf("净额合计 = %d, 期望 0", sum)
	}
}

// 内在价值恰等于 T 行权，比 T 小 1 则无人行权。
func TestThresholdExactAndBelow(t *testing.T) {
	setup := func() *Engine {
		e := mustEngine(t, Call, 100, 10, 1)
		mustTrade(t, e, "A", "X", 3)
		mustTrade(t, e, "B", "Y", 5)
		mustTrade(t, e, "A", "Z", 2)
		if err := e.Abstain("B"); err != nil {
			t.Fatalf("Abstain(B) 失败: %v", err)
		}
		mustMargin(t, e, "X", 60)
		mustMargin(t, e, "Y", 25)
		return e
	}

	// v = 1 恰等于 T = 1，仍行权。
	st := mustSettle(t, setup(), 101)
	if st.Intrinsic != 1 || st.TotalExercised != 5 {
		t.Fatalf("v=%d Q=%d, 期望 v=1 Q=5", st.Intrinsic, st.TotalExercised)
	}
	if len(st.Exercises) != 1 || st.Exercises[0] != (Exercise{Account: "A", Qty: 5}) {
		t.Fatalf("行权清单 = %+v, 期望仅 A 行权 5", st.Exercises)
	}

	// v = 0 比 T 小 1，无人行权：无指派、无违约、净额全 0。
	st = mustSettle(t, setup(), 100)
	if st.Intrinsic != 0 || st.TotalExercised != 0 {
		t.Fatalf("v=%d Q=%d, 期望 v=0 Q=0", st.Intrinsic, st.TotalExercised)
	}
	if len(st.Exercises) != 0 || len(st.Defaults) != 0 {
		t.Fatalf("行权/违约清单应为空: %+v %+v", st.Exercises, st.Defaults)
	}
	for _, a := range st.Assignments {
		if a.Qty != 0 {
			t.Fatalf("无人行权时指派量应为 0: %+v", a)
		}
	}
	if sum := sumNet(st); sum != 0 {
		t.Fatalf("净额合计 = %d, 期望 0", sum)
	}
	for _, c := range st.NetCash {
		if c.Net != 0 {
			t.Fatalf("无人行权时净额应为 0: %+v", c)
		}
	}
}

// 认沽：v = max(K−S, 0)，同样按门槛行权。
func TestPutOption(t *testing.T) {
	e := mustEngine(t, Put, 100, 10, 5)
	mustTrade(t, e, "A", "X", 4)
	mustTrade(t, e, "B", "Y", 6)
	mustMargin(t, e, "X", 500)

	st := mustSettle(t, e, 90)
	if st.Intrinsic != 10 || st.TotalExercised != 10 {
		t.Fatalf("v=%d Q=%d, 期望 v=10 Q=10", st.Intrinsic, st.TotalExercised)
	}
	wantAs := []Assignment{
		{Seq: 1, Account: "X", Qty: 4},
		{Seq: 2, Account: "Y", Qty: 6},
	}
	if !reflect.DeepEqual(st.Assignments, wantAs) {
		t.Fatalf("指派清单 = %+v, 期望 %+v", st.Assignments, wantAs)
	}
	// owe_X=400 保证金 500 足额，owe_Y=600 无保证金，Δ=600。
	wantDef := []Default{{Account: "Y", Amount: 600}}
	if !reflect.DeepEqual(st.Defaults, wantDef) {
		t.Fatalf("违约清单 = %+v, 期望 %+v", st.Defaults, wantDef)
	}
	// recv_A=400, recv_B=600, loss_A=floor(600×400/1000)=240, loss_B=360。
	wantNet := map[string]int64{"A": 160, "B": 240, "X": -400, "Y": 0}
	if got := netMap(st); !reflect.DeepEqual(got, wantNet) {
		t.Fatalf("净现金 = %+v, 期望 %+v", got, wantNet)
	}
}

// 认沽价外：S > K 时 v = 0，无人行权。
func TestPutOutOfTheMoney(t *testing.T) {
	e := mustEngine(t, Put, 100, 10, 1)
	mustTrade(t, e, "A", "X", 4)
	st := mustSettle(t, e, 105)
	if st.Intrinsic != 0 || st.TotalExercised != 0 || len(st.Defaults) != 0 {
		t.Fatalf("价外应无人行权: v=%d Q=%d 违约=%+v", st.Intrinsic, st.TotalExercised, st.Defaults)
	}
}

// 全部放弃时 Q 为 0：无行权、无指派、无违约。
func TestAllAbstain(t *testing.T) {
	e := mustEngine(t, Call, 100, 10, 1)
	mustTrade(t, e, "A", "X", 3)
	mustTrade(t, e, "B", "Y", 5)
	if err := e.Abstain("A"); err != nil {
		t.Fatalf("Abstain(A) 失败: %v", err)
	}
	if err := e.Abstain("B"); err != nil {
		t.Fatalf("Abstain(B) 失败: %v", err)
	}

	st := mustSettle(t, e, 200)
	if st.TotalExercised != 0 || len(st.Exercises) != 0 || len(st.Defaults) != 0 {
		t.Fatalf("全部放弃应无人行权: Q=%d 行权=%+v 违约=%+v",
			st.TotalExercised, st.Exercises, st.Defaults)
	}
	wantAs := []Assignment{
		{Seq: 1, Account: "X", Qty: 0},
		{Seq: 2, Account: "Y", Qty: 0},
	}
	if !reflect.DeepEqual(st.Assignments, wantAs) {
		t.Fatalf("指派清单 = %+v, 期望 %+v", st.Assignments, wantAs)
	}
	if sum := sumNet(st); sum != 0 {
		t.Fatalf("净额合计 = %d, 期望 0", sum)
	}
}

// 放弃按账户生效：同一账户的两个多头批次都不行权，
// 且放弃登记之后新增的多头批次同样不行权。
func TestAbstainIsAccountWide(t *testing.T) {
	e := mustEngine(t, Call, 100, 1, 1)
	mustTrade(t, e, "A", "X", 3)
	mustTrade(t, e, "A", "Y", 2)
	mustTrade(t, e, "B", "Z", 4)
	if err := e.Abstain("A"); err != nil {
		t.Fatalf("Abstain(A) 失败: %v", err)
	}
	// 放弃之后 A 新增的多头批次同样不行权。
	mustTrade(t, e, "A", "W", 1)

	st := mustSettle(t, e, 200)
	wantEx := []Exercise{{Account: "B", Qty: 4}}
	if !reflect.DeepEqual(st.Exercises, wantEx) {
		t.Fatalf("行权清单 = %+v, 期望 %+v", st.Exercises, wantEx)
	}
	wantAs := []Assignment{
		{Seq: 1, Account: "X", Qty: 3},
		{Seq: 2, Account: "Y", Qty: 1},
		{Seq: 3, Account: "Z", Qty: 0},
		{Seq: 4, Account: "W", Qty: 0},
	}
	if !reflect.DeepEqual(st.Assignments, wantAs) {
		t.Fatalf("指派清单 = %+v, 期望 %+v", st.Assignments, wantAs)
	}
}

// 同一账户同时持有多头与空头批次：应收不抵扣应付。
func TestSameAccountLongAndShort(t *testing.T) {
	e := mustEngine(t, Call, 100, 10, 1)
	mustTrade(t, e, "A", "X", 3)
	mustTrade(t, e, "Y", "A", 2)
	mustMargin(t, e, "A", 50)

	st := mustSettle(t, e, 110)
	// v=10，A 行权 3、Y 行权 2；指派 X 得 3、A 得 2。
	wantAs := []Assignment{
		{Seq: 1, Account: "X", Qty: 3},
		{Seq: 2, Account: "A", Qty: 2},
	}
	if !reflect.DeepEqual(st.Assignments, wantAs) {
		t.Fatalf("指派清单 = %+v, 期望 %+v", st.Assignments, wantAs)
	}
	// owe_A=200 以保证金 50 支付，D_A=150；owe_X=300 全缺，Δ=450。
	wantDef := []Default{{Account: "A", Amount: 150}, {Account: "X", Amount: 300}}
	if !reflect.DeepEqual(st.Defaults, wantDef) {
		t.Fatalf("违约清单 = %+v, 期望 %+v", st.Defaults, wantDef)
	}
	// recv_A=300, recv_Y=200；loss_A=floor(450×300/500)=270, loss_Y=180。
	// A 的应收不抵扣应付：net_A = 300 − 270 − 50 = −20。
	// X 无保证金，实付 0（缺口 300 由行权方分担），净额为 0。
	wantNet := map[string]int64{"A": -20, "X": 0, "Y": 20}
	if got := netMap(st); !reflect.DeepEqual(got, wantNet) {
		t.Fatalf("净现金 = %+v, 期望 %+v", got, wantNet)
	}
}

// 保证金恰等于 owe 时无缺口；少 1 则缺口为 1。
func TestMarginExactAndOneShort(t *testing.T) {
	build := func(margin int64) *Engine {
		e := mustEngine(t, Call, 100, 1, 1)
		mustTrade(t, e, "A", "X", 5)
		mustMargin(t, e, "X", margin)
		return e
	}

	// v=100，owe_X = 100×5×1 = 500，保证金恰等于 owe。
	st := mustSettle(t, build(500), 200)
	if len(st.Defaults) != 0 {
		t.Fatalf("保证金恰等于 owe 时应无违约: %+v", st.Defaults)
	}
	wantNet := map[string]int64{"A": 500, "X": -500}
	if got := netMap(st); !reflect.DeepEqual(got, wantNet) {
		t.Fatalf("净现金 = %+v, 期望 %+v", got, wantNet)
	}

	// 保证金少 1：缺口为 1，由唯一行权账户承担。
	st = mustSettle(t, build(499), 200)
	wantDef := []Default{{Account: "X", Amount: 1}}
	if !reflect.DeepEqual(st.Defaults, wantDef) {
		t.Fatalf("违约清单 = %+v, 期望 %+v", st.Defaults, wantDef)
	}
	wantNet = map[string]int64{"A": 499, "X": -499}
	if got := netMap(st); !reflect.DeepEqual(got, wantNet) {
		t.Fatalf("净现金 = %+v, 期望 %+v", got, wantNet)
	}
}

// Δ 等于 ΣT（全部空头无保证金）时，每个行权账户的损失恰为其应收。
func TestDeltaEqualsTotalRecv(t *testing.T) {
	e := mustEngine(t, Call, 100, 10, 1)
	mustTrade(t, e, "A", "X", 3)
	mustTrade(t, e, "B", "Y", 5)

	st := mustSettle(t, e, 102)
	// Δ = ΣT = 160，loss_A = 60 = recv_A，loss_B = 100 = recv_B。
	wantDef := []Default{{Account: "X", Amount: 60}, {Account: "Y", Amount: 100}}
	if !reflect.DeepEqual(st.Defaults, wantDef) {
		t.Fatalf("违约清单 = %+v, 期望 %+v", st.Defaults, wantDef)
	}
	for _, c := range st.NetCash {
		if c.Net != 0 {
			t.Fatalf("Δ=ΣT 时所有净额应为 0: %+v", c)
		}
	}
}

// 大数用例：Δ×recv_a 超出 64 位（约 10^36），须用 128 位或大整数计算。
func TestBigNumbers(t *testing.T) {
	e := mustEngine(t, Call, 1, 1000, 1)
	// 1000 笔 n=10^6，累计恰为上限 10^9。
	for i := 0; i < 1000; i++ {
		mustTrade(t, e, "A", "X", 1_000_000)
	}
	st := mustSettle(t, e, 1_000_000)
	// v = 999999，recv_A = owe_X = 999999×10^9×1000 = 999999000000000000。
	const want = int64(999999000000000000)
	if st.TotalExercised != 1_000_000_000 {
		t.Fatalf("Q = %d, 期望 10^9", st.TotalExercised)
	}
	wantDef := []Default{{Account: "X", Amount: want}}
	if !reflect.DeepEqual(st.Defaults, wantDef) {
		t.Fatalf("违约清单 = %+v, 期望 %+v", st.Defaults, wantDef)
	}
	// 唯一行权账户承担全部缺口：loss_A = Δ = recv_A，净额为 0。
	if got := netMap(st); got["A"] != 0 || got["X"] != 0 {
		t.Fatalf("净现金 = %+v, 期望 A 与 X 均为 0", got)
	}

	// 两个行权账户的大数分摊：验证不变量（损失和 = Δ、各自不超应收）。
	e2 := mustEngine(t, Call, 1, 1000, 1)
	for i := 0; i < 500; i++ {
		mustTrade(t, e2, "A", "X", 1_000_000)
		mustTrade(t, e2, "B", "Y", 1_000_000)
	}
	mustMargin(t, e2, "X", 1)
	st2 := mustSettle(t, e2, 1_000_000)
	var delta int64
	for _, d := range st2.Defaults {
		delta += d.Amount
	}
	const recvEach = int64(999999000000000000) / 2
	var lossSum int64
	nets := netMap(st2)
	for acct, recv := range map[string]int64{"A": recvEach, "B": recvEach} {
		loss := recv - nets[acct] // 这两账户 pay 为 0
		if loss < 0 || loss > recv {
			t.Fatalf("loss_%s = %d 超出 [0, %d]", acct, loss, recv)
		}
		lossSum += loss
	}
	if lossSum != delta {
		t.Fatalf("损失合计 %d != 缺口 %d", lossSum, delta)
	}
	if sum := sumNet(st2); sum != 0 {
		t.Fatalf("净额合计 = %d, 期望 0", sum)
	}
}

// 自成交被拒，且被拒绝的 Trade 不消耗批次序号。
func TestSelfTradeRejected(t *testing.T) {
	e := mustEngine(t, Call, 100, 10, 1)
	_, err := e.Trade("A", "A", 1)
	checkErr(t, "Trade(A, A, 1)", err, ErrSelfTrade)
	if got := mustTrade(t, e, "A", "B", 1); got != 1 {
		t.Fatalf("被拒交易不应消耗 seq, 得到 seq = %d, 期望 1", got)
	}
}

// Settle 成功之后的任何操作都报已到期；参数非法优先于已到期。
func TestOpsAfterSettleRejected(t *testing.T) {
	e := mustEngine(t, Call, 100, 10, 1)
	mustTrade(t, e, "A", "X", 1)
	mustSettle(t, e, 102)

	_, err := e.Trade("B", "Y", 1)
	checkErr(t, "到期后 Trade", err, ErrExpired)
	checkErr(t, "到期后 Margin", e.Margin("X", 1), ErrExpired)
	checkErr(t, "到期后 Abstain", e.Abstain("A"), ErrExpired)
	_, err = e.Settle(103)
	checkErr(t, "重复 Settle", err, ErrExpired)

	// 参数非法优先于已到期。
	_, err = e.Trade("", "Y", 1)
	checkErr(t, "到期后空账户 Trade", err, ErrInvalidParam)
	_, err = e.Trade("B", "B", 1)
	checkErr(t, "到期后自成交", err, ErrExpired)
	checkErr(t, "到期后空账户 Margin", e.Margin("", 1), ErrInvalidParam)
	checkErr(t, "到期后无持仓 Abstain", e.Abstain("nobody"), ErrExpired)
	_, err = e.Settle(0)
	checkErr(t, "到期后非法结算价", err, ErrInvalidParam)
}

// 被拒绝的操作不改变任何状态。
func TestRejectedOpsKeepState(t *testing.T) {
	e := mustEngine(t, Call, 100, 10, 1)

	// 非法 Trade：n 越界、空账户、自成交，均不改变状态。
	_, err := e.Trade("A", "B", 0)
	checkErr(t, "n=0", err, ErrInvalidParam)
	_, err = e.Trade("A", "B", 1_000_001)
	checkErr(t, "n 超上限", err, ErrInvalidParam)
	_, err = e.Trade("", "B", 1)
	checkErr(t, "空买方", err, ErrInvalidParam)
	_, err = e.Trade("A", "", 1)
	checkErr(t, "空卖方", err, ErrInvalidParam)
	_, err = e.Trade("A", "A", 1)
	checkErr(t, "自成交", err, ErrSelfTrade)

	// 非法 Margin：g 越界、空账户，均不累计。
	checkErr(t, "g=0", e.Margin("M", 0), ErrInvalidParam)
	checkErr(t, "g 超上限", e.Margin("M", 1_000_000_000_001), ErrInvalidParam)
	checkErr(t, "空账户", e.Margin("", 1), ErrInvalidParam)
	if got := e.MarginOf("M"); got != 0 {
		t.Fatalf("被拒 Margin 不应累计, MarginOf(M) = %d", got)
	}

	// 无多头持仓的账户不能放弃。
	checkErr(t, "无持仓 Abstain", e.Abstain("ghost"), ErrNoLong)
	if e.Abstained("ghost") {
		t.Fatal("被拒 Abstain 不应登记")
	}

	// 非法 Settle 不使系列到期。
	_, err = e.Settle(0)
	checkErr(t, "S=0", err, ErrInvalidParam)
	_, err = e.Settle(1_000_001)
	checkErr(t, "S 超上限", err, ErrInvalidParam)
	if e.Expired() {
		t.Fatal("非法 Settle 不应使系列到期")
	}

	// 状态未被污染：首笔交易 seq 仍为 1，放弃可正常登记。
	if got := mustTrade(t, e, "A", "B", 2); got != 1 {
		t.Fatalf("seq = %d, 期望 1", got)
	}
	if err := e.Abstain("A"); err != nil {
		t.Fatalf("Abstain(A) 失败: %v", err)
	}
	// 重复放弃成功且无副作用。
	if err := e.Abstain("A"); err != nil {
		t.Fatalf("重复 Abstain(A) 应成功: %v", err)
	}
	st := mustSettle(t, e, 200)
	if st.TotalExercised != 0 {
		t.Fatalf("A 已放弃, Q = %d, 期望 0", st.TotalExercised)
	}
}

// 累计上限：Trade 的 n 之和不得超 10^9，Margin 累计不得超 10^15。
func TestCumulativeLimits(t *testing.T) {
	e := mustEngine(t, Call, 100, 10, 1)
	for i := 0; i < 1000; i++ {
		mustTrade(t, e, "A", "B", 1_000_000)
	}
	_, err := e.Trade("A", "B", 1)
	checkErr(t, "累计超 10^9", err, ErrInvalidParam)
	if got := e.LongQty("A"); got != 1_000_000_000 {
		t.Fatalf("被拒交易不应计入, LongQty(A) = %d", got)
	}

	for i := 0; i < 1000; i++ {
		mustMargin(t, e, "M", 1_000_000_000_000)
	}
	checkErr(t, "累计超 10^15", e.Margin("M", 1), ErrInvalidParam)
	if got := e.MarginOf("M"); got != 1_000_000_000_000_000 {
		t.Fatalf("MarginOf(M) = %d, 期望 10^15", got)
	}
}

// 构造参数校验。
func TestConstructorValidation(t *testing.T) {
	bad := []struct {
		kind         Kind
		k, mu, thres int64
	}{
		{Kind(2), 100, 10, 1},
		{Kind(-1), 100, 10, 1},
		{Call, 0, 10, 1},
		{Call, 1_000_001, 10, 1},
		{Call, 100, 0, 1},
		{Call, 100, 1001, 1},
		{Call, 100, 10, 0},
		{Call, 100, 10, 1_000_001},
	}
	for i, c := range bad {
		if _, err := NewEngine(c.kind, c.k, c.mu, c.thres); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("用例 %d: 期望 ErrInvalidParam, 得到 %v", i, err)
		}
	}
	// 边界值合法。
	for _, c := range [][4]int64{{1, 1, 1}, {1_000_000, 1000, 1_000_000}} {
		if _, err := NewEngine(Call, c[0], c[1], c[2]); err != nil {
			t.Fatalf("边界参数 %v 应合法: %v", c, err)
		}
		if _, err := NewEngine(Put, c[0], c[1], c[2]); err != nil {
			t.Fatalf("边界参数 %v 应合法: %v", c, err)
		}
	}
}

// 并发调用：结果等价于某个串行顺序，多次 Settle 中恰有一次成功。
func TestConcurrency(t *testing.T) {
	e := mustEngine(t, Call, 50, 2, 3)
	mustTrade(t, e, "A", "X", 10)
	mustTrade(t, e, "B", "Y", 20)

	const workers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	settleOK := 0
	settleExpired := 0
	var settled *Settlement

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				switch i % 4 {
				case 0:
					_, _ = e.Trade("C", "Z", 1)
				case 1:
					_ = e.Margin("X", 5)
				case 2:
					_ = e.Abstain("A")
				case 3:
					st, err := e.Settle(60)
					mu.Lock()
					if err == nil {
						settleOK++
						settled = st
					} else if errors.Is(err, ErrExpired) {
						settleExpired++
					}
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()

	if settleOK != 1 {
		t.Fatalf("成功的 Settle 次数 = %d, 期望恰为 1", settleOK)
	}
	if settleOK+settleExpired == 0 {
		t.Fatal("没有任何 Settle 被执行")
	}
	if !e.Expired() {
		t.Fatal("Settle 成功后 Expired 应为 true")
	}

	// 不变量：被指派总量等于总行权量；指派只落在 seq 升序的前缀上。
	var assigned int64
	seenZero := false
	prevSeq := int64(0)
	for _, a := range settled.Assignments {
		if a.Seq <= prevSeq {
			t.Fatalf("指派清单未按 seq 升序: %+v", settled.Assignments)
		}
		prevSeq = a.Seq
		if seenZero && a.Qty != 0 {
			t.Fatalf("出现 0 指派后仍有非零指派: %+v", settled.Assignments)
		}
		if a.Qty == 0 {
			seenZero = true
		}
		assigned += a.Qty
	}
	if assigned != settled.TotalExercised {
		t.Fatalf("被指派总量 %d != 总行权量 %d", assigned, settled.TotalExercised)
	}
	if sum := sumNet(settled); sum != 0 {
		t.Fatalf("净额合计 = %d, 期望 0", sum)
	}
}

// 规格例题二：无放弃，缺口分摊的向下取整与余量按字节序分配。
func TestExampleLossSharing(t *testing.T) {
	e := mustEngine(t, Call, 100, 10, 1)
	mustTrade(t, e, "A", "X", 3)
	mustTrade(t, e, "B", "Y", 5)
	mustTrade(t, e, "A", "Z", 2)
	mustMargin(t, e, "X", 60)
	mustMargin(t, e, "Y", 71)

	st := mustSettle(t, e, 102)
	if st.Intrinsic != 2 || st.TotalExercised != 10 {
		t.Fatalf("v=%d Q=%d, 期望 v=2 Q=10", st.Intrinsic, st.TotalExercised)
	}
	wantEx := []Exercise{{Account: "A", Qty: 5}, {Account: "B", Qty: 5}}
	if !reflect.DeepEqual(st.Exercises, wantEx) {
		t.Fatalf("行权清单 = %+v, 期望 %+v", st.Exercises, wantEx)
	}
	wantAs := []Assignment{
		{Seq: 1, Account: "X", Qty: 3},
		{Seq: 2, Account: "Y", Qty: 5},
		{Seq: 3, Account: "Z", Qty: 2},
	}
	if !reflect.DeepEqual(st.Assignments, wantAs) {
		t.Fatalf("指派清单 = %+v, 期望 %+v", st.Assignments, wantAs)
	}
	wantDef := []Default{{Account: "Y", Amount: 29}, {Account: "Z", Amount: 40}}
	if !reflect.DeepEqual(st.Defaults, wantDef) {
		t.Fatalf("违约清单 = %+v, 期望 %+v", st.Defaults, wantDef)
	}
	// Δ=69，floor(69×100/200)=34，余量 1 给字节序最小的 A。
	wantNet := map[string]int64{"A": 65, "B": 66, "X": -60, "Y": -71, "Z": 0}
	if got := netMap(st); !reflect.DeepEqual(got, wantNet) {
		t.Fatalf("净现金 = %+v, 期望 %+v", got, wantNet)
	}
	if sum := sumNet(st); sum != 0 {
		t.Fatalf("净额合计 = %d, 期望 0", sum)
	}
}
