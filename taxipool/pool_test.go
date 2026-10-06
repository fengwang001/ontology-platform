package taxipool

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func baseConfig() Config {
	return Config{
		Terminals:         map[string]int{"T1": 2, "T2": 2, "T3": 2},
		ArriveLimit:       10,
		TransferLimit:     25,
		NoShowLimit:       2,
		BanDuration:       100,
		ShortTripDistance: 50,
		ReturnLimit:       30,
		VoucherValidity:   60,
		DailyVoucherLimit: 2,
		PriorityCap:       1,
		DayOffsetMinutes:  0,
	}
}

func mustPool(t *testing.T, cfg Config) *Pool {
	t.Helper()
	p, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	return p
}

// mustTrip 让司机完成一次“入池-放行-到达-载客离开”闭环，行程距离为 dist。
func mustTrip(t *testing.T, p *Pool, drv, term string, ts, dist int64) {
	t.Helper()
	if _, err := p.EnterPool(drv, term, ts); err != nil {
		t.Fatalf("enter %s: %v", drv, err)
	}
	if _, err := p.Dispatch(term, ts+1); err != nil {
		t.Fatalf("dispatch %s: %v", term, err)
	}
	if err := p.Arrive(drv, ts+2); err != nil {
		t.Fatalf("arrive %s: %v", drv, err)
	}
	if err := p.CompleteTrip(drv, dist, ts+3); err != nil {
		t.Fatalf("trip %s: %v", drv, err)
	}
}

func mustVoucher(t *testing.T, p *Pool, drv string) VoucherInfo {
	t.Helper()
	info, err := p.VoucherInfo(drv)
	if err != nil {
		t.Fatalf("voucher info %s: %v", drv, err)
	}
	return info
}

// 行程距离恰等于短途阈值算短途，超过则不算。
func TestShortTripDistanceBoundary(t *testing.T) {
	p := mustPool(t, baseConfig())
	mustTrip(t, p, "alpha", "T1", 0, 50) // 距离 == 阈值
	res, err := p.EnterPool("alpha", "T1", 10)
	if err != nil {
		t.Fatalf("enter alpha: %v", err)
	}
	if !res.VoucherIssued || !res.Priority {
		t.Fatalf("dist==threshold should issue voucher and enter priority, got %+v", res)
	}
	mustTrip(t, p, "bravo", "T2", 10, 51) // 距离 == 阈值+1
	res, err = p.EnterPool("bravo", "T2", 20)
	if err != nil {
		t.Fatalf("enter bravo: %v", err)
	}
	if res.VoucherIssued || res.Priority {
		t.Fatalf("dist==threshold+1 should not issue voucher, got %+v", res)
	}
}

// 返回间隔恰等于返回时限算及时，超过则不算。
func TestReturnIntervalBoundary(t *testing.T) {
	p := mustPool(t, baseConfig())
	mustTrip(t, p, "bravo", "T1", 0, 50)       // 离开时刻 3
	mustTrip(t, p, "alpha", "T2", 10, 50)      // 离开时刻 13
	res, err := p.EnterPool("bravo", "T1", 34) // 间隔 31 > 30
	if err != nil {
		t.Fatalf("enter bravo: %v", err)
	}
	if res.VoucherIssued {
		t.Fatalf("interval 31 > limit should not issue voucher, got %+v", res)
	}
	res, err = p.EnterPool("alpha", "T2", 43) // 间隔 30 == 时限
	if err != nil {
		t.Fatalf("enter alpha: %v", err)
	}
	if !res.VoucherIssued || !res.Priority {
		t.Fatalf("interval==limit should issue voucher, got %+v", res)
	}
}

// 入池时刻恰等于凭证过期时刻视为已过期，按普通司机入队。
func TestVoucherExpiryAtEntry(t *testing.T) {
	cfg := baseConfig()
	cfg.Terminals = map[string]int{"T1": 1, "T2": 2}
	p := mustPool(t, cfg)
	if _, err := p.EnterPool("fill", "T1", 0); err != nil { // T1 满
		t.Fatalf("enter fill: %v", err)
	}
	mustTrip(t, p, "bravo", "T2", 0, 50) // 离开时刻 3
	if _, err := p.EnterPool("bravo", "T1", 4); err != ErrQueueFull {
		t.Fatalf("bravo enter full queue: %v", err)
	}
	if info := mustVoucher(t, p, "bravo"); !info.Held || info.IssuedAt != 4 || info.ExpiresAt != 64 {
		t.Fatalf("bravo voucher should survive rejection, got %+v", info)
	}
	mustTrip(t, p, "charlie", "T2", 10, 50) // 离开时刻 13
	if _, err := p.EnterPool("charlie", "T1", 14); err != ErrQueueFull {
		t.Fatalf("charlie enter full queue: %v", err)
	}
	if err := p.Leave("fill", 20); err != nil {
		t.Fatalf("leave fill: %v", err)
	}
	res, err := p.EnterPool("charlie", "T1", 73) // 73 < 过期时刻 74，仍有效
	if err != nil || !res.Priority {
		t.Fatalf("charlie should enter priority before expiry, res=%+v err=%v", res, err)
	}
	if err := p.Leave("charlie", 73); err != nil {
		t.Fatalf("leave charlie: %v", err)
	}
	res, err = p.EnterPool("bravo", "T1", 74) // 74 == 过期时刻 64 之后，已过期
	if err != nil {
		t.Fatalf("enter bravo: %v", err)
	}
	if res.Priority || res.VoucherHeld || res.VoucherIssued {
		t.Fatalf("expired voucher must be treated as normal entry, got %+v", res)
	}
	if info := mustVoucher(t, p, "bravo"); info.Held {
		t.Fatalf("expired voucher should be discarded, got %+v", info)
	}
}

// 每自然日凭证发放恰达上限后不再发放。
func TestDailyVoucherLimit(t *testing.T) {
	p := mustPool(t, baseConfig())       // DailyVoucherLimit = 2
	mustTrip(t, p, "alpha", "T1", 0, 50) // 离开时刻 3
	res, err := p.EnterPool("alpha", "T1", 4)
	if err != nil || !res.VoucherIssued || !res.Priority {
		t.Fatalf("cycle 1 should issue voucher #1, res=%+v err=%v", res, err)
	}
	if _, err := p.Dispatch("T1", 5); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := p.Arrive("alpha", 6); err != nil {
		t.Fatalf("arrive: %v", err)
	}
	if err := p.CompleteTrip("alpha", 50, 7); err != nil {
		t.Fatalf("trip: %v", err)
	}
	res, err = p.EnterPool("alpha", "T1", 8)
	if err != nil || !res.VoucherIssued {
		t.Fatalf("cycle 2 should issue voucher #2 (quota reached exactly), res=%+v err=%v", res, err)
	}
	if info := mustVoucher(t, p, "alpha"); info.DayCount != 2 {
		t.Fatalf("day count should be 2, got %+v", info)
	}
	if _, err := p.Dispatch("T1", 9); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := p.Arrive("alpha", 10); err != nil {
		t.Fatalf("arrive: %v", err)
	}
	if err := p.CompleteTrip("alpha", 50, 11); err != nil {
		t.Fatalf("trip: %v", err)
	}
	res, err = p.EnterPool("alpha", "T1", 12)
	if err != nil {
		t.Fatalf("enter: %v", err)
	}
	if res.VoucherIssued || res.Priority {
		t.Fatalf("quota exhausted, cycle 3 must not issue voucher, got %+v", res)
	}
}

// 优先名额已满时凭证不消耗但记一次受限，第二次仍受限则作废。
func TestPriorityCapStrikes(t *testing.T) {
	p := mustPool(t, baseConfig()) // PriorityCap = 1
	mustTrip(t, p, "alpha", "T1", 0, 50)
	res, err := p.EnterPool("alpha", "T1", 4)
	if err != nil || !res.Priority {
		t.Fatalf("alpha should take the only priority slot, res=%+v err=%v", res, err)
	}
	mustTrip(t, p, "bravo", "T2", 10, 50)
	res, err = p.EnterPool("bravo", "T1", 14) // 名额已满：普通入队，凭证保留
	if err != nil {
		t.Fatalf("enter bravo: %v", err)
	}
	if res.Priority || !res.VoucherHeld {
		t.Fatalf("cap full: expect normal entry with voucher kept, got %+v", res)
	}
	if info := mustVoucher(t, p, "bravo"); !info.Held || info.Strikes != 1 {
		t.Fatalf("voucher should have 1 strike, got %+v", info)
	}
	if pos, _ := p.Position("bravo"); pos != 1 {
		t.Fatalf("bravo should be behind alpha, pos=%d", pos)
	}
	if err := p.Leave("bravo", 15); err != nil { // 离队不消耗凭证
		t.Fatalf("leave bravo: %v", err)
	}
	if info := mustVoucher(t, p, "bravo"); !info.Held || info.Strikes != 1 {
		t.Fatalf("leave must not consume voucher, got %+v", info)
	}
	res, err = p.EnterPool("bravo", "T1", 16) // 名额仍满：第二次受限，凭证作废
	if err != nil {
		t.Fatalf("re-enter bravo: %v", err)
	}
	if res.Priority || res.VoucherHeld {
		t.Fatalf("second capped attempt should void voucher, got %+v", res)
	}
	if info := mustVoucher(t, p, "bravo"); info.Held {
		t.Fatalf("voucher should be voided after 2 strikes, got %+v", info)
	}
	snap, _ := p.QueueSnapshot("T1")
	if len(snap) != 2 || snap[0] != "alpha" || snap[1] != "bravo" {
		t.Fatalf("queue order wrong: %v", snap)
	}
}

// 入池被拒绝（满员、禁入）时凭证不消耗，时效按发放时刻继续计算。
func TestVoucherKeptOnReject(t *testing.T) {
	// 满员拒绝
	p := mustPool(t, baseConfig())
	if _, err := p.EnterPool("x", "T1", 0); err != nil {
		t.Fatalf("enter x: %v", err)
	}
	if _, err := p.EnterPool("y", "T1", 1); err != nil {
		t.Fatalf("enter y: %v", err)
	}
	mustTrip(t, p, "bravo", "T2", 5, 50)
	if _, err := p.EnterPool("bravo", "T1", 9); err != ErrQueueFull {
		t.Fatalf("expect ErrQueueFull, got %v", err)
	}
	if info := mustVoucher(t, p, "bravo"); !info.Held || info.IssuedAt != 9 {
		t.Fatalf("voucher must survive full-queue rejection, got %+v", info)
	}
	if c := p.Clock(); c != 8 { // 被拒操作不推进时钟
		t.Fatalf("rejected op must not advance clock, got %d", c)
	}
	if _, err := p.Dispatch("T1", 10); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	res, err := p.EnterPool("bravo", "T1", 11)
	if err != nil || !res.Priority || res.VoucherHeld {
		t.Fatalf("held voucher should be consumed on priority entry, res=%+v err=%v", res, err)
	}

	// 禁入拒绝（PriorityCap=0 使凭证在成功入池时也不被消耗）
	cfg := baseConfig()
	cfg.PriorityCap = 0
	cfg.NoShowLimit = 1
	p2 := mustPool(t, cfg)
	mustTrip(t, p2, "alpha", "T1", 0, 50)
	res, err = p2.EnterPool("alpha", "T1", 4) // 名额恒满：普通入队，凭证记 1 次受限
	if err != nil || res.Priority || !res.VoucherHeld {
		t.Fatalf("expect normal entry with held voucher, res=%+v err=%v", res, err)
	}
	if _, err := p2.Dispatch("T1", 5); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := p2.NoShow("alpha", 16); err != nil { // 爽约 1 次即禁入至 116
		t.Fatalf("noshow: %v", err)
	}
	if _, err := p2.EnterPool("alpha", "T1", 20); err != ErrDriverBanned {
		t.Fatalf("expect ErrDriverBanned, got %v", err)
	}
	if info := mustVoucher(t, p2, "alpha"); !info.Held || info.Strikes != 1 {
		t.Fatalf("voucher must survive banned rejection, got %+v", info)
	}
	res, err = p2.EnterPool("alpha", "T1", 116) // 禁入期恰结束可入池；凭证已过期
	if err != nil {
		t.Fatalf("enter at ban end: %v", err)
	}
	if res.Priority || res.VoucherHeld {
		t.Fatalf("voucher expired during ban, expect normal entry, got %+v", res)
	}
}

// 到达恰在时限到期那一刻视为逾期；逾期后按爽约处理。
func TestArriveDeadlineBoundary(t *testing.T) {
	p := mustPool(t, baseConfig())
	if _, err := p.EnterPool("alpha", "T1", 0); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if _, err := p.EnterPool("bravo", "T1", 1); err != nil {
		t.Fatalf("enter: %v", err)
	}
	d1, err := p.Dispatch("T1", 2)
	if err != nil || d1.DriverID != "alpha" || d1.Deadline != 12 {
		t.Fatalf("dispatch alpha: %+v err=%v", d1, err)
	}
	d2, err := p.Dispatch("T1", 3)
	if err != nil || d2.DriverID != "bravo" || d2.Deadline != 13 {
		t.Fatalf("dispatch bravo: %+v err=%v", d2, err)
	}
	if err := p.Arrive("alpha", 11); err != nil { // 11 < 12，按时
		t.Fatalf("arrive before deadline: %v", err)
	}
	if err := p.Arrive("bravo", 13); err != ErrArrivalOverdue { // 恰到期视为逾期
		t.Fatalf("arrive at deadline should be overdue, got %v", err)
	}
	info, _ := p.InspectDriver("bravo")
	if info.State != "dispatched" || info.Deadline != 13 {
		t.Fatalf("rejected arrival must not change state, got %+v", info)
	}
	if c := p.Clock(); c != 11 {
		t.Fatalf("rejected op must not advance clock, got %d", c)
	}
	if err := p.NoShow("bravo", 13); err != nil {
		t.Fatalf("noshow at deadline: %v", err)
	}
	info, _ = p.InspectDriver("bravo")
	if info.State != "idle" || info.NoShows != 1 {
		t.Fatalf("noshow should remove driver and count, got %+v", info)
	}
	if err := p.Arrive("bravo", 14); err != ErrDriverNotDispatched {
		t.Fatalf("arrive after noshow: %v", err)
	}
}

// 爽约恰达禁入次数即禁入，禁入期恰结束那一刻可入池。
func TestBanBoundary(t *testing.T) {
	p := mustPool(t, baseConfig()) // NoShowLimit=2, BanDuration=100
	if _, err := p.EnterPool("alpha", "T1", 0); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if _, err := p.Dispatch("T1", 1); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := p.NoShow("alpha", 11); err != nil { // 爽约 1 次，未达上限
		t.Fatalf("noshow 1: %v", err)
	}
	if info, _ := p.InspectDriver("alpha"); info.BannedUntil != 0 {
		t.Fatalf("1 noshow < limit should not ban, got %+v", info)
	}
	if _, err := p.EnterPool("alpha", "T1", 12); err != nil {
		t.Fatalf("re-enter: %v", err)
	}
	if _, err := p.Dispatch("T1", 13); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := p.NoShow("alpha", 23); err != nil { // 爽约 2 次，恰达上限
		t.Fatalf("noshow 2: %v", err)
	}
	if info, _ := p.InspectDriver("alpha"); info.BannedUntil != 123 {
		t.Fatalf("ban should last until 123, got %+v", info)
	}
	if _, err := p.EnterPool("alpha", "T1", 122); err != ErrDriverBanned {
		t.Fatalf("expect ErrDriverBanned, got %v", err)
	}
	if _, err := p.EnterPool("alpha", "T1", 123); err != nil { // 恰结束可入池
		t.Fatalf("enter at ban end should succeed: %v", err)
	}
}

// 调剂：备选队首为优先司机不参与；并列取候机楼标识字典序最小者；调剂时限生效。
func TestTransferRules(t *testing.T) {
	// 优先队首不参与调剂
	p := mustPool(t, baseConfig())
	mustTrip(t, p, "alpha", "T2", 0, 50)
	if res, err := p.EnterPool("alpha", "T2", 4); err != nil || !res.Priority {
		t.Fatalf("alpha priority head: %+v %v", res, err)
	}
	if _, err := p.EnterPool("bravo", "T3", 5); err != nil {
		t.Fatalf("enter bravo: %v", err)
	}
	res, err := p.Dispatch("T1", 10)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.Transferred || res.DriverID != "bravo" || res.FromTerminal != "T3" {
		t.Fatalf("should transfer bravo from T3 (T2 head is priority), got %+v", res)
	}
	if res.Deadline != 35 { // 调剂时限 10+25
		t.Fatalf("transfer deadline should use transfer limit, got %d", res.Deadline)
	}
	// 全部备选队首都是优先司机时无车可放行
	if _, err := p.Dispatch("T1", 11); err != ErrNoCarAvailable {
		t.Fatalf("priority head must not be transferred, got %v", err)
	}

	// 并列时刻取候机楼标识字典序最小者
	p2 := mustPool(t, baseConfig())
	if _, err := p2.EnterPool("alpha", "T2", 0); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if _, err := p2.EnterPool("bravo", "T3", 0); err != nil {
		t.Fatalf("enter: %v", err)
	}
	res, err = p2.Dispatch("T1", 1)
	if err != nil || res.DriverID != "alpha" || res.FromTerminal != "T2" {
		t.Fatalf("tie should pick smallest terminal id, got %+v err=%v", res, err)
	}

	// 队首入池时刻最早者优先
	p3 := mustPool(t, baseConfig())
	if _, err := p3.EnterPool("alpha", "T3", 3); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if _, err := p3.EnterPool("bravo", "T2", 5); err != nil {
		t.Fatalf("enter: %v", err)
	}
	res, err = p3.Dispatch("T1", 10)
	if err != nil || res.DriverID != "alpha" || res.FromTerminal != "T3" {
		t.Fatalf("earliest head ts should win, got %+v err=%v", res, err)
	}
}

// 同一时刻入池按司机标识字典序；优先司机恒在普通司机之前。
func TestSameTimestampOrdering(t *testing.T) {
	cfg := baseConfig()
	cfg.Terminals = map[string]int{"T1": 5, "T2": 2}
	p := mustPool(t, cfg)
	for _, e := range []struct {
		id, term string
		ts       int64
	}{
		{"charlie", "T1", 3},
		{"delta", "T1", 5},
		{"alpha", "T1", 5},
	} {
		if _, err := p.EnterPool(e.id, e.term, e.ts); err != nil {
			t.Fatalf("enter %s: %v", e.id, err)
		}
	}
	snap, _ := p.QueueSnapshot("T1")
	if want := []string{"charlie", "alpha", "delta"}; !equalStrings(snap, want) {
		t.Fatalf("same-ts order should be by driver id, got %v want %v", snap, want)
	}
	mustTrip(t, p, "bravo", "T2", 10, 50)
	if res, err := p.EnterPool("bravo", "T1", 14); err != nil || !res.Priority {
		t.Fatalf("bravo priority: %+v %v", res, err)
	}
	snap, _ = p.QueueSnapshot("T1")
	if want := []string{"bravo", "charlie", "alpha", "delta"}; !equalStrings(snap, want) {
		t.Fatalf("priority driver must head the queue, got %v want %v", snap, want)
	}
	if pos, _ := p.Position("delta"); pos != 3 {
		t.Fatalf("delta position should be 3, got %d", pos)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 错误只报次序最靠前的一类：参数非法 < 时钟回退 < 候机楼不存在 < 司机不存在 < 其余。
func TestErrorPrecedence(t *testing.T) {
	cfg := baseConfig()
	cfg.NoShowLimit = 1
	p := mustPool(t, cfg)
	if _, err := p.EnterPool("", "", -1); err != ErrInvalidParam {
		t.Fatalf("empty ids + negative ts should be ErrInvalidParam, got %v", err)
	}
	if _, err := p.Dispatch("T3", 0); err != ErrNoCarAvailable {
		t.Fatalf("dispatch empty pool, got %v", err)
	}
	if _, err := p.EnterPool("alpha", "T1", 10); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if _, err := p.EnterPool("alpha", "NOPE", 5); err != ErrClockRollback {
		t.Fatalf("rollback beats terminal-not-found, got %v", err)
	}
	if _, err := p.EnterPool("alpha", "NOPE", 20); err != ErrTerminalNotFound {
		t.Fatalf("unknown terminal, got %v", err)
	}
	if _, err := p.EnterPool("alpha", "T2", 20); err != ErrDriverInQueue {
		t.Fatalf("duplicate enter, got %v", err)
	}
	if _, err := p.EnterPool("bravo", "T1", 21); err != nil {
		t.Fatalf("enter bravo: %v", err)
	}
	if _, err := p.EnterPool("charlie", "T1", 22); err != ErrQueueFull {
		t.Fatalf("full queue, got %v", err)
	}
	if err := p.Arrive("ghost", 23); err != ErrDriverNotFound {
		t.Fatalf("arrive unknown, got %v", err)
	}
	if err := p.Arrive("alpha", 23); err != ErrDriverNotDispatched {
		t.Fatalf("arrive queued driver, got %v", err)
	}
	if err := p.Leave("ghost", 23); err != ErrDriverNotFound {
		t.Fatalf("leave unknown, got %v", err)
	}
	if _, err := p.Dispatch("NOPE", 23); err != ErrTerminalNotFound {
		t.Fatalf("dispatch unknown terminal, got %v", err)
	}
	res, err := p.Dispatch("T3", 23) // T3 为空，从 T1 调剂
	if err != nil || !res.Transferred || res.DriverID != "alpha" {
		t.Fatalf("dispatch T3 should transfer alpha, got %+v err=%v", res, err)
	}
	if res.Deadline != 48 {
		t.Fatalf("transfer deadline should be 23+25=48, got %d", res.Deadline)
	}
	// 禁入与候机楼不存在同时成立时报候机楼不存在
	if err := p.NoShow("alpha", 48); err != nil {
		t.Fatalf("noshow: %v", err)
	}
	if _, err := p.EnterPool("alpha", "NOPE", 50); err != ErrTerminalNotFound {
		t.Fatalf("terminal-not-found beats banned, got %v", err)
	}
	if _, err := p.EnterPool("alpha", "T1", 50); err != ErrDriverBanned {
		t.Fatalf("banned, got %v", err)
	}
	// 未逾期不能按爽约处理
	if _, err := p.EnterPool("delta", "T2", 50); err != nil {
		t.Fatalf("enter delta: %v", err)
	}
	if _, err := p.Dispatch("T2", 51); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := p.NoShow("delta", 55); err != ErrInvalidParam {
		t.Fatalf("noshow before deadline, got %v", err)
	}
	// 放行后到达之前不可离队（司机不在队列中）
	if err := p.Leave("delta", 55); err != ErrDriverNotFound {
		t.Fatalf("leave while dispatched, got %v", err)
	}
	if err := p.CompleteTrip("delta", 10, 55); err != ErrInvalidParam {
		t.Fatalf("trip before arrive, got %v", err)
	}
	if _, err := p.Position("ghost"); err != ErrDriverNotFound {
		t.Fatalf("position unknown, got %v", err)
	}
}

// 被拒操作不改变状态、队列次序、凭证与时钟。
func TestRejectedOpsKeepState(t *testing.T) {
	cfg := baseConfig()
	cfg.Terminals = map[string]int{"T1": 1, "T2": 1}
	p := mustPool(t, cfg)
	if _, err := p.EnterPool("alpha", "T1", 10); err != nil {
		t.Fatalf("enter: %v", err)
	}
	before := p.debugState()
	if _, err := p.EnterPool("bravo", "T1", 5); err != ErrClockRollback {
		t.Fatalf("rollback, got %v", err)
	}
	if _, err := p.EnterPool("bravo", "NOPE", 20); err != ErrTerminalNotFound {
		t.Fatalf("terminal, got %v", err)
	}
	if _, err := p.EnterPool("bravo", "T1", 20); err != ErrQueueFull {
		t.Fatalf("full, got %v", err)
	}
	if _, err := p.EnterPool("alpha", "T2", 21); err != ErrDriverInQueue {
		t.Fatalf("dup, got %v", err)
	}
	if got := p.debugState(); got != before {
		t.Fatalf("rejected ops changed state:\nbefore: %s\nafter:  %s", before, got)
	}
	if c := p.Clock(); c != 10 {
		t.Fatalf("clock should stay 10, got %d", c)
	}
	if _, err := p.EnterPool("bravo", "T2", 15); err != nil { // 15 >= clock 10，接受
		t.Fatalf("enter after rejections: %v", err)
	}
}

// 位置查询开销与历史入池总数无关：treap 只含当前在队节点，
// 相同当前内容下，经历 10 万次入离队的池与新建池的查询访问节点数完全一致。
func TestPositionQueryCost(t *testing.T) {
	cfg := baseConfig()
	cfg.Terminals = map[string]int{"T1": 2000}
	p := mustPool(t, cfg)
	var ts int64
	for i := 0; i < 50000; i++ { // 制造 10 万条历史入离队记录，队列保持为空
		if _, err := p.EnterPool("churn", "T1", ts); err != nil {
			t.Fatalf("churn enter: %v", err)
		}
		ts++
		if err := p.Leave("churn", ts); err != nil {
			t.Fatalf("churn leave: %v", err)
		}
		ts++
	}
	fresh := mustPool(t, cfg)
	const n = 1000
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = "d" + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676))
		if _, err := p.EnterPool(ids[i], "T1", ts); err != nil {
			t.Fatalf("fill p: %v", err)
		}
		if _, err := fresh.EnterPool(ids[i], "T1", ts); err != nil {
			t.Fatalf("fill fresh: %v", err)
		}
		ts++
	}
	snap, err := p.QueueSnapshot("T1")
	if err != nil || len(snap) != n {
		t.Fatalf("snapshot should hold exactly current %d drivers, got %d", n, len(snap))
	}
	for i := 0; i < n; i += 97 {
		pos, visits, err := p.PositionWithCost(ids[i])
		if err != nil {
			t.Fatalf("position: %v", err)
		}
		if visits > 64 { // 期望深度约 2*ln(1000)≈14，64 为宽松上界
			t.Fatalf("rank visited %d nodes in queue of %d, exceeds O(log n) bound", visits, n)
		}
		_, freshVisits, err := fresh.PositionWithCost(ids[i])
		if err != nil {
			t.Fatalf("fresh position: %v", err)
		}
		if visits != freshVisits {
			t.Fatalf("query cost depends on history: churned=%d fresh=%d", visits, freshVisits)
		}
		if pos >= n {
			t.Fatalf("position out of range: %d", pos)
		}
	}
}

// 相同操作序列重放得到完全相同的队列历史与凭证记录。
func TestDeterministicReplay(t *testing.T) {
	cfg := baseConfig()
	rng := rand.New(rand.NewSource(99))
	type op struct {
		kind      int
		drv, term string
		dist, ts  int64
	}
	drivers := []string{"a", "b", "c", "d", "e"}
	terms := []string{"T1", "T2", "T3"}
	var ops []op
	var ts int64
	for i := 0; i < 300; i++ {
		ts += rng.Int63n(5)
		ops = append(ops, op{rng.Intn(6), drivers[rng.Intn(5)], terms[rng.Intn(3)], int64(rng.Intn(80)), ts})
	}
	run := func() []string {
		p := mustPool(t, cfg)
		states := make([]string, 0, len(ops))
		for _, o := range ops {
			switch o.kind {
			case 0:
				p.EnterPool(o.drv, o.term, o.ts)
			case 1:
				p.Dispatch(o.term, o.ts)
			case 2:
				p.Arrive(o.drv, o.ts)
			case 3:
				p.NoShow(o.drv, o.ts)
			case 4:
				p.Leave(o.drv, o.ts)
			case 5:
				p.CompleteTrip(o.drv, o.dist, o.ts)
			}
			states = append(states, p.debugState())
		}
		return states
	}
	s1, s2 := run(), run()
	for i := range s1 {
		if s1[i] != s2[i] {
			t.Fatalf("replay diverged at op %d:\n%s\n%s", i, s1[i], s2[i])
		}
	}
}

// 并发调用等价于某个串行顺序：结束后校验全部结构不变量。
func TestConcurrent(t *testing.T) {
	cfg := baseConfig()
	cfg.Terminals = map[string]int{"T1": 50, "T2": 50, "T3": 50}
	p := mustPool(t, cfg)
	drivers := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	terms := []string{"T1", "T2", "T3"}
	var counter atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				ts := counter.Add(1)
				d := drivers[rng.Intn(len(drivers))]
				tm := terms[rng.Intn(len(terms))]
				switch rng.Intn(7) {
				case 0, 1:
					p.EnterPool(d, tm, ts)
				case 2:
					p.Dispatch(tm, ts)
				case 3:
					p.Arrive(d, ts)
				case 4:
					p.NoShow(d, ts)
				case 5:
					p.Leave(d, ts)
				case 6:
					p.CompleteTrip(d, int64(rng.Intn(80)), ts)
				}
			}
		}(int64(g))
	}
	wg.Wait()
	seen := map[string]string{}
	for _, tm := range terms {
		snap, err := p.QueueSnapshot(tm)
		if err != nil {
			t.Fatalf("snapshot %s: %v", tm, err)
		}
		lastPriority := -1
		firstNormal := len(snap)
		for i, id := range snap {
			if prev, dup := seen[id]; dup {
				t.Fatalf("driver %s appears in both %s and %s", id, prev, tm)
			}
			seen[id] = tm
			info, err := p.InspectDriver(id)
			if err != nil || info.State != "queued" {
				t.Fatalf("driver %s in snapshot but state %v err %v", id, info.State, err)
			}
			pos, err := p.Position(id)
			if err != nil || pos != i {
				t.Fatalf("position of %s = %d, want %d (err %v)", id, pos, i, err)
			}
			if info.Priority {
				lastPriority = i
			} else if firstNormal == len(snap) {
				firstNormal = i
			}
		}
		if lastPriority > firstNormal {
			t.Fatalf("%s: priority drivers must precede normal drivers", tm)
		}
	}
}
