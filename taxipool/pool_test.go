package taxipool

import (
	"errors"
	"testing"
	"time"
)

var testTZ = time.FixedZone("CST", 8*3600)

func testConfig() *Config {
	return &Config{
		Terminals:         map[string]int{"T1": 3, "T2": 2},
		ArrivalLimit:      10 * time.Minute,
		TransferLimit:     6 * time.Minute,
		ShortTripMeters:   3000,
		ReturnLimit:       30 * time.Minute,
		VoucherTTL:        60 * time.Minute,
		DailyVoucherLimit: 2,
		PrioritySlots:     2,
		NoShowLimit:       2,
		BanDuration:       2 * time.Hour,
		TimeZone:          testTZ,
	}
}

var t0 = time.Date(2026, 10, 6, 8, 0, 0, 0, testTZ)

func mustRegister(t *testing.T, p *Pool, d string, at time.Time) {
	t.Helper()
	if err := p.RegisterDriver(d, at); err != nil {
		t.Fatalf("RegisterDriver(%s): %v", d, err)
	}
}

func mustJoin(t *testing.T, p *Pool, d, term string, at time.Time) *JoinResult {
	t.Helper()
	r, err := p.Join(d, term, at)
	if err != nil {
		t.Fatalf("Join(%s,%s): %v", d, term, err)
	}
	return r
}

func mustDispatch(t *testing.T, p *Pool, term string, at time.Time) *DispatchResult {
	t.Helper()
	r, err := p.Dispatch(term, at)
	if err != nil {
		t.Fatalf("Dispatch(%s): %v", term, err)
	}
	return r
}

func kindIs(t *testing.T, err error, want ErrorKind, ctx string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: 期望错误 %v，实际成功", ctx, want.Sentinel())
	}
	if !errors.Is(err, want.Sentinel()) {
		t.Fatalf("%s: 期望 %v，实际 %v (kind=%v)", ctx, want.Sentinel(), err, KindOf(err))
	}
}

// fullTrip：放行->准时到达->完成行程（距离 dist）。
func fullTrip(t *testing.T, p *Pool, d, reqTerm string, dispatchAt, arriveAt, leaveAt time.Time, dist float64) {
	t.Helper()
	mustDispatch(t, p, reqTerm, dispatchAt)
	if err := p.Arrive(d, arriveAt); err != nil {
		t.Fatalf("Arrive(%s): %v", d, err)
	}
	if err := p.CompleteTrip(d, leaveAt, dist); err != nil {
		t.Fatalf("CompleteTrip(%s): %v", d, err)
	}
}

// 1. 行程距离恰等于短途阈值 -> 发放凭证；入池后排在普通司机之前。
func TestBoundary_DistanceEqualThreshold(t *testing.T) {
	p, _ := New(testConfig())
	mustRegister(t, p, "A", t0)
	mustRegister(t, p, "B", t0)
	mustJoin(t, p, "A", "T1", t0.Add(2*time.Minute))
	fullTrip(t, p, "A", "T1", t0.Add(3*time.Minute), t0.Add(4*time.Minute), t0.Add(20*time.Minute), 3000)
	mustJoin(t, p, "B", "T1", t0.Add(20*time.Minute+time.Nanosecond))
	r := mustJoin(t, p, "A", "T1", t0.Add(21*time.Minute))
	if !r.VoucherIssued || !r.VoucherUsed || !r.Priority {
		t.Fatalf("短途取等判定错误: %+v", r)
	}
	snap, _ := p.Snapshot("T1")
	if snap[0].Driver != "A" || snap[1].Driver != "B" {
		t.Fatalf("优先司机应排在普通司机之前: %+v", snap)
	}
}

// 2. 返回间隔恰等于返回时限 -> 及时发放；多 1ns 则不发。
func TestBoundary_ReturnIntervalEqualLimit(t *testing.T) {
	p, _ := New(testConfig())
	mustRegister(t, p, "A", t0)
	mustJoin(t, p, "A", "T1", t0)
	fullTrip(t, p, "A", "T1", t0.Add(time.Minute), t0.Add(2*time.Minute), t0.Add(10*time.Minute), 1000)
	r := mustJoin(t, p, "A", "T1", t0.Add(40*time.Minute))
	if !r.VoucherIssued {
		t.Fatalf("返回间隔取等应发凭证")
	}
	fullTrip(t, p, "A", "T1", t0.Add(41*time.Minute), t0.Add(42*time.Minute), t0.Add(50*time.Minute), 1000)
	r2, err := p.Join("A", "T1", t0.Add(80*time.Minute+time.Nanosecond))
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if r2.VoucherIssued {
		t.Fatalf("超返回时限 1ns 不应发凭证: %+v", r2)
	}
}

// 3. 凭证恰在过期时刻入池 -> 已过期，按普通司机处理；凭证被作废。
func TestBoundary_VoucherExpiresExactly(t *testing.T) {
	p, _ := New(testConfig()) // T2 容量 2
	mustRegister(t, p, "A", t0)
	mustRegister(t, p, "B", t0)
	mustRegister(t, p, "C", t0)
	mustJoin(t, p, "A", "T1", t0)
	fullTrip(t, p, "A", "T1", t0.Add(time.Minute), t0.Add(2*time.Minute), t0.Add(10*time.Minute), 1000)
	issue := t0.Add(20 * time.Minute)
	mustJoin(t, p, "B", "T2", issue)
	mustJoin(t, p, "C", "T2", issue)   // T2 满
	_, err := p.Join("A", "T2", issue) // 凭证发放但满员被拒，凭证保留
	kindIs(t, err, KindQueueFull, "T2 满员入池被拒")
	view, _ := p.Inspect("A")
	if !view.HasVoucher {
		t.Fatalf("入池被拒凭证应保留")
	}
	exp := view.VoucherExpiry
	if err := p.Leave("C", exp); err != nil { // 恰在凭证过期时刻腾位
		t.Fatalf("Leave: %v", err)
	}
	r := mustJoin(t, p, "A", "T2", exp) // 恰过期时刻入池
	if r.VoucherIssued || r.Priority {
		t.Fatalf("恰过期时刻应按普通入队: %+v", r)
	}
	view2, _ := p.Inspect("A")
	if view2.HasVoucher {
		t.Fatalf("过期凭证应已作废")
	}
}

// 4. 每日凭证次数恰达上限：第 N 次发，第 N+1 次同日不发；跨自然日（时区切分）恢复。
func TestBoundary_DailyVoucherCap(t *testing.T) {
	cfg := testConfig()
	cfg.Terminals = map[string]int{"T1": 20}
	p, _ := New(cfg)
	mustRegister(t, p, "A", t0)
	day := time.Date(2026, 10, 6, 9, 0, 0, 0, testTZ)
	// 09:00 普通入队（无短途，不发券）。
	mustJoin(t, p, "A", "T1", day)
	// 第一轮短途：09:20 凭证 #1。
	fullTrip(t, p, "A", "T1", day.Add(time.Minute), day.Add(2*time.Minute),
		day.Add(10*time.Minute), 1000)
	if r := mustJoin(t, p, "A", "T1", day.Add(20*time.Minute)); !r.VoucherIssued {
		t.Fatal("第 1 张应发放")
	}
	// 第二轮短途：10:20 凭证 #2（恰达上限）。
	fullTrip(t, p, "A", "T1", day.Add(60*time.Minute), day.Add(61*time.Minute),
		day.Add(70*time.Minute), 1000)
	if r := mustJoin(t, p, "A", "T1", day.Add(80*time.Minute)); !r.VoucherIssued {
		t.Fatal("第 2 张应发放")
	}
	// 第三轮短途同日：11:20 不再发。
	fullTrip(t, p, "A", "T1", day.Add(120*time.Minute), day.Add(121*time.Minute),
		day.Add(130*time.Minute), 1000)
	if r := mustJoin(t, p, "A", "T1", day.Add(140*time.Minute)); r.VoucherIssued {
		t.Fatal("每日第 3 张不应发放")
	}
	nextDay := time.Date(2026, 10, 7, 0, 30, 0, 0, testTZ)
	fullTrip(t, p, "A", "T1", nextDay, nextDay.Add(time.Minute), nextDay.Add(10*time.Minute), 1000)
	if r := mustJoin(t, p, "A", "T1", nextDay.Add(20*time.Minute)); !r.VoucherIssued {
		t.Fatalf("次日凭证额度应恢复")
	}
}

// 5. 优先名额已满：凭证第一次受限保留，第二次仍受限则作废。
func TestBoundary_RestrictedTwiceVoided(t *testing.T) {
	cfg := testConfig()
	cfg.Terminals = map[string]int{"T1": 10}
	p, _ := New(cfg)
	for _, id := range []string{"P1", "P2", "A"} {
		mustRegister(t, p, id, t0)
	}
	// P1：08:00 入队，08:01 放行，08:02 到达，08:10 离开（短途），08:20 凭证入队。
	mustJoin(t, p, "P1", "T1", t0)
	fullTrip(t, p, "P1", "T1", t0.Add(time.Minute), t0.Add(2*time.Minute),
		t0.Add(10*time.Minute), 1000)
	mustJoin(t, p, "P1", "T1", t0.Add(20*time.Minute))
	// P2：08:30 入队，08:31 放行，08:32 到达，08:40 离开，08:50 凭证入队。
	mustJoin(t, p, "P2", "T1", t0.Add(30*time.Minute))
	fullTrip(t, p, "P2", "T1", t0.Add(31*time.Minute), t0.Add(32*time.Minute),
		t0.Add(40*time.Minute), 1000)
	mustJoin(t, p, "P2", "T1", t0.Add(50*time.Minute)) // 两个优先名额占满
	// A：09:00 入队，09:01 放行，09:02 到达，09:10 离开，09:20 第一次受限。
	mustJoin(t, p, "A", "T1", t0.Add(60*time.Minute))
	fullTrip(t, p, "A", "T1", t0.Add(61*time.Minute), t0.Add(62*time.Minute),
		t0.Add(70*time.Minute), 1000)
	r := mustJoin(t, p, "A", "T1", t0.Add(80*time.Minute))
	if r.Priority {
		t.Fatalf("优先名额满，不应按优先入队")
	}
	if v, _ := p.Inspect("A"); !v.HasVoucher {
		t.Fatalf("第一次受限凭证应保留")
	}
	if err := p.Leave("A", t0.Add(81*time.Minute)); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	r2 := mustJoin(t, p, "A", "T1", t0.Add(82*time.Minute)) // 第二次受限
	if r2.Priority {
		t.Fatalf("第二次受限仍应按普通入队")
	}
	if v, _ := p.Inspect("A"); v.HasVoucher {
		t.Fatalf("第二次受限后凭证应作废")
	}
}

// 6. 满员拒绝不消耗凭证：腾位后凭证仍可用并进入优先位。
func TestBoundary_RejectedJoinKeepsVoucher(t *testing.T) {
	p, _ := New(testConfig()) // T1 容量 3
	for _, id := range []string{"A", "X", "Y", "Z"} {
		mustRegister(t, p, id, t0)
	}
	mustJoin(t, p, "A", "T1", t0)
	fullTrip(t, p, "A", "T1", t0.Add(time.Minute), t0.Add(2*time.Minute),
		t0.Add(10*time.Minute), 1000) // A 回到 idle，08:30 可短途返回
	mustJoin(t, p, "X", "T1", t0.Add(20*time.Minute))
	mustJoin(t, p, "Y", "T1", t0.Add(21*time.Minute))
	mustJoin(t, p, "Z", "T1", t0.Add(22*time.Minute))
	_, err := p.Join("A", "T1", t0.Add(30*time.Minute)) // 发凭证但满员
	kindIs(t, err, KindQueueFull, "满员")
	if v, _ := p.Inspect("A"); !v.HasVoucher {
		t.Fatalf("满员被拒凭证应保留")
	}
	if err := p.Leave("Z", t0.Add(31*time.Minute)); err != nil {
		t.Fatal(err)
	}
	r := mustJoin(t, p, "A", "T1", t0.Add(32*time.Minute))
	if !r.VoucherUsed || r.Position != 0 {
		t.Fatalf("腾位后应使用保留凭证排到优先位: %+v", r)
	}
}

// 7a. 到达恰在时限到期那一刻 -> 逾期爽约。
func TestBoundary_ArriveExactlyAtDeadline(t *testing.T) {
	cfg := testConfig()
	cfg.NoShowLimit = 1
	p, _ := New(cfg)
	mustRegister(t, p, "A", t0)
	mustJoin(t, p, "A", "T1", t0)
	d := mustDispatch(t, p, "T1", t0.Add(time.Minute))
	err := p.Arrive("A", d.Deadline) // 恰到期
	kindIs(t, err, KindArrivalOverdue, "恰到期上报")
	v, _ := p.Inspect("A")
	if v.State != "idle" || !v.BanUntil.Equal(d.Deadline.Add(cfg.BanDuration)) {
		t.Fatalf("逾期应爽约并禁入: %+v", v)
	}
}

// 7b. 早 1ns 到达 -> 成功。
func TestBoundary_ArriveOneNanoBeforeDeadline(t *testing.T) {
	p, _ := New(testConfig())
	mustRegister(t, p, "A", t0)
	mustJoin(t, p, "A", "T1", t0)
	d := mustDispatch(t, p, "T1", t0)
	if err := p.Arrive("A", d.Deadline.Add(-time.Nanosecond)); err != nil {
		t.Fatalf("早 1ns 应成功: %v", err)
	}
}

// 8. 爽约恰达禁入次数触发禁入；禁入期恰结束那一刻可入池。
func TestBoundary_NoShowBanExact(t *testing.T) {
	p, _ := New(testConfig()) // NoShowLimit=2, BanDuration=2h
	mustRegister(t, p, "A", t0)
	// 第一次爽约：08:00 放行，deadline 08:10，逾期。
	mustJoin(t, p, "A", "T1", t0)
	d1 := mustDispatch(t, p, "T1", t0)
	testAdvance(t, p, d1.Deadline)
	// 第二次：再入队放行并逾期 -> 触发禁入，banUntil = deadline2 + 2h。
	mustJoin(t, p, "A", "T1", d1.Deadline)
	d2 := mustDispatch(t, p, "T1", d1.Deadline)
	testAdvance(t, p, d2.Deadline)
	banEnd := d2.Deadline.Add(2 * time.Hour)
	_, err := p.Join("A", "T1", banEnd.Add(-time.Nanosecond))
	kindIs(t, err, KindDriverBanned, "禁入期内")
	// 禁入期恰结束那一刻可入池。
	r := mustJoin(t, p, "A", "T1", banEnd)
	if r.Position != 0 {
		t.Fatalf("禁入恰结束应可入池")
	}
}

// testAdvance 推进系统时钟并确定性地处置所有已逾期放行（测试用）。
func testAdvance(t *testing.T, p *Pool, at time.Time) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finalizeNoShows(at, "")
	p.now = at
}

// 9a. 调剂：请求队列空、备选队列队首为优先司机 -> 该队列不参与。
func TestBoundary_TransferSkipsPriorityFront(t *testing.T) {
	cfg := testConfig()
	cfg.Terminals = map[string]int{"T1": 5, "T2": 5}
	p, _ := New(cfg)
	mustRegister(t, p, "P", t0)
	mustJoin(t, p, "P", "T2", t0)
	fullTrip(t, p, "P", "T2", t0.Add(time.Minute), t0.Add(2*time.Minute),
		t0.Add(10*time.Minute), 1000)
	mustJoin(t, p, "P", "T2", t0.Add(20*time.Minute)) // T2 队首为优先
	_, err := p.Dispatch("T1", t0.Add(30*time.Minute))
	kindIs(t, err, KindNoTaxi, "唯一备选队首为优先")
}

// 14. 并发调用：乱序并发入池（互不相同的时刻），最终队列与按时刻串行重放一致。
func TestBoundary_ConcurrentEquivalentToSerial(t *testing.T) {
	cfg := testConfig()
	cfg.Terminals = map[string]int{"T1": 40}
	p, _ := New(cfg)
	const n = 30
	for i := 0; i < n; i++ {
		mustRegister(t, p, fmtID2(i), t0)
	}
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			_, err := p.Join(fmtID2(i), "T1", t0.Add(time.Second))
			done <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Fatalf("并发 Join: %v", err)
		}
	}
	snap, _ := p.Snapshot("T1")
	if len(snap) != n {
		t.Fatalf("队列长度应为 %d，实际 %d", n, len(snap))
	}
	for i := 1; i < len(snap); i++ {
		if snap[i].Driver <= snap[i-1].Driver {
			t.Fatalf("同刻并发入池应按标识字典序：%v 在 %v 前",
				snap[i].Driver, snap[i-1].Driver)
		}
	}
	// 不变量：每位司机至多一条队列一个位置（快照无重复）。
	seen := map[string]bool{}
	for _, e := range snap {
		if seen[e.Driver] {
			t.Fatalf("司机 %s 重复出现", e.Driver)
		}
		seen[e.Driver] = true
	}
}

func fmtID2(i int) string {
	return "d" + twoDigits(i)
}

func twoDigits(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// 9b. 调剂正常路径：取队首入池时刻最早的队列，并列取候机楼标识字典序最小；
// 调剂放行使用调剂时限。
func TestBoundary_TransferNormal(t *testing.T) {
	cfg := testConfig()
	cfg.Terminals = map[string]int{"T1": 5, "T2": 5, "T3": 5, "T4": 5, "T5": 5}
	p, _ := New(cfg)
	for _, id := range []string{"A", "B"} {
		mustRegister(t, p, id, t0)
	}
	mustJoin(t, p, "A", "T2", t0.Add(10*time.Minute))
	mustJoin(t, p, "B", "T3", t0.Add(11*time.Minute)) // A 入池更早
	r := mustDispatch(t, p, "T1", t0.Add(30*time.Minute))
	if !r.Transferred || r.Driver != "A" || r.SourceTerminal != "T2" {
		t.Fatalf("应调剂最早入池者 A: %+v", r)
	}
	if want := t0.Add(30 * time.Minute).Add(cfg.TransferLimit); !r.Deadline.Equal(want) {
		t.Fatalf("调剂时限错误: got %v want %v", r.Deadline, want)
	}
	// 并列时候机楼标识字典序：T2 与 T3 同刻入队，取 T2。
	mustRegister(t, p, "C", t0.Add(35*time.Minute))
	mustRegister(t, p, "E", t0.Add(35*time.Minute))
	mustRegister(t, p, "F", t0.Add(35*time.Minute))
	// 清空 T3 中的 B（放行即离开队列，到达与否不影响本判定）。
	mustDispatch(t, p, "T3", t0.Add(36*time.Minute))
	mustJoin(t, p, "F", "T5", t0.Add(40*time.Minute))
	mustJoin(t, p, "E", "T4", t0.Add(40*time.Minute))
	r2 := mustDispatch(t, p, "T1", t0.Add(41*time.Minute))
	if !r2.Transferred || r2.SourceTerminal != "T4" || r2.Driver != "E" {
		t.Fatalf("并列应取候机楼标识更小的 T4: %+v", r2)
	}
}

// 10. 同一时刻两位司机入池：按司机标识字典序排列。
func TestBoundary_SameTimeLexicographic(t *testing.T) {
	p, _ := New(testConfig())
	mustRegister(t, p, "B10", t0)
	mustRegister(t, p, "B2", t0)
	mustJoin(t, p, "B10", "T1", t0)
	mustJoin(t, p, "B2", "T1", t0)
	snap, _ := p.Snapshot("T1")
	if snap[0].Driver != "B10" || snap[1].Driver != "B2" {
		t.Fatalf("同刻应按字典序（B10 < B2）: %v %v", snap[0].Driver, snap[1].Driver)
	}
	pos10, _ := p.Position("B10")
	pos2, _ := p.Position("B2")
	if pos10 != 0 || pos2 != 1 {
		t.Fatalf("位置应为 0/1，实际 %d/%d", pos10, pos2)
	}
}

// 11. 容量、重复入池、时钟回退、候机楼/司机不存在等错误类别与拒绝不改状态。
func TestBoundary_BasicRejections(t *testing.T) {
	p, _ := New(testConfig()) // T2 容量 2
	mustRegister(t, p, "A", t0)
	_, err := p.Join("A", "NOPE", t0)
	kindIs(t, err, KindTerminalNotFound, "候机楼不存在")
	_, err = p.Join("GHOST", "T1", t0)
	kindIs(t, err, KindDriverNotFound, "司机不存在")
	mustJoin(t, p, "A", "T2", t0)
	_, err = p.Join("A", "T2", t0)
	kindIs(t, err, KindAlreadyQueued, "重复入池")
	mustRegister(t, p, "B", t0)
	mustJoin(t, p, "B", "T2", t0)
	snapBefore, _ := p.Snapshot("T2")
	_, err = p.Join("GHOST2", "T2", t0)
	kindIs(t, err, KindDriverNotFound, "满员前先判司机不存在")
	mustRegister(t, p, "C", t0)
	_, err = p.Join("C", "T2", t0)
	kindIs(t, err, KindQueueFull, "队列已满")
	snapAfter, _ := p.Snapshot("T2")
	if len(snapAfter) != len(snapBefore) {
		t.Fatalf("被拒绝操作不得改变队列")
	}
	_, err = p.Join("C", "T2", t0.Add(-time.Nanosecond))
	kindIs(t, err, KindClockRewind, "时钟回退")
	// 错误优先级：空候机楼 + 时钟回退，先报时钟回退。
	_, err = p.Join("C", "NOPE", t0.Add(-time.Second))
	kindIs(t, err, KindClockRewind, "时钟回退优先于候机楼不存在")
	// 参数非法最先。
	_, err = p.Join("", "NOPE", t0)
	kindIs(t, err, KindInvalidParam, "参数非法最先")
	// 无车可放行：所有队列皆空（A、B 已占满 T2，未发生过放行；这里改在新候机楼语义下，
	// 直接对确实为空的 T1 判定——先把 T2 两车放行清空，再请求 T1 无备选）。
	mustDispatch(t, p, "T2", t0.Add(2*time.Second))
	mustDispatch(t, p, "T2", t0.Add(3*time.Second))
	_, err = p.Dispatch("T1", t0.Add(4*time.Second))
	kindIs(t, err, KindNoTaxi, "无车可放行")
}

// 12. 放行后到达之前不可离队；到达后完成行程；离队不消耗凭证（凭证仍在）。
func TestBoundary_LeaveRules(t *testing.T) {
	p, _ := New(testConfig())
	mustRegister(t, p, "A", t0)
	mustJoin(t, p, "A", "T1", t0)
	mustDispatch(t, p, "T1", t0.Add(time.Minute))
	err := p.Leave("A", t0.Add(2*time.Minute))
	kindIs(t, err, KindCannotLeaveDispatched, "放行中不可离队")
	err = p.Arrive("B", t0.Add(2*time.Minute))
	kindIs(t, err, KindDriverNotFound, "未注册司机到达")
}

// 13. 位置查询开销只随当前队长变化，与历史入池总数无关（遍历步数可验证）。
func TestBoundary_PositionScanIndependentOfHistory(t *testing.T) {
	cfg := testConfig()
	cfg.Terminals = map[string]int{"T1": 10}
	p, _ := New(cfg)
	for i := 0; i < 6; i++ {
		mustRegister(t, p, fmtID(i), t0)
	}
	// 产生大量历史入池/出队，最终队列中只留 D5 一人。
	for i := 0; i < 5; i++ {
		mustJoin(t, p, fmtID(i), "T1", t0.Add(time.Duration(i)*time.Minute))
	}
	for i := 0; i < 5; i++ {
		mustDispatch(t, p, "T1", t0.Add(10*time.Duration(i+1)*time.Minute))
		if err := p.Arrive(fmtID(i), t0.Add(10*time.Duration(i+1)*time.Minute+time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := p.CompleteTrip(fmtID(i),
			t0.Add(10*time.Duration(i+1)*time.Minute+2*time.Minute), 5000); err != nil {
			t.Fatal(err)
		}
	}
	mustJoin(t, p, "D5", "T1", t0.Add(100*time.Minute))
	pos, err := p.Position("D5")
	if err != nil || pos != 0 {
		t.Fatalf("D5 位置应为 0: pos=%d err=%v", pos, err)
	}
	scan := p.LastPositionScan("T1")
	if scan != 1 {
		t.Fatalf("队列仅 1 人，遍历步数应为 1，实际 %d（与历史入池数无关）", scan)
	}
	// 再加 2 人，步数等于当前队长范围内的值（=3），仍与历史无关。
	mustRegister(t, p, "D6", t0.Add(100*time.Minute))
	mustRegister(t, p, "D7", t0.Add(100*time.Minute))
	mustJoin(t, p, "D6", "T1", t0.Add(101*time.Minute))
	mustJoin(t, p, "D7", "T1", t0.Add(102*time.Minute))
	_, _ = p.Position("D7")
	if scan := p.LastPositionScan("T1"); scan != 3 {
		t.Fatalf("当前 3 人，遍历步数应为 3，实际 %d", scan)
	}
}

func fmtID(i int) string {
	if i < 5 {
		return string(rune('0' + i))
	}
	return "D" + string(rune('0'+i))
}
