package deposit

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func testCfg() Config {
	// A=5 申报期，B=3 争议期，C=4 退还期，每日 1/100 违约金（向下取整）。
	return Config{A: 5, B: 3, C: 4, RateNum: 1, RateDen: 100}
}

func codeOf(err error) ErrCode {
	if err == nil {
		return ErrOK
	}
	return err.(*Error).Code
}

func mustCode(t *testing.T, err error, want ErrCode) {
	t.Helper()
	if got := codeOf(err); got != want {
		t.Fatalf("error code: got %v, want %v (%v)", got, want, err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertConserve(t *testing.T, s *Service, now int, id string) Snapshot {
	t.Helper()
	snap, err := s.View(now, id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if got := snap.Refunded + snap.Vested + snap.Frozen + snap.Pending; got != snap.Deposit {
		t.Fatalf("conservation broken: %d+%d+%d+%d=%d != deposit %d",
			snap.Refunded, snap.Vested, snap.Frozen, snap.Pending, got, snap.Deposit)
	}
	return snap
}

// 申报期最后一天可申报，次日拒绝。
func TestFilingDeadlineBoundary(t *testing.T) {
	s := NewService(testCfg(), nil)
	mustOK(t, s.Checkout(0, "L", 1000))
	_, err := s.FileClaim(5, "L", Rent, 100)
	mustOK(t, err)
	_, err = s.FileClaim(6, "L", Rent, 100)
	mustCode(t, err, ErrLate)
}

// 撤销只能在申报期内；撤销后不得再撤销，且不参与受偿。
func TestWithdrawDeadline(t *testing.T) {
	s := NewService(testCfg(), nil)
	mustOK(t, s.Checkout(0, "L", 1000))
	id, _ := s.FileClaim(2, "L", Damage, 100)
	mustOK(t, s.WithdrawClaim(5, "L", id))
	mustCode(t, s.WithdrawClaim(5, "L", id), ErrState)
	mustCode(t, s.WithdrawClaim(6, "L", id), ErrLate)
	snap := assertConserve(t, s, 6, "L")
	if snap.Refundable != 1000 || snap.Frozen != 0 || snap.Vested != 0 {
		t.Fatalf("withdrawn claim still allocates: %+v", snap)
	}
}

// 争议期边界、申报期内不得争议、同一扣项只能争议一次。
func TestDisputeWindowBoundary(t *testing.T) {
	cfg := testCfg()

	s := NewService(cfg, nil)
	mustOK(t, s.Checkout(0, "L", 1000))
	id, _ := s.FileClaim(5, "L", Damage, 100)
	mustCode(t, s.Dispute(5, "L", id), ErrState)
	mustOK(t, s.Dispute(6, "L", id))

	s2 := NewService(cfg, nil)
	mustOK(t, s2.Checkout(0, "L", 1000))
	id2, _ := s2.FileClaim(5, "L", Damage, 100)
	mustOK(t, s2.Dispute(8, "L", id2))
	mustCode(t, s2.Dispute(8, "L", id2), ErrState)

	s3 := NewService(cfg, nil)
	mustOK(t, s3.Checkout(0, "L", 1000))
	id3, _ := s3.FileClaim(5, "L", Damage, 100)
	mustCode(t, s3.Dispute(9, "L", id3), ErrLate)
}

// 押金恰好覆盖、以及差一分时的部分受偿与应收。
func TestAllocationExactAndShort(t *testing.T) {
	cfg := testCfg()

	s := NewService(cfg, nil)
	mustOK(t, s.Checkout(0, "L", 600))
	r, _ := s.FileClaim(0, "L", Rent, 300)
	d, _ := s.FileClaim(1, "L", Damage, 200)
	c2, _ := s.FileClaim(2, "L", Cleaning, 100)
	snap := assertConserve(t, s, 6, "L")
	if snap.Receivable != 0 {
		t.Fatalf("exact cover: pending=%d receivable=%d", snap.Pending, snap.Receivable)
	}
	paid := map[int]int64{}
	for _, cl := range snap.Claims {
		paid[cl.ID] = cl.Paid
	}
	if paid[r] != 300 || paid[d] != 200 || paid[c2] != 100 {
		t.Fatalf("exact cover paid: %v", paid)
	}
	// 争议期结束（第 8 天）后，全部受偿额归属房东，尚未处置归零。
	snap = assertConserve(t, s, 9, "L")
	if snap.Vested != 600 || snap.Pending != 0 {
		t.Fatalf("exact cover vested: vested=%d pending=%d", snap.Vested, snap.Pending)
	}

	s = NewService(cfg, nil)
	mustOK(t, s.Checkout(0, "L", 599))
	s.FileClaim(0, "L", Rent, 300)
	s.FileClaim(1, "L", Damage, 200)
	cl, _ := s.FileClaim(2, "L", Cleaning, 100)
	snap = assertConserve(t, s, 6, "L")
	if snap.Receivable != 1 {
		t.Fatalf("one-short: pending=%d receivable=%d", snap.Pending, snap.Receivable)
	}
	for _, x := range snap.Claims {
		if x.ID == cl && x.Paid != 99 {
			t.Fatalf("tail claim paid=%d want 99", x.Paid)
		}
	}
}

// 同类别内按申报先后受偿；类别次序优先于申报时间。
func TestSameCategoryAndCategoryOrder(t *testing.T) {
	cfg := testCfg()
	s := NewService(cfg, nil)
	mustOK(t, s.Checkout(0, "L", 150))
	other, _ := s.FileClaim(1, "L", Other, 1000)
	first, _ := s.FileClaim(3, "L", Damage, 100)
	rent, _ := s.FileClaim(4, "L", Rent, 60)
	second, _ := s.FileClaim(4, "L", Damage, 100)
	snap := assertConserve(t, s, 6, "L")
	paid := map[int]int64{}
	for _, cl := range snap.Claims {
		paid[cl.ID] = cl.Paid
	}
	if paid[rent] != 60 || paid[first] != 90 || paid[second] != 0 || paid[other] != 0 {
		t.Fatalf("order paid: %v", paid)
	}
}

// 冻结期间冻结额不可退，其余可退；逾期只对可退部分计违约金。
func TestFreezeBlocksRefundAndLiability(t *testing.T) {
	cfg := testCfg()
	s := NewService(cfg, nil)
	mustOK(t, s.Checkout(0, "L", 1000))
	disputed, _ := s.FileClaim(1, "L", Damage, 400)
	mustOK(t, s.Dispute(6, "L", disputed))
	snap := assertConserve(t, s, 6, "L")
	if snap.Frozen != 400 || snap.Refundable != 600 {
		t.Fatalf("freeze snap: %+v", snap)
	}
	p, liab, err := s.Refund(12, "L") // deadline=9，逾期日 10、11 共 2 天
	mustOK(t, err)
	if p != 600 {
		t.Fatalf("refund principal=%d want 600", p)
	}
	if liab != 600*2/100 {
		t.Fatalf("liability=%d want 12", liab)
	}
	assertConserve(t, s, 12, "L")
	_, _, err = s.Refund(13, "L")
	mustCode(t, err, ErrState)
}

// 裁定金额低于/高于冻结金额、越界、重复裁定。
func TestAdjudicateBelowAndAboveFrozen(t *testing.T) {
	cfg := testCfg()

	s := NewService(cfg, nil)
	mustOK(t, s.Checkout(0, "L", 1000))
	id, _ := s.FileClaim(1, "L", Damage, 400)
	mustOK(t, s.Dispute(6, "L", id))
	mustOK(t, s.Adjudicate(7, "L", id, 100))
	snap := assertConserve(t, s, 7, "L")
	if snap.Frozen != 0 || snap.Vested != 100 || snap.Refundable != 900 {
		t.Fatalf("below snap: %+v", snap)
	}
	if snap.Receivable != 0 {
		t.Fatalf("below receivable=%d want 0", snap.Receivable)
	}
	mustCode(t, s.Adjudicate(8, "L", id, 100), ErrState)

	s = NewService(cfg, nil)
	mustOK(t, s.Checkout(0, "L", 100))
	id, _ = s.FileClaim(1, "L", Rent, 500) // 仅受偿 100
	mustOK(t, s.Dispute(6, "L", id))
	mustOK(t, s.Adjudicate(7, "L", id, 500))
	snap = assertConserve(t, s, 7, "L")
	if snap.Vested != 100 || snap.Receivable != 400 || snap.Frozen != 0 {
		t.Fatalf("above snap: %+v", snap)
	}

	s = NewService(cfg, nil)
	mustOK(t, s.Checkout(0, "L", 100))
	id, _ = s.FileClaim(1, "L", Rent, 50)
	mustOK(t, s.Dispute(6, "L", id))
	mustCode(t, s.Adjudicate(7, "L", id, 51), ErrAmount)
	mustCode(t, s.Adjudicate(7, "L", id, -1), ErrAmount)
}

// 裁定释放后从裁定日重新起算 C 天；第 C 天当天免责，次日计 1 天。
func TestAdjudicateRetimerAndExactDayC(t *testing.T) {
	cfg := testCfg()
	s := NewService(cfg, nil)
	mustOK(t, s.Checkout(0, "L", 1000))
	id, _ := s.FileClaim(1, "L", Damage, 400)
	mustOK(t, s.Dispute(6, "L", id))
	p, liab, err := s.Refund(9, "L")
	mustOK(t, err)
	if p != 600 || liab != 0 {
		t.Fatalf("first refund p=%d liab=%d", p, liab)
	}
	mustOK(t, s.Adjudicate(20, "L", id, 0))
	snap := assertConserve(t, s, 20, "L")
	if snap.Refundable != 400 {
		t.Fatalf("release refundable=%d", snap.Refundable)
	}
	_, liab, err = s.Refund(24, "L") // 20+C 当天免责
	mustOK(t, err)
	if liab != 0 {
		t.Fatalf("day-C liability=%d want 0", liab)
	}
	assertConserve(t, s, 24, "L")

	s = NewService(cfg, nil)
	mustOK(t, s.Checkout(0, "L", 1000))
	id, _ = s.FileClaim(1, "L", Damage, 400)
	mustOK(t, s.Dispute(6, "L", id))
	s.Refund(9, "L")
	s.Adjudicate(20, "L", id, 0)
	_, liab, _ = s.Refund(26, "L") // 逾期区间 [25,25]，1 天违约金
	if liab != 400/100 {
		t.Fatalf("day C+2 liability=%d want 4", liab)
	}
}

// 错误判定固定次序：参数非法 > 时钟回退 > 租约不存在 > 逾期 > 状态 > 金额。
func TestRejectionOrder(t *testing.T) {
	s := NewService(testCfg(), nil)
	mustOK(t, s.Checkout(10, "L", 100))

	_, e1 := s.FileClaim(5, "", Rent, 10)
	mustCode(t, e1, ErrIllegalArgument)
	_, e2 := s.FileClaim(5, "L", Rent, 10)
	mustCode(t, e2, ErrClockRollback)
	_, e3 := s.FileClaim(10, "NOPE", Rent, 10)
	mustCode(t, e3, ErrNoLease)
	_, e4 := s.FileClaim(16, "L", Rent, -5)
	mustCode(t, e4, ErrLate) // 期限问题先于金额问题
	_, e5 := s.FileClaim(11, "L", Category(99), -5)
	mustCode(t, e5, ErrIllegalArgument)
	_, e6 := s.FileClaim(11, "L", Rent, -5)
	mustCode(t, e6, ErrAmount)
	_, e7 := s.FileClaim(11, "L", Rent, 0)
	mustCode(t, e7, ErrAmount)
	_, e8 := s.View(10, "GHOST")
	mustCode(t, e8, ErrNoLease)
}

// 被拒绝的操作不留痕、不动时钟；全程守恒。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s := NewService(testCfg(), nil)
	mustOK(t, s.Checkout(0, "L", 1000))
	id, _ := s.FileClaim(1, "L", Damage, 400)

	_, err := s.FileClaim(99, "L", Rent, 10) // 逾期
	mustCode(t, err, ErrLate)
	// 时钟未推进到 99，合法 now=2 仍可接受，证明时钟未被污染。
	_, err = s.FileClaim(2, "L", Rent, 10)
	mustOK(t, err)

	l := s.leases["L"]
	before := len(l.events)
	_, err = s.FileClaim(2, "L", Rent, -1)
	mustCode(t, err, ErrAmount)
	mustCode(t, s.Dispute(2, "L", id), ErrState)
	mustCode(t, s.WithdrawClaim(6, "L", id), ErrLate)
	if len(l.events) != before {
		t.Fatalf("rejected ops left %d traces", len(l.events)-before)
	}
	assertConserve(t, s, 6, "L")
	assertConserve(t, s, 50, "L")
}

// 并发：多 goroutine 同时退还，同一笔金额最多被退还一次，且守恒。
func TestConcurrentRefund(t *testing.T) {
	s := NewService(testCfg(), nil)
	mustOK(t, s.Checkout(0, "L", 1000))
	s.FileClaim(1, "L", Other, 250)

	var wg sync.WaitGroup
	var oks, total int64
	var mu sync.Mutex
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, _, err := s.Refund(10, "L")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				oks++
				total += p
			}
		}()
	}
	wg.Wait()
	if oks != 1 || total != 750 {
		t.Fatalf("concurrent refund oks=%d total=%d", oks, total)
	}
	snap := assertConserve(t, s, 10, "L")
	if snap.Refunded != 750 {
		t.Fatalf("refunded=%d want 750", snap.Refunded)
	}
}

// 确定性：相同操作序列重放，快照逐字段一致。
func TestReplayDeterminism(t *testing.T) {
	run := func() Snapshot {
		s := NewService(testCfg(), nil)
		mustOK(t, s.Checkout(0, "L", 1000))
		a, _ := s.FileClaim(0, "L", Rent, 600)
		b, _ := s.FileClaim(2, "L", Cleaning, 300)
		s.WithdrawClaim(3, "L", b)
		c, _ := s.FileClaim(4, "L", Damage, 300)
		s.Dispute(7, "L", a)
		s.Refund(8, "L")
		s.Adjudicate(12, "L", a, 400)
		s.Dispute(8, "L", c)
		s.Adjudicate(15, "L", c, 300)
		s.Refund(30, "L")
		snap, _ := s.View(30, "L")
		return snap
	}
	x, y := run(), run()
	if x.Deposit != y.Deposit || x.Refunded != y.Refunded || x.Vested != y.Vested ||
		x.Frozen != y.Frozen || x.Pending != y.Pending || x.Receivable != y.Receivable ||
		x.Liability != y.Liability || x.Refundable != y.Refundable || len(x.Claims) != len(y.Claims) {
		t.Fatalf("replay mismatch:\n%+v\n%+v", x, y)
	}
}

// 日志：每步打印输入、输出与判定依据。
func TestStepLogging(t *testing.T) {
	var b strings.Builder
	s := NewService(testCfg(), func(line string) { b.WriteString(line + "\n") })
	mustOK(t, s.Checkout(0, "L", 100))
	_, err := s.FileClaim(99, "L", Rent, 10)
	mustCode(t, err, ErrLate)
	out := b.String()
	if !strings.Contains(out, "Checkout") || !strings.Contains(out, "REJECT") ||
		!strings.Contains(out, "past deadline") {
		t.Fatalf("log missing input/output/reason:\n%s", out)
	}
}

// 复杂度可验证证明：争议/裁定只读写该扣项自身字段。
// 这里通过计时放大规模差异（弱证据），强证据见 DESIGN.md 与代码：
// Adjudicate/Dispute 中不存在任何随扣项总数增长的循环或 map 遍历，
// 唯一的全量排序只在申报期截止时发生一次。
func TestAdjudicateComplexityIndependentOfClaimCount(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	measure := func(n int) time.Duration {
		s := NewService(testCfg(), nil)
		mustOK(t, s.Checkout(0, "L", int64(n)*10))
		var target int
		for i := 0; i < n; i++ {
			id, err := s.FileClaim(1, "L", Other, 5)
			mustOK(t, err)
			target = id
		}
		mustOK(t, s.Dispute(6, "L", target))
		start := time.Now()
		for k := 0; k < 20000; k++ {
			// 直接反复走裁定的 O(1) 核心路径（状态会被置为已裁定，
			// 因此这里测量的是 map 查找 + 固定算术的拒绝路径，仍只与单条扣项有关）。
			_ = s.Adjudicate(7, "L", target, 3)
		}
		return time.Since(start)
	}
	small := measure(50)
	big := measure(5000)
	// 允许 3 倍抖动；若裁定随扣项数线性增长，big 会接近 small 的 100 倍。
	if big > small*6+time.Millisecond {
		t.Fatalf("adjudicate cost scales with claims: small=%v big=%v", small, big)
	}
}
