package ontology

import "testing"

func testConfig() Config {
	return Config{
		LongThreshold:  100,
		ShortThreshold: 10,
		RefundPercents: [3]int{10, 30, 50},
		ChangePercents: [3]int{5, 15, 25},
		MaxChanges:     2,
		VoucherTTL:     5000,
	}
}

func mustRegister(t *testing.T, s *System, id string, fare, dep int64) {
	t.Helper()
	if err := s.RegisterFlight(id, fare, dep); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}

func mustBuy(t *testing.T, s *System, id, owner, flight string) {
	t.Helper()
	if err := s.PurchaseTicket(id, owner, flight); err != nil {
		t.Fatalf("buy %s: %v", id, err)
	}
}

func expectKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	oe, ok := asOpError(err)
	if !ok {
		t.Fatalf("want OpError kind %d, got %v", want, err)
	}
	if oe.Kind != want {
		t.Fatalf("want kind %d, got kind %d (%v)", want, oe.Kind, err)
	}
}

// TestTierBoundaries 覆盖距出发恰等于两阈值及各差一秒的档位归属。
func TestTierBoundaries(t *testing.T) {
	long, short := int64(100), int64(10)
	cases := []struct {
		delta int64
		want  tier
		dep   bool
	}{
		{101, tierFar, false},
		{100, tierFar, false}, // 恰等于长阈值归较宽松（远）档
		{99, tierMid, false},
		{11, tierMid, false},
		{10, tierMid, false}, // 恰等于短阈值归较宽松（中）档
		{9, tierNear, false},
		{1, tierNear, false},
		{0, tierNear, true}, // 操作时刻 == 出发时刻：已出发
		{-5, tierNear, true},
	}
	for _, c := range cases {
		got := tierFor(c.delta, long, short)
		if got.tier != c.want || got.departed != c.dep {
			t.Fatalf("delta=%d want tier=%d dep=%v, got tier=%d dep=%v",
				c.delta, c.want, c.dep, got.tier, got.departed)
		}
	}
}

// TestRefundTiersAndCeil 验证退票三档手续费与向上取整。
func TestRefundTiersAndCeil(t *testing.T) {
	s := New(testConfig())
	mustRegister(t, s, "F", 1000, 1000)
	mustBuy(t, s, "T", "alice", "F")
	q, err := s.QuoteRefund("T", 900) // delta=100 远档 10%
	if err != nil || q.Fee != 100 || q.RefundCash != 900 {
		t.Fatalf("far: %+v err=%v", q, err)
	}
	q, _ = s.QuoteRefund("T", 901) // delta=99 中档 30%
	if q.Fee != 300 || q.RefundCash != 700 {
		t.Fatalf("mid: %+v", q)
	}
	q, _ = s.QuoteRefund("T", 991) // delta=9 近档 50%
	if q.Fee != 500 || q.RefundCash != 500 {
		t.Fatalf("near: %+v", q)
	}
	if _, err := s.QuoteRefund("T", 1000); err == nil {
		t.Fatal("want departed error")
	}
	s2 := New(testConfig())
	mustRegister(t, s2, "G", 101, 1000)
	mustBuy(t, s2, "T2", "bob", "G")
	q2, _ := s2.QuoteRefund("T2", 900) // 101*10%=10.1 -> 11
	if q2.Fee != 11 || q2.RefundCash != 90 {
		t.Fatalf("ceil: %+v", q2)
	}
}

// TestChangeDiffDirections 覆盖正/负/零差价三种改签的现金与代金券流向。
func TestChangeDiffDirections(t *testing.T) {
	cfg := testConfig()

	s := New(cfg) // 正差价
	mustRegister(t, s, "F0", 1000, 1000)
	mustRegister(t, s, "F1", 1300, 2000)
	mustBuy(t, s, "T", "alice", "F0")
	res, err := s.Change(ChangeRequest{TicketID: "T", TargetID: "F1", Cash: 350}, 900)
	if err != nil {
		t.Fatalf("positive: %v", err)
	}
	if res.ChangeFee != 50 || res.Diff != 300 || res.CashDue != 350 || res.VoucherID != "" {
		t.Fatalf("positive: %+v", res)
	}
	tk, _ := s.TicketView("T")
	if tk.Fare != 1300 || tk.Departure != 2000 || tk.Changes != 1 {
		t.Fatalf("after change: %+v", tk)
	}

	s = New(cfg) // 零差价
	mustRegister(t, s, "Z0", 1000, 1000)
	mustRegister(t, s, "Z1", 1000, 2000)
	mustBuy(t, s, "T", "alice", "Z0")
	res, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "Z1", Cash: 50}, 900)
	if err != nil || res.ChangeFee != 50 || res.Diff != 0 || res.CashDue != 50 {
		t.Fatalf("zero: %+v err=%v", res, err)
	}

	s = New(cfg) // 负差价
	mustRegister(t, s, "N0", 1000, 1000)
	mustRegister(t, s, "N1", 800, 2000)
	mustBuy(t, s, "T", "alice", "N0")
	res, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "N1", Cash: 50}, 900)
	if err != nil {
		t.Fatalf("negative: %v", err)
	}
	if res.Diff != -200 || res.ChangeFee != 50 || res.CashDue != 50 || res.VoucherID == "" {
		t.Fatalf("negative: %+v", res)
	}
	v, ok := s.VoucherView(res.VoucherID)
	if !ok || v.Amount != 200 || v.Owner != "alice" || v.ExpiresAt != 5900 {
		t.Fatalf("voucher: %+v ok=%v", v, ok)
	}
	tk, _ = s.TicketView("T")
	if tk.Fare != 800 {
		t.Fatalf("fare want 800 got %d", tk.Fare)
	}
}

// seedVoucher 通过 -amount 负差价改签造出金额为 amount 的代金券。
// S2 为同价下一目标（fare 相同，出发 3000），当前票面价变为 1000-amount。
func seedVoucher(t *testing.T, amount int64) (*System, string) {
	t.Helper()
	s := New(testConfig())
	mustRegister(t, s, "S0", 1000, 1000)
	mustRegister(t, s, "S1", 1000-amount, 2000)
	mustRegister(t, s, "S2", 1000-amount, 2050)
	mustBuy(t, s, "T", "alice", "S0")
	res, err := s.Change(ChangeRequest{TicketID: "T", TargetID: "S1", Cash: 50}, 900)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return s, res.VoucherID
}

// TestVoucherCover 覆盖代金券面额大于/等于/小于应补总额。
// 当前 fare=800，S2 出发 3000，now=2900 delta=100 远档 fee=40。
func TestVoucherCover(t *testing.T) {
	s, vid := seedVoucher(t, 200) // 大于
	res, err := s.Change(ChangeRequest{TicketID: "T", TargetID: "S2", VoucherID: vid, Cash: 0}, 1899)
	if err != nil {
		t.Fatalf("greater: %v", err)
	}
	if res.VoucherUsed != 40 || res.CashDue != 0 {
		t.Fatalf("greater: %+v", res)
	}
	v, _ := s.VoucherView(vid)
	if v.Amount != 160 || v.Used {
		t.Fatalf("greater remaining: %+v", v)
	}

	s, vid = seedVoucher(t, 48) // 等于：fare952, far档 fee48
	res, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "S2", VoucherID: vid, Cash: 0}, 1899)
	if err != nil {
		t.Fatalf("equal: %v", err)
	}
	if res.VoucherUsed != 48 || res.CashDue != 0 {
		t.Fatalf("equal: %+v", res)
	}
	v, _ = s.VoucherView(vid)
	if !v.Used || v.Amount != 0 {
		t.Fatalf("equal should be used up: %+v", v)
	}

	s, vid = seedVoucher(t, 20) // 小于：fare980 far档 fee49，券20 -> 现金29
	res, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "S2", VoucherID: vid, Cash: 29}, 1899)
	if err != nil {
		t.Fatalf("less: %v", err)
	}
	if res.VoucherUsed != 20 || res.CashDue != 29 {
		t.Fatalf("less: %+v", res)
	}

	s, vid = seedVoucher(t, 20) // 现金不符：应补 49-20=29，错误报应补现金
	_, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "S2", VoucherID: vid, Cash: 19}, 1899)
	if err == nil {
		t.Fatal("want payment mismatch")
	}
	oe, ok := asOpError(err)
	if !ok || oe.Kind != KindPaymentMismatch || oe.CashDue != 29 || oe.Expected != 19 {
		t.Fatalf("payment mismatch detail: %v", err)
	}
}

// TestInvoluntaryRefund 非自愿退票：全退当前票面 + 历次改签费，代金券不退。
func TestInvoluntaryRefund(t *testing.T) {
	s := New(testConfig())
	mustRegister(t, s, "F0", 1000, 1000)
	mustRegister(t, s, "F1", 800, 2000)
	mustBuy(t, s, "T", "alice", "F0")
	r1, err := s.Change(ChangeRequest{TicketID: "T", TargetID: "F1", Cash: 50}, 900)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CancelFlight("F1", 1500) // 票当前在 F1（未退）-> 非自愿
	if err != nil || n != 1 {
		t.Fatalf("cancel: n=%d err=%v", n, err)
	}
	rr, err := s.Refund("T", 1500) // 退 800 票面 + 50 改签费
	if err != nil {
		t.Fatalf("invol refund: %v", err)
	}
	if rr.RefundCash != 850 || rr.Fare != 800 || rr.ChangeFees != 50 || !rr.Involuntary {
		t.Fatalf("invol refund: %+v", rr)
	}
	v, ok := s.VoucherView(r1.VoucherID) // 代金券不退
	if !ok || v.Amount != 200 {
		t.Fatalf("voucher should remain: %+v ok=%v", v, ok)
	}
	if _, err := s.Refund("T", 1500); err == nil {
		t.Fatal("want already refunded")
	}
}

// TestInvoluntaryChangeThenVoluntary 非自愿改签后再自愿改签：计档与计次。
func TestInvoluntaryChangeThenVoluntary(t *testing.T) {
	cfg := testConfig()
	cfg.MaxChanges = 1
	s := New(cfg)
	mustRegister(t, s, "F0", 1000, 1000)
	mustRegister(t, s, "F1", 1200, 3000)
	mustRegister(t, s, "F2", 1200, 4000)
	mustBuy(t, s, "T", "alice", "F0")
	n, _ := s.CancelFlight("F0", 2000) // 晚于出发也不报已出发
	if n != 1 {
		t.Fatalf("cancel n=%d", n)
	}
	// 非自愿改签：免费、正差价 +200 免补、不生券、不计次、不带支付
	res, err := s.Change(ChangeRequest{TicketID: "T", TargetID: "F1"}, 2000)
	if err != nil {
		t.Fatalf("invol change: %v", err)
	}
	if res.ChangeFee != 0 || res.Diff != 200 || res.TotalDue != 0 || res.CashDue != 0 || res.VoucherID != "" {
		t.Fatalf("invol change money: %+v", res)
	}
	tk, _ := s.TicketView("T")
	if tk.Involuntary || tk.Changes != 0 || tk.ChangeFees != 0 || tk.Fare != 1200 {
		t.Fatalf("after invol change: %+v", tk)
	}
	// 非自愿改签带支付应被拒绝（应补为 0）
	// （此处已无法重放，故另起系统验证）
	s2 := New(cfg)
	mustRegister(t, s2, "G0", 1000, 1000)
	mustRegister(t, s2, "G1", 1200, 2000)
	mustBuy(t, s2, "T", "alice", "G0")
	_, _ = s2.CancelFlight("G0", 500)
	if _, err := s2.Change(ChangeRequest{TicketID: "T", TargetID: "G1", Cash: 10}, 500); err == nil {
		t.Fatal("invol change with cash must be rejected")
	}
	// 再自愿改签：计入第 1 次（上限 1），按原航班 F1 出发 3000 计档。
	// now=2990 delta=10 中档 15% fee=1200*15%=180，零差价。
	res, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "F2", Cash: 180}, 2990)
	if err != nil {
		t.Fatalf("voluntary after invol: %v", err)
	}
	if res.ChangeFee != 180 || res.Changes != 1 {
		t.Fatalf("voluntary after invol: %+v", res)
	}
	// 已达上限 1，再次自愿改签报次数超限
	mustRegister(t, s, "F3", 1200, 5000)
	_, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "F3", Cash: 0}, 3950)
	expectKind(t, err, KindChangeLimit)
}

// TestClockRewindAndRejectNoSideEffect 时钟回退；被拒绝操作不推进时钟、不改状态。
func TestClockRewindAndRejectNoSideEffect(t *testing.T) {
	s := New(testConfig())
	mustRegister(t, s, "F0", 1000, 1000)
	mustRegister(t, s, "F1", 1000, 2000)
	mustBuy(t, s, "T", "alice", "F0")
	if _, err := s.Change(ChangeRequest{TicketID: "T", TargetID: "F1", Cash: 50}, 900); err != nil {
		t.Fatal(err)
	}
	// 回退
	before, _ := s.TicketView("T")
	_, err := s.Refund("T", 899)
	expectKind(t, err, KindClockRewind)
	after, _ := s.TicketView("T")
	if after.Changes != before.Changes || after.Refunded != before.Refunded {
		t.Fatalf("rejected op changed state: before=%+v after=%+v", before, after)
	}
	// 被拒操作不推进时钟：now=900 仍应被接受（非回退）
	if _, err := s.Refund("T", 900); err != nil {
		t.Fatalf("clock should still be 900, refund: %v", err)
	}
}

// TestRejectionOrder 验证统一拒绝次序中每一对相邻类别只报更靠前者。
func TestRejectionOrder(t *testing.T) {
	cfg := testConfig()

	// 参数非法 > 时钟回退：非法参数 + 回退时刻
	s := New(cfg)
	_, err := s.Refund("", -1)
	expectKind(t, err, KindInvalidArgument)

	// 时钟回退 > 票不存在：先推进时钟，再用不存在票 + 回退
	s = New(cfg)
	mustRegister(t, s, "F0", 1000, 1000)
	mustBuy(t, s, "T", "alice", "F0")
	if _, err := s.Refund("T", 900); err != nil {
		t.Fatal(err)
	}
	_, err = s.Refund("NOPE", 899)
	expectKind(t, err, KindClockRewind)

	// 票不存在 > 已退票：不存在票不可能已退，验证不存在优先
	s = New(cfg)
	_, err = s.Refund("NOPE", 100)
	expectKind(t, err, KindTicketNotFound)

	// 已退票 > 已出发：一张已退且 now>=dep 的票
	s = New(cfg)
	mustRegister(t, s, "F0", 1000, 1000)
	mustBuy(t, s, "T", "alice", "F0")
	if _, err := s.Refund("T", 500); err != nil {
		t.Fatal(err)
	}
	_, err = s.Refund("T", 2000) // 已出发且已退
	expectKind(t, err, KindTicketState)

	// 已出发 > 次数超限：MaxChanges=0 且已出发
	c0 := cfg
	c0.MaxChanges = 0
	s = New(c0)
	mustRegister(t, s, "F0", 1000, 1000)
	mustRegister(t, s, "F1", 1000, 2000)
	mustBuy(t, s, "T", "alice", "F0")
	_, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "F1", Cash: 0}, 1000)
	expectKind(t, err, KindDeparted)

	// 次数超限 > 代金券不存在：MaxChanges=0，未出发，给不存在券
	s = New(c0)
	mustRegister(t, s, "F0", 1000, 1000)
	mustRegister(t, s, "F1", 1000, 2000)
	mustBuy(t, s, "T", "alice", "F0")
	_, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "F1", VoucherID: "VX", Cash: 0}, 500)
	expectKind(t, err, KindChangeLimit)

	// 构造一张已用尽/过期/他属的券，用于代金券四类次序
	buildVoucherSystem := func(t *testing.T) (*System, string) {
		s := New(cfg)
		mustRegister(t, s, "F0", 1000, 1000)
		mustRegister(t, s, "F1", 960, 2000) // -40 券
		mustRegister(t, s, "F2", 960, 3000)
		mustBuy(t, s, "T", "alice", "F0")
		r, err := s.Change(ChangeRequest{TicketID: "T", TargetID: "F1", Cash: 50}, 900)
		if err != nil {
			t.Fatal(err)
		}
		return s, r.VoucherID
	}

	// 代金券不存在 > 归属不符：不存在的券
	s, _ = buildVoucherSystem(t)
	_, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "F2", VoucherID: "NOPE", Cash: 48}, 1899)
	expectKind(t, err, KindVoucherNotFound)

	// 归属不符 > 已过期：给券改主人。直接伪造一张他属且过期的券。
	s = New(cfg)
	mustRegister(t, s, "F0", 1000, 1000)
	mustRegister(t, s, "F2", 1000, 5000)
	mustBuy(t, s, "T", "alice", "F0")
	s.mu.Lock()
	s.vouchers["VOTHER"] = &Voucher{ID: "VOTHER", Owner: "bob", Amount: 10, ExpiresAt: 1, Used: false}
	s.mu.Unlock()
	_, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "F2", VoucherID: "VOTHER", Cash: 40}, 500)
	expectKind(t, err, KindVoucherOwner)

	// 已过期 > 已用尽：alice 的券既过期又用尽
	s = New(cfg)
	mustRegister(t, s, "F0", 1000, 1000)
	mustRegister(t, s, "F2", 1000, 2000)
	mustBuy(t, s, "T", "alice", "F0")
	s.mu.Lock()
	s.vouchers["VDEAD"] = &Voucher{ID: "VDEAD", Owner: "alice", Amount: 0, ExpiresAt: 1, Used: true}
	s.mu.Unlock()
	_, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "F2", VoucherID: "VDEAD", Cash: 40}, 500)
	expectKind(t, err, KindVoucherExpired)

	// 恰等于到期时刻视为已过期（未用尽）
	s = New(cfg)
	mustRegister(t, s, "F0", 1000, 1000)
	mustRegister(t, s, "F2", 1000, 5000)
	mustBuy(t, s, "T", "alice", "F0")
	s.mu.Lock()
	s.vouchers["VEXP"] = &Voucher{ID: "VEXP", Owner: "alice", Amount: 10, ExpiresAt: 500, Used: false}
	s.mu.Unlock()
	_, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "F2", VoucherID: "VEXP", Cash: 40}, 500)
	expectKind(t, err, KindVoucherExpired)

	// 已用尽 > 支付不符：有效但用尽（直接构造一张未过期但用尽的券）
	s = New(cfg)
	mustRegister(t, s, "F0", 1000, 1000)
	mustRegister(t, s, "FU", 1000, 5000)
	mustBuy(t, s, "T", "alice", "F0")
	s.mu.Lock()
	s.vouchers["VUSED"] = &Voucher{ID: "VUSED", Owner: "alice", Amount: 0, ExpiresAt: 9000, Used: true}
	s.mu.Unlock()
	_, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "FU", VoucherID: "VUSED", Cash: 1}, 500)
	expectKind(t, err, KindVoucherUsed)

	// 支付不符：有效券 + 错误现金（应补现金随错误返回）
	s, vid := buildVoucherSystem(t)
	_, err = s.Change(ChangeRequest{TicketID: "T", TargetID: "F2", VoucherID: vid, Cash: 1}, 1899)
	expectKind(t, err, KindPaymentMismatch)
}
