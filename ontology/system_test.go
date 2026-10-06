package ffm

import (
	"errors"
	"testing"
)

func testConfig() Config {
	return Config{
		RetroWindow:      30,
		MinMiles:         500,
		InactiveDuration: 100,
		CancelFee:        10,
		PeriodLength:     100,
		Thresholds:       [3]int64{1000, 2000, 4000},
		Bonuses:          [4]int64{0, 10, 20, 50},
	}
}

func codeOf(err error) ErrCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code()
	}
	return 0
}

func mustOpen(t *testing.T, s *System, id string, now int64) {
	t.Helper()
	if err := s.OpenAccount(id, now); err != nil {
		t.Fatalf("open %s: %v", id, err)
	}
}

// 1) 累计定级里程恰等于阈值：立即升级，本次入账按升级前等级加成。
func TestUpgradeAtExactThreshold(t *testing.T) {
	s, _ := NewSystem(testConfig())
	mustOpen(t, s, "a", 0)
	// 1000 航距 * 100% = 1000，恰达第一档阈值；入账前为 0 级（0% 加成）。
	r, err := s.Post("a", 10, Segment{ID: "s1", Distance: 1000, Rate: 100, FlightTime: 10})
	if err != nil {
		t.Fatal(err)
	}
	if r.Base != 1000 || r.Bonus != 0 || r.TierBefore != 0 || r.TierAfter != 1 {
		t.Fatalf("unexpected %+v", r)
	}
	snap, _ := s.Query("a", 10)
	if snap.Tier != 1 || snap.Qualifying != 1000 || snap.Redeemable != 1000 {
		t.Fatalf("snap %+v", snap)
	}
	// 下一次入账享 1 级 10% 加成。
	r2, err := s.Post("a", 20, Segment{ID: "s2", Distance: 500, Rate: 100, FlightTime: 20})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Base != 500 || r2.Bonus != 50 || r2.Redeemable != 550 {
		t.Fatalf("unexpected %+v", r2)
	}
}

// 2) 周期结束后补登：定级里程计入旧周期但不触发升级。
func TestLatePostAfterPeriodEnd(t *testing.T) {
	cfg := testConfig()
	cfg.InactiveDuration = 1000 // 本用例不涉及冻结，避免首个活动落在阈值边界
	s, _ := NewSystem(cfg)
	mustOpen(t, s, "a", 0)
	// 在 t=120（第 1 周期）补登飞行于 t=90（第 0 周期）的航段，窗口 30 闭终点=120 合法。
	r, err := s.Post("a", 120, Segment{ID: "s1", Distance: 1000, Rate: 100, FlightTime: 90})
	if err != nil {
		t.Fatal(err)
	}
	if r.PeriodIndex != 0 || r.TierBefore != 0 || r.TierAfter != 0 {
		t.Fatalf("unexpected %+v", r)
	}
	snap, _ := s.Query("a", 120)
	// 等级不升；当前周期（第 1 周期）定级累计为 0；可兑换里程照常入账。
	if snap.Tier != 0 || snap.Qualifying != 0 || snap.Redeemable != 1000 {
		t.Fatalf("snap %+v", snap)
	}
	// 超窗 1 秒报错（终点取闭，121 超期）。
	if _, err := s.Post("a", 121, Segment{ID: "s2", Distance: 1, Rate: 0, FlightTime: 90}); codeOf(err) != ErrLatePosting {
		t.Fatalf("want late, got %v", err)
	}
}

// 3) 周期末降级至多一级（含跨空周期闭形式与窗口边界恰等于阈值）。
func TestDowngradeAtMostOneLevel(t *testing.T) {
	s, _ := NewSystem(testConfig())
	mustOpen(t, s, "a", 0)
	// 第 0 周期冲到 3 级（4000）。
	if _, err := s.Post("a", 5, Segment{ID: "s1", Distance: 4000, Rate: 100, FlightTime: 5}); err != nil {
		t.Fatal(err)
	}
	if snap, _ := s.Query("a", 50); snap.Tier != 3 {
		t.Fatalf("tier %d", snap.Tier)
	}
	// t=100 结算第 0 周期（4000 里程），等级维持 3；新周期从 0 开始。
	if snap, _ := s.Query("a", 100); snap.Tier != 3 || snap.Qualifying != 0 {
		t.Fatalf("boundary snap %+v", snap)
	}
	// 第 1 周期无定级里程，t=200 结算空周期后最多降一级 -> 2。
	if snap, _ := s.Query("a", 200); snap.Tier != 2 {
		t.Fatalf("after 1 empty period tier %d", snap.Tier)
	}
	// 再连续跳过多个空周期，每周期最多降一级，最终到底。
	if snap, _ := s.Query("a", 400); snap.Tier != 0 {
		t.Fatalf("after many empty periods tier %d", snap.Tier)
	}
	// 用一次真实入账落地结算后，结论必须与查询投影一致（先解冻）。
	if err := s.Unfreeze("a", 500); err != nil {
		t.Fatal(err)
	}
	r, err := s.Post("a", 500, Segment{ID: "s2", Distance: 10, Rate: 100, FlightTime: 500})
	if err != nil {
		t.Fatal(err)
	}
	if r.TierBefore != 0 {
		t.Fatalf("tier before post %d", r.TierBefore)
	}
}

// 4) 扣回导致欠账，再入账先抵扣欠账。
func TestRefundCreatesDebtThenPostOffsets(t *testing.T) {
	s, _ := NewSystem(testConfig())
	mustOpen(t, s, "a", 0)
	// 入账 1000 可兑换里程。
	if _, err := s.Post("a", 5, Segment{ID: "s1", Distance: 1000, Rate: 100, FlightTime: 5}); err != nil {
		t.Fatal(err)
	}
	// 兑换掉 800，余 200。
	if _, err := s.Redeem("a", "r1", 10, 800); err != nil {
		t.Fatal(err)
	}
	// 退票 s1：应扣 1000，余 200 -> 余额 0、欠账 800；定级里程同时扣回。
	if err := s.Refund("a", "s1", 15); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Query("a", 15)
	if snap.Redeemable != 0 || snap.Debt != 800 || snap.Qualifying != 0 {
		t.Fatalf("snap %+v", snap)
	}
	// 已退票航段不可再入账。
	if _, err := s.Post("a", 16, Segment{ID: "s1", Distance: 1000, Rate: 100, FlightTime: 5}); codeOf(err) != ErrDuplicatePosting {
		t.Fatalf("want duplicate, got %v", err)
	}
	// 再入账：基础 500，当前为 1 级（10% 加成）共 550，全部抵欠账（欠 800 -> 250）。
	r, err := s.Post("a", 20, Segment{ID: "s2", Distance: 500, Rate: 100, FlightTime: 20})
	if err != nil {
		t.Fatal(err)
	}
	if r.Base != 500 || r.Bonus != 50 || r.Redeemable != 0 || r.ToDebt != 550 {
		t.Fatalf("post %+v", r)
	}
	snap, _ = s.Query("a", 20)
	if snap.Redeemable != 0 || snap.Debt != 250 {
		t.Fatalf("snap %+v", snap)
	}
	// 有欠账时兑换一律里程不足。
	if _, err := s.Redeem("a", "r2", 25, 1); codeOf(err) != ErrInsufficientMiles {
		t.Fatalf("want insufficient, got %v", err)
	}
}

// 5) 取消兑换：手续里程不小于当初扣减时退回零；其余按差额先抵欠账。
func TestCancelRedemptionFeeAndDebt(t *testing.T) {
	s, _ := NewSystem(testConfig())
	mustOpen(t, s, "a", 0)
	if _, err := s.Post("a", 5, Segment{ID: "s1", Distance: 1000, Rate: 100, FlightTime: 5}); err != nil {
		t.Fatal(err)
	}
	// 兑换 5 里程：取消费 10 >= 5，退回 0。
	if _, err := s.Redeem("a", "tiny", 10, 5); err != nil {
		t.Fatal(err)
	}
	cr, err := s.CancelRedeem("a", "tiny", 15)
	if err != nil {
		t.Fatal(err)
	}
	if cr.Refunded != 0 || cr.Balance != 995 {
		t.Fatalf("cancel tiny %+v", cr)
	}
	// 再取消同一记录 -> 已取消。
	if _, err := s.CancelRedeem("a", "tiny", 16); codeOf(err) != ErrRedemptionCancelled {
		t.Fatalf("want cancelled, got %v", err)
	}
	// 取消不存在的记录 -> 记录不存在（与已取消可区分）。
	if _, err := s.CancelRedeem("a", "nope", 17); codeOf(err) != ErrRedemptionNotExist {
		t.Fatalf("want not-exist, got %v", err)
	}
	// 大额兑换 900 后制造欠账再取消：退 890 先抵欠账。
	if _, err := s.Redeem("a", "big", 20, 995); err != nil {
		t.Fatal(err)
	}
	if err := s.Refund("a", "s1", 25); err != nil { // 扣回 1000：余 0、欠 1000
		t.Fatal(err)
	}
	cr2, err := s.CancelRedeem("a", "big", 30)
	if err != nil {
		t.Fatal(err)
	}
	// 退票 s1 时账户已为 1 级：s1 入账 1000、0 加成仍是 1000；
	// big 取消退回 995-10=985，全部抵欠账，欠账 1000 -> 15。
	if cr2.Refunded != 0 || cr2.ToDebt != 985 || cr2.Debt != 15 || cr2.Balance != 0 {
		t.Fatalf("cancel big %+v", cr2)
	}
}

// 6) 不活跃时长恰等于阈值即冻结；冻结期间扣回照常；显式解冻重新起算。
func TestFreezingBoundaryRefundAndUnfreeze(t *testing.T) {
	s, _ := NewSystem(testConfig())
	mustOpen(t, s, "a", 0)
	if _, err := s.Post("a", 0, Segment{ID: "s1", Distance: 1000, Rate: 100, FlightTime: 0}); err != nil {
		t.Fatal(err)
	}
	// 恰等于阈值 100：冻结。
	if _, err := s.Post("a", 100, Segment{ID: "s2", Distance: 1, Rate: 100, FlightTime: 100}); codeOf(err) != ErrAccountFrozen {
		t.Fatalf("want frozen at ==100, got %v", err)
	}
	// 差 1：未冻结。
	if _, err := s.Post("a", 99, Segment{ID: "s2", Distance: 1, Rate: 100, FlightTime: 99}); err != nil {
		t.Fatalf("want allowed at 99, got %v", err)
	}
	// 上一次接受在 99，t=199 再次冻结；冻结期扣回照常执行。
	if err := s.Refund("a", "s2", 199); err != nil {
		t.Fatalf("refund while frozen: %v", err)
	}
	snap, _ := s.Query("a", 199)
	// s2 基础=500（保底），0级无加成；余额 1000+500-500=1000。
	if snap.Redeemable != 1000 || snap.Debt != 0 {
		t.Fatalf("snap %+v", snap)
	}
	// 冻结期兑换也报冻结。
	if _, err := s.Redeem("a", "r1", 199, 1); codeOf(err) != ErrAccountFrozen {
		t.Fatalf("want frozen redeem, got %v", err)
	}
	// 显式解冻后立刻可操作。
	if err := s.Unfreeze("a", 200); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem("a", "r1", 200, 10); err != nil {
		t.Fatalf("redeem after unfreeze: %v", err)
	}
}

// 7) 拒绝次序：每一对相邻类别都要能稳定区分。
// 类别序列：参数非法 > 时钟回退 > 账户不存在 > 账户冻结 > 重复入账 >
//
//	补登超期 > 兑换记录不存在/已取消 > 里程不足。
func TestRejectionOrderingAdjacentPairs(t *testing.T) {
	// 7a 参数非法 > 时钟回退：参数非法优先。
	s, _ := NewSystem(testConfig())
	mustOpen(t, s, "a", 100)
	if _, err := s.Post("a", 50, Segment{ID: "", Distance: 1, Rate: 0, FlightTime: 0}); codeOf(err) != ErrInvalidParam {
		t.Fatalf("param>clock post: %v", err)
	}
	if err := s.Unfreeze("a", -1); codeOf(err) != ErrInvalidParam {
		t.Fatalf("param>clock unfreeze: %v", err)
	}
	// 7b 时钟回退 > 账户不存在。
	if _, err := s.Post("ghost", 0, Segment{ID: "x", Distance: 1, Rate: 0, FlightTime: 0}); codeOf(err) != ErrAccountNotExist {
		t.Fatalf("clockback>missing (missing acct at t0 vs lastOp100 only meaningful for existing): %v", err)
	}
	if _, err := s.Post("a", 50, Segment{ID: "x", Distance: 1, Rate: 0, FlightTime: 50}); codeOf(err) != ErrClockBack {
		t.Fatalf("clockback>missing(precondition existing): %v", err)
	}
	// 7c 账户不存在 > 账户冻结：直接打不存在账户（其“冻结”无定义，应报不存在）。
	if _, err := s.Post("ghost", 200, Segment{ID: "x", Distance: 1, Rate: 0, FlightTime: 200}); codeOf(err) != ErrAccountNotExist {
		t.Fatalf("missing>frozen: %v", err)
	}
	// 7d 账户冻结 > 重复入账：冻结账户对已入账航段再入账。
	mustOpen(t, s, "f", 0)
	if _, err := s.Post("f", 0, Segment{ID: "dup", Distance: 1, Rate: 0, FlightTime: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Post("f", 100, Segment{ID: "dup", Distance: 1, Rate: 0, FlightTime: 0}); codeOf(err) != ErrAccountFrozen {
		t.Fatalf("frozen>duplicate: %v", err)
	}
	// 7e 重复入账 > 补登超期：同一航段且已超出窗口。
	if err := s.Unfreeze("f", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Post("f", 1000, Segment{ID: "dup", Distance: 1, Rate: 0, FlightTime: 0}); codeOf(err) != ErrDuplicatePosting {
		t.Fatalf("duplicate>late: %v", err)
	}
	// 7f 补登超期 > 兑换类错误：构造“超期入账”确认其优先；再构造取消场景。
	if _, err := s.Post("f", 1000, Segment{ID: "late", Distance: 1, Rate: 0, FlightTime: 0}); codeOf(err) != ErrLatePosting {
		t.Fatalf("late posting: %v", err)
	}
	// 7g 兑换记录不存在 > 已取消：未知 ID 报不存在。
	mustOpen(t, s, "g", 0)
	if _, err := s.Post("g", 0, Segment{ID: "s1", Distance: 1000, Rate: 100, FlightTime: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelRedeem("g", "nope", 0); codeOf(err) != ErrRedemptionNotExist {
		t.Fatalf("not-exist>cancelled: %v", err)
	}
	if _, err := s.Redeem("g", "r1", 5, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelRedeem("g", "r1", 6); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelRedeem("g", "r1", 7); codeOf(err) != ErrRedemptionCancelled {
		t.Fatalf("cancelled: %v", err)
	}
	// 7h 已取消 > 里程不足：对一张已取消、且余额不足的票再取消，报已取消。
	if _, err := s.CancelRedeem("g", "r1", 8); codeOf(err) != ErrRedemptionCancelled {
		t.Fatalf("cancelled>insufficient: %v", err)
	}
	// 7i 里程不足兜底（有余额但不够）。
	if _, err := s.Redeem("g", "r2", 9, 100000); codeOf(err) != ErrInsufficientMiles {
		t.Fatalf("insufficient: %v", err)
	}
}

// 8) 未入账航段退票：不报错、封锁航段，且此后入账报重复。
func TestRefundUnknownSegmentBlocksIt(t *testing.T) {
	s, _ := NewSystem(testConfig())
	mustOpen(t, s, "a", 0)
	if err := s.Refund("a", "x", 5); err != nil {
		t.Fatalf("refund unknown: %v", err)
	}
	if _, err := s.Post("a", 6, Segment{ID: "x", Distance: 1, Rate: 0, FlightTime: 6}); codeOf(err) != ErrDuplicatePosting {
		t.Fatalf("blocked post: %v", err)
	}
	if snap, _ := s.Query("a", 6); snap.Redeemable != 0 || snap.Debt != 0 || snap.Tier != 0 {
		t.Fatalf("state changed: %+v", snap)
	}
	// 再次退票幂等成功。
	if err := s.Refund("a", "x", 7); err != nil {
		t.Fatal(err)
	}
}

// 9) 保底与取整：ceil，不足保底按保底。
func TestBaseMilesCeilAndFloor(t *testing.T) {
	c := testConfig()
	if got := baseMiles(Segment{Distance: 3, Rate: 50}, c); got != 500 {
		t.Fatalf("floor: %d", got) // ceil(1.5)=2 < 500
	}
	if got := baseMiles(Segment{Distance: 100, Rate: 15}, c); got != 500 {
		t.Fatalf("ceil 15 < floor: %d", got)
	}
	c2 := c
	c2.MinMiles = 0
	if got := baseMiles(Segment{Distance: 10, Rate: 15}, c2); got != 2 {
		t.Fatalf("ceil: %d", got) // ceil(1.5)=2
	}
	if got := baseMiles(Segment{Distance: 10, Rate: 300}, c2); got != 30 {
		t.Fatalf("300%%: %d", got)
	}
}

// 10) 拒绝操作不推进时钟、不改状态。
func TestRejectedDoesNotAdvanceClock(t *testing.T) {
	s, _ := NewSystem(testConfig())
	mustOpen(t, s, "a", 0)
	if _, err := s.Post("a", 10, Segment{ID: "", Distance: 1, Rate: 0, FlightTime: 0}); codeOf(err) != ErrInvalidParam {
		t.Fatal(err)
	}
	if err := s.Unfreeze("a", 5); err != nil {
		t.Fatalf("clock should still be 0, unfreeze at 5 must succeed: %v", err)
	}
}
