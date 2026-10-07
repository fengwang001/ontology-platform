package booking

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{HoldWindow: 5, RefundFullDays: 7, RefundHalfDays: 3}
}

// newTestService 创建服务与一个默认房源 L（价格 100，最短入住 1，间隙 0）。
func newTestService(t *testing.T) *Service {
	t.Helper()
	svc, err := NewService(testConfig())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.CreateListing(0, "L", ListingCfg{NightlyPrice: 100, DefaultMinStay: 1, GapDays: 0}); err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	return svc
}

func addListing(t *testing.T, svc *Service, now int, id string, cfg ListingCfg) {
	t.Helper()
	if err := svc.CreateListing(now, id, cfg); err != nil {
		t.Fatalf("CreateListing %s: %v", id, err)
	}
}

func mustHold(t *testing.T, svc *Service, now int, listing string, ci, co int) string {
	t.Helper()
	id, err := svc.CreateHold(now, listing, ci, co)
	if err != nil {
		t.Fatalf("CreateHold(%d, %s, %d, %d): %v", now, listing, ci, co, err)
	}
	return id
}

func mustConfirm(t *testing.T, svc *Service, now int, listing string, ci, co int) string {
	t.Helper()
	id := mustHold(t, svc, now, listing, ci, co)
	if err := svc.Pay(now, id); err != nil {
		t.Fatalf("Pay(%d, %s): %v", now, id, err)
	}
	return id
}

func wantCode(t *testing.T, op string, err error, want Code) {
	t.Helper()
	if got := CodeOf(err); got != want {
		t.Fatalf("%s: got code %s, want %s (err=%v)", op, got, want, err)
	}
}

func mustAvail(t *testing.T, svc *Service, now int, listing string, ci, co int) {
	t.Helper()
	if err := svc.CheckAvailability(now, listing, ci, co); err != nil {
		t.Fatalf("CheckAvailability(%d, %s, %d, %d): unexpected %v", now, listing, ci, co, err)
	}
}

func mustUnavail(t *testing.T, svc *Service, now int, listing string, ci, co int, want Code) {
	t.Helper()
	wantCode(t, fmt.Sprintf("CheckAvailability(%d, %s, %d, %d)", now, listing, ci, co),
		svc.CheckAvailability(now, listing, ci, co), want)
}

// TestNightOpenClose 验证入住日当天开始占用、退房日当天不占用。
func TestNightOpenClose(t *testing.T) {
	svc := newTestService(t)
	mustConfirm(t, svc, 0, "L", 10, 13)               // 占用夜 10,11,12
	mustUnavail(t, svc, 0, "L", 12, 14, CodeConflict) // 夜 12 被占
	mustAvail(t, svc, 0, "L", 13, 15)                 // 退房日 13 当天可入住
	mustAvail(t, svc, 0, "L", 8, 10)                  // 入住日 10 当天可由前一笔退房
	mustUnavail(t, svc, 0, "L", 9, 11, CodeConflict)  // 夜 10 被占
}

// TestGapZeroAndOne 验证间隙为零允许首尾相接，间隙为一必须空出一整天。
func TestGapZeroAndOne(t *testing.T) {
	svc := newTestService(t)
	mustConfirm(t, svc, 0, "L", 10, 12)
	mustAvail(t, svc, 0, "L", 12, 14) // 间隙 0：退房日 == 入住日

	addListing(t, svc, 0, "G1", ListingCfg{NightlyPrice: 100, DefaultMinStay: 1, GapDays: 1})
	mustConfirm(t, svc, 0, "G1", 10, 12)
	mustUnavail(t, svc, 0, "G1", 12, 14, CodeGap) // 间隙 1：必须空出整天 12
	mustAvail(t, svc, 0, "G1", 13, 15)            // 空出一天后允许
	mustUnavail(t, svc, 0, "G1", 8, 10, CodeGap)  // 前侧同样受间隙约束
	mustAvail(t, svc, 0, "G1", 7, 9)
}

// TestPerDayMinStay 验证按日不同的最短入住在入住日取值。
func TestPerDayMinStay(t *testing.T) {
	svc := newTestService(t)
	if err := svc.SetMinStay(0, "L", 20, 3); err != nil {
		t.Fatalf("SetMinStay: %v", err)
	}
	mustUnavail(t, svc, 0, "L", 20, 22, CodeMinStay) // 入住日 20 适用 3 晚
	mustAvail(t, svc, 0, "L", 20, 23)
	mustAvail(t, svc, 0, "L", 21, 22) // 入住日 21 适用默认值 1
	mustAvail(t, svc, 0, "L", 19, 20) // 入住日 19 适用默认值 1
}

// TestOrphanBoundary 验证孤夜空档恰等最短入住减一时拒绝、恰等最短入住时允许。
func TestOrphanBoundary(t *testing.T) {
	svc := newTestService(t)
	if err := svc.SetDefaultMinStay(0, "L", 3); err != nil {
		t.Fatalf("SetDefaultMinStay: %v", err)
	}
	mustConfirm(t, svc, 0, "L", 10, 13)
	mustConfirm(t, svc, 0, "L", 22, 25)
	// 左侧空档 = 最短入住 - 1 = 2：拒绝
	mustUnavail(t, svc, 0, "L", 15, 18, CodeOrphan)
	// 右侧空档 = 最短入住 - 1 = 2：拒绝
	mustUnavail(t, svc, 0, "L", 17, 20, CodeOrphan)
	// 两侧空档恰等最短入住 3：允许
	mustAvail(t, svc, 0, "L", 16, 19)
	// 空档为零（贴住邻居）：不算孤夜
	mustAvail(t, svc, 0, "L", 13, 16)
	mustAvail(t, svc, 0, "L", 19, 22)
}

// TestBlockGapNotOrphan 验证与封锁相邻产生的空档不受孤夜约束。
func TestBlockGapNotOrphan(t *testing.T) {
	svc := newTestService(t)
	if err := svc.SetDefaultMinStay(0, "L", 3); err != nil {
		t.Fatalf("SetDefaultMinStay: %v", err)
	}
	if _, err := svc.AddBlock(0, "L", 10, 13); err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	// 空档 1 晚 < 最短入住 3，但邻居是封锁：豁免
	mustAvail(t, svc, 0, "L", 14, 17)
	// 对照：邻居是预订时同样的空档构成孤夜
	addListing(t, svc, 0, "L2", ListingCfg{NightlyPrice: 100, DefaultMinStay: 3, GapDays: 0})
	mustConfirm(t, svc, 0, "L2", 10, 13)
	mustUnavail(t, svc, 0, "L2", 14, 17, CodeOrphan)
}

// TestHoldExpiry 验证保留最后一刻可付款、下一刻付款失败并释放日历。
func TestHoldExpiry(t *testing.T) {
	svc := newTestService(t) // H = 5
	id := mustHold(t, svc, 10, "L", 20, 22)
	if err := svc.Pay(15, id); err != nil { // 10+5=15，最后一刻
		t.Fatalf("pay at last valid moment: %v", err)
	}

	id2 := mustHold(t, svc, 20, "L", 30, 32)
	wantCode(t, "pay one tick after expiry", svc.Pay(26, id2), CodeInvalidState) // 20+5=25 < 26
	// 失效保留在 now=26 的查询下已释放日历
	mustAvail(t, svc, 26, "L", 30, 32)
	// 被接受的操作真正清除失效保留，同一夜可再订
	mustHold(t, svc, 26, "L", 30, 32)
}

// TestRefundTiers 验证退款两档边界取等归较宽松的一档。
func TestRefundTiers(t *testing.T) {
	svc := newTestService(t) // P=7, Q=3，价格 100
	// d = 20 - now；金额 200
	b1 := mustConfirm(t, svc, 0, "L", 20, 22)
	r, err := svc.Cancel(13, b1) // d=7 == P：全额
	if err != nil || r != 200 {
		t.Fatalf("cancel at d=P: refund=%d err=%v, want 200", r, err)
	}
	b2 := mustConfirm(t, svc, 13, "L", 20, 22) // 取消后立即可订
	r, err = svc.Cancel(17, b2)                // d=3 == Q：一半
	if err != nil || r != 100 {
		t.Fatalf("cancel at d=Q: refund=%d err=%v, want 100", r, err)
	}
	b3 := mustConfirm(t, svc, 17, "L", 20, 22)
	r, err = svc.Cancel(18, b3) // d=2 < Q：不退
	if err != nil || r != 0 {
		t.Fatalf("cancel at d<Q: refund=%d err=%v, want 0", r, err)
	}
	b4 := mustConfirm(t, svc, 18, "L", 20, 22)
	wantCode(t, "cancel on check-in day", errorFromCancel(svc, 20, b4), CodeInvalidState)
	wantCode(t, "cancel after check-in day", errorFromCancel(svc, 20, b4), CodeInvalidState)
}

func errorFromCancel(svc *Service, now int, id string) error {
	_, err := svc.Cancel(now, id)
	return err
}

// TestModifyBooking 验证修改失败原预订不变、成功只记录差价、只能改一次。
func TestModifyBooking(t *testing.T) {
	svc := newTestService(t)
	a := mustConfirm(t, svc, 0, "L", 10, 12) // 金额 200
	mustConfirm(t, svc, 0, "L", 14, 16)

	wantCode(t, "modify into conflict", errorFromModify(svc, 0, a, 13, 15), CodeConflict)
	// 失败：原预订保持不变
	snap, _ := svc.Snapshot("L")
	bi := findBooking(snap, a)
	if bi.Checkin != 10 || bi.Checkout != 12 || bi.Modified || bi.PriceDiff != 0 {
		t.Fatalf("booking changed after failed modify: %+v", bi)
	}
	// 成功：付款不变，差价 300-200=100 只记录
	diff, err := svc.ModifyBooking(0, a, 20, 23)
	if err != nil || diff != 100 {
		t.Fatalf("modify: diff=%d err=%v, want 100", diff, err)
	}
	snap, _ = svc.Snapshot("L")
	bi = findBooking(snap, a)
	if bi.Checkin != 20 || bi.Checkout != 23 || !bi.Modified || bi.Amount != 200 || bi.PriceDiff != 100 {
		t.Fatalf("booking after modify: %+v", bi)
	}
	wantCode(t, "modify twice", errorFromModify(svc, 0, a, 30, 32), CodeInvalidState)
}

func errorFromModify(svc *Service, now int, id string, ci, co int) error {
	_, err := svc.ModifyBooking(now, id, ci, co)
	return err
}

func findBooking(snap ListingSnapshot, id string) BookingInfo {
	for _, b := range snap.Bookings {
		if b.ID == id {
			return b
		}
	}
	return BookingInfo{}
}

// TestResizeBlock 验证缩短总是允许、延长按新增部分判定冲突。
func TestResizeBlock(t *testing.T) {
	svc := newTestService(t)
	bl, err := svc.AddBlock(0, "L", 10, 12)
	if err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	mustConfirm(t, svc, 0, "L", 15, 18)
	// 延长到 [10,16)：新增部分 [12,16) 与预订 [15,18) 冲突
	wantCode(t, "extend into booking", svc.ResizeBlock(0, "L", bl, 10, 16), CodeConflict)
	snap, _ := svc.Snapshot("L")
	for _, iv := range snap.Intervals {
		if iv.ID == bl && (iv.Start != 10 || iv.End != 12) {
			t.Fatalf("block changed after rejected extend: %+v", iv)
		}
	}
	// 延长到 [10,15)：新增部分不冲突
	if err := svc.ResizeBlock(0, "L", bl, 10, 15); err != nil {
		t.Fatalf("extend to 15: %v", err)
	}
	// 缩短总是允许
	if err := svc.ResizeBlock(0, "L", bl, 11, 13); err != nil {
		t.Fatalf("shorten: %v", err)
	}
	wantCode(t, "shrink to empty", svc.ResizeBlock(0, "L", bl, 11, 11), CodeInvalidParams)
	wantCode(t, "resize missing block", svc.ResizeBlock(0, "L", "BL-999", 0, 1), CodeNotFound)
}

// TestErrorPrecedence 验证按固定次序只报告第一个错误。
func TestErrorPrecedence(t *testing.T) {
	svc := newTestService(t) // lastNow = 0
	mustConfirm(t, svc, 5, "L", 30, 32)

	// 参数非法 优先于 时钟回退、不存在
	_, err := svc.CreateHold(3, "NOPE", 12, 10)
	wantCode(t, "params before rollback/notfound", err, CodeInvalidParams)
	// 时钟回退 优先于 不存在
	_, err = svc.CreateHold(3, "NOPE", 10, 12)
	wantCode(t, "rollback before notfound", err, CodeClockRollback)
	// 不存在 优先于 状态
	wantCode(t, "notfound before state", svc.Pay(6, "BK-999"), CodeNotFound)
	// 状态 优先于 日期不可订：保留已失效时付款报状态错误
	h := mustHold(t, svc, 6, "L", 40, 42) // 有效期至 11
	wantCode(t, "expired hold pay is state error", svc.Pay(12, h), CodeInvalidState)
	// 已取消再操作报状态错误
	c := mustConfirm(t, svc, 6, "L", 50, 52)
	if _, err := svc.Cancel(7, c); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	wantCode(t, "cancel twice", errorFromCancel(svc, 8, c), CodeInvalidState)

	// 不可订内部次序：封锁 < 冲突 < 间隙 < 最短入住 < 孤夜
	addListing(t, svc, 8, "P", ListingCfg{NightlyPrice: 100, DefaultMinStay: 3, GapDays: 2})
	if _, err := svc.AddBlock(8, "P", 10, 12); err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	mustConfirm(t, svc, 8, "P", 14, 17)
	mustConfirm(t, svc, 8, "P", 30, 33)
	mustUnavail(t, svc, 8, "P", 11, 15, CodeBlocked)  // 同时与封锁、预订相交：报封锁
	mustUnavail(t, svc, 8, "P", 13, 16, CodeConflict) // 与预订相交（也违反间隙）：报冲突
	mustUnavail(t, svc, 8, "P", 18, 21, CodeGap)      // 间隙 1 < 2（也不足最短入住）：报间隙
	mustUnavail(t, svc, 8, "P", 22, 23, CodeMinStay)  // 1 晚 < 3（也是孤夜）：报最短入住
	mustUnavail(t, svc, 8, "P", 23, 26, CodeOrphan)   // 右侧空档 30-26-2=2 < 3：报孤夜
}

// TestRejectedLeavesNoTrace 验证被拒绝的操作不改变日历、预订状态与时钟。
func TestRejectedLeavesNoTrace(t *testing.T) {
	svc := newTestService(t)
	mustConfirm(t, svc, 5, "L", 10, 12)
	h := mustHold(t, svc, 5, "L", 20, 22)
	before, _ := svc.Snapshot("L")
	lastNow := svc.LastNow()

	rejected := []func() error{
		func() error { _, e := svc.CreateHold(4, "L", 30, 32); return e },    // 时钟回退
		func() error { _, e := svc.CreateHold(6, "L", 32, 30); return e },    // 参数非法
		func() error { _, e := svc.CreateHold(6, "NOPE", 30, 32); return e }, // 不存在
		func() error { return svc.Pay(6, "BK-999") },                         // 预订不存在
		func() error { _, e := svc.Cancel(6, h); return e },                  // 保留不可取消
		func() error { _, e := svc.CreateHold(6, "L", 11, 13); return e },    // 冲突
		func() error { _, e := svc.AddBlock(6, "L", 10, 12); return e },      // 封锁与预订冲突
		func() error { return svc.CheckAvailability(6, "L", 11, 13) },        // 查询不推进时钟
	}
	for i, op := range rejected {
		if err := op(); err == nil {
			t.Fatalf("rejected op %d unexpectedly succeeded", i)
		}
		after, _ := svc.Snapshot("L")
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("rejected op %d left trace:\nbefore=%+v\nafter=%+v", i, before, after)
		}
		if svc.LastNow() != lastNow {
			t.Fatalf("rejected op %d advanced clock to %d", i, svc.LastNow())
		}
	}
}

// TestConcurrentHolds 验证并发创建保留不会使同一夜被两笔保留占用。
func TestConcurrentHolds(t *testing.T) {
	svc := newTestService(t)
	const workers = 32
	var wg sync.WaitGroup
	results := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = svc.CreateHold(0, "L", 10, 15)
		}(i)
	}
	wg.Wait()
	succeeded := 0
	for _, err := range results {
		if err == nil {
			succeeded++
			continue
		}
		wantCode(t, "concurrent hold", err, CodeConflict)
	}
	if succeeded != 1 {
		t.Fatalf("concurrent holds on same nights: %d succeeded, want exactly 1", succeeded)
	}
	snap, _ := svc.Snapshot("L")
	if len(snap.Intervals) != 1 {
		t.Fatalf("calendar has %d intervals, want 1", len(snap.Intervals))
	}
}

// TestJudgeComplexity 验证可订性判定开销不随历史预订总数线性增长：
// 以 treap 节点访问步数为可验证指标，历史从 1k 增至 100k 时步数只随 log n 增长。
func TestJudgeComplexity(t *testing.T) {
	stepsAt := func(n int) int64 {
		svc, err := NewService(testConfig())
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		if err := svc.CreateListing(0, "L", ListingCfg{NightlyPrice: 1, DefaultMinStay: 1, GapDays: 0}); err != nil {
			t.Fatalf("CreateListing: %v", err)
		}
		for i := 0; i < n; i++ {
			id, err := svc.CreateHold(0, "L", i*4, i*4+1)
			if err != nil {
				t.Fatalf("CreateHold %d: %v", i, err)
			}
			if err := svc.Pay(0, id); err != nil {
				t.Fatalf("Pay %d: %v", i, err)
			}
		}
		if err := svc.CheckAvailability(0, "L", n*4+1, n*4+2); err != nil {
			t.Fatalf("CheckAvailability: %v", err)
		}
		svc.mu.Lock()
		defer svc.mu.Unlock()
		return svc.listings["L"].cal.occ.steps
	}
	s1k := stepsAt(1_000)
	s10k := stepsAt(10_000)
	s100k := stepsAt(100_000)
	t.Logf("judge steps: n=1k:%d n=10k:%d n=100k:%d", s1k, s10k, s100k)
	// 线性扫描在 n=100k 时约需 1e5 次访问；树查找应在百次量级内
	if s100k > 256 {
		t.Fatalf("judge steps %d at n=100k exceeds logarithmic bound 256", s100k)
	}
	// 历史增长 100 倍，步数增长不得超过 4 倍（log2(100) ≈ 6.6，树高差远小于线性）
	if s1k > 0 && s100k > 4*s1k {
		t.Fatalf("judge steps grew %d -> %d for 100x history, not logarithmic", s1k, s100k)
	}
	_ = s10k
}

// TestReplayDeterminism 验证相同操作序列重放得到完全相同的日历与退款结果。
func TestReplayDeterminism(t *testing.T) {
	run := func() (ListingSnapshot, []int64) {
		svc, err := NewService(testConfig())
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		var refunds []int64
		addListing(t, svc, 0, "L", ListingCfg{NightlyPrice: 100, DefaultMinStay: 2, GapDays: 1})
		a := mustConfirm(t, svc, 0, "L", 10, 13)
		mustHold(t, svc, 1, "L", 20, 23)
		b := mustConfirm(t, svc, 2, "L", 30, 32)
		if _, err := svc.ModifyBooking(3, b, 34, 37); err != nil {
			t.Fatalf("modify: %v", err)
		}
		r, err := svc.Cancel(4, a)
		if err != nil {
			t.Fatalf("cancel: %v", err)
		}
		refunds = append(refunds, r)
		snap, _ := svc.Snapshot("L")
		return snap, refunds
	}
	s1, r1 := run()
	s2, r2 := run()
	if !reflect.DeepEqual(s1, s2) || !reflect.DeepEqual(r1, r2) {
		t.Fatalf("replay diverged:\n%+v / %v\n%+v / %v", s1, r1, s2, r2)
	}
}
