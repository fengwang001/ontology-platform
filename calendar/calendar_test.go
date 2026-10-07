package calendar

import "testing"

// newTestService 返回 H=5、P=7、Q=3 的服务。
func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(5, 7, 3)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func mustAddListing(t *testing.T, s *Service, now int64, id string, price, minStay, gap int64) {
	t.Helper()
	if err := s.AddListing(now, id, price, minStay, gap); err != nil {
		t.Fatalf("AddListing(%s): %v", id, err)
	}
}

func mustHold(t *testing.T, s *Service, now int64, listing string, in, out int64) int64 {
	t.Helper()
	id, err := s.Hold(now, listing, in, out)
	if err != nil {
		t.Fatalf("Hold(%s,[%d,%d)): %v", listing, in, out, err)
	}
	return id
}

func mustConfirm(t *testing.T, s *Service, now int64, listing string, in, out int64) int64 {
	t.Helper()
	id := mustHold(t, s, now, listing, in, out)
	if err := s.Pay(now, id); err != nil {
		t.Fatalf("Pay(%d): %v", id, err)
	}
	return id
}

func expectErr(t *testing.T, err error, code ErrCode, reason Reason) {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("期望 *Error，got %v", err)
	}
	if e.Code != code || e.Reason != reason {
		t.Fatalf("期望 %s/%s，got %v", code, reason, e)
	}
}

func expectBookable(t *testing.T, s *Service, now int64, listing string, in, out int64) {
	t.Helper()
	ok, err := s.CheckAvailability(now, listing, in, out)
	if !ok {
		t.Fatalf("期望 [%d,%d) 可订，got %v", in, out, err)
	}
}

func expectNotBookable(t *testing.T, s *Service, now int64, listing string, in, out int64, reason Reason) {
	t.Helper()
	ok, err := s.CheckAvailability(now, listing, in, out)
	if ok {
		t.Fatalf("期望 [%d,%d) 不可订(%s)，实际可订", in, out, reason)
	}
	expectErr(t, err, ErrNotBookable, reason)
}

// 入住日当天开始占用、退房日当天不占用（[in, out) 半开区间）。
func TestCheckInOpenCheckOutClosed(t *testing.T) {
	s := newTestService(t)
	mustAddListing(t, s, 0, "L", 100, 1, 0)
	mustConfirm(t, s, 0, "L", 10, 13)                              // 占用夜 10,11,12
	expectNotBookable(t, s, 0, "L", 9, 11, ReasonBookingConflict)  // 夜 10 被占
	expectNotBookable(t, s, 0, "L", 12, 14, ReasonBookingConflict) // 夜 12 被占
	expectBookable(t, s, 0, "L", 13, 15)                           // 退房日 13 不占
	mustHold(t, s, 0, "L", 13, 15)                                 // 紧接退房日入住（gap=0）
	expectNotBookable(t, s, 0, "L", 14, 16, ReasonBookingConflict) // 夜 14 被新保留占
}

// 换客间隙为零允许首尾相接，为一则必须空出一整天。
func TestGapZeroAndOne(t *testing.T) {
	s := newTestService(t)
	mustAddListing(t, s, 0, "G0", 100, 1, 0)
	mustAddListing(t, s, 0, "G1", 100, 1, 1)
	mustConfirm(t, s, 0, "G0", 10, 12)
	expectBookable(t, s, 0, "G0", 12, 14) // gap=0：退房日 == 入住日
	mustConfirm(t, s, 0, "G1", 10, 12)
	expectNotBookable(t, s, 0, "G1", 12, 14, ReasonGapTooSmall) // 空 0 天 < 1
	expectBookable(t, s, 0, "G1", 13, 15)                       // 空 1 天，扣除间隙后空档为 0
	mustHold(t, s, 0, "G1", 13, 15)
	expectNotBookable(t, s, 0, "G1", 15, 17, ReasonGapTooSmall) // 对称方向同样受限
	expectBookable(t, s, 0, "G1", 16, 18)
}

// 最短入住按日设定，一笔预订取其入住日的值。
func TestMinStayTakenAtCheckInDay(t *testing.T) {
	s := newTestService(t)
	mustAddListing(t, s, 0, "M", 100, 1, 0)
	if err := s.SetMinStayForDay(0, "M", 10, 3); err != nil {
		t.Fatalf("SetMinStayForDay: %v", err)
	}
	expectNotBookable(t, s, 0, "M", 10, 12, ReasonMinStayTooShort) // 2 < 入住日 10 的 3
	expectBookable(t, s, 0, "M", 10, 13)
	mustHold(t, s, 0, "M", 10, 13)
	expectBookable(t, s, 0, "M", 20, 21) // 入住日 20 用默认值 1
	if err := s.SetMinStayForDay(0, "M", 30, 4); err != nil {
		t.Fatalf("SetMinStayForDay: %v", err)
	}
	expectBookable(t, s, 0, "M", 31, 32) // 入住日 31 仍是默认值 1
	expectNotBookable(t, s, 0, "M", 30, 33, ReasonMinStayTooShort)
	expectBookable(t, s, 0, "M", 30, 34)
}

// 孤夜：空档恰为最短入住减一时拒绝，恰等于最短入住时允许，空档为零不算孤夜。
func TestOrphanNightBoundary(t *testing.T) {
	s := newTestService(t)
	mustAddListing(t, s, 0, "O", 100, 2, 0) // minStay=2, gap=0
	mustConfirm(t, s, 0, "O", 0, 2)
	mustConfirm(t, s, 0, "O", 7, 9)
	expectNotBookable(t, s, 0, "O", 2, 6, ReasonOrphanNight) // 尾部空档 1 = 2-1
	expectBookable(t, s, 0, "O", 2, 5)                       // 尾部空档 2 = minStay
	expectBookable(t, s, 0, "O", 2, 7)                       // 尾部空档 0
	mustConfirm(t, s, 0, "O", 14, 16)
	expectNotBookable(t, s, 0, "O", 10, 12, ReasonOrphanNight) // 头部空档 1（前一笔末夜 8）
	expectBookable(t, s, 0, "O", 11, 14)                       // 头部空档 2 = minStay，尾部空档 0
	expectBookable(t, s, 0, "O", 9, 11)                        // 头部空档 0
}

// 与封锁区间相邻产生的空档不受孤夜约束。
func TestBlockAdjacentGapIsNotOrphan(t *testing.T) {
	s := newTestService(t)
	mustAddListing(t, s, 0, "B", 100, 3, 0)
	mustConfirm(t, s, 0, "B", 0, 3)
	if _, err := s.AddBlock(0, "B", 7, 10); err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	expectBookable(t, s, 0, "B", 3, 6) // 尾部空档 1 夜（夜 6）紧邻封锁，豁免
	mustHold(t, s, 0, "B", 3, 6)
	// 对照：同样的空档紧邻预订则构成孤夜
	mustAddListing(t, s, 0, "B2", 100, 3, 0)
	mustConfirm(t, s, 0, "B2", 0, 3)
	mustConfirm(t, s, 0, "B2", 8, 11)
	expectNotBookable(t, s, 0, "B2", 3, 6, ReasonOrphanNight) // 尾部空档 2 < 3
	expectBookable(t, s, 0, "B2", 3, 8)                       // 空档 0，允许
}

// 保留在有效期最后一刻仍可付款，下一刻付款报状态错误；
// 查询可订性反映 now 下的自动失效。
func TestHoldExpiryLastMomentAndNext(t *testing.T) {
	s := newTestService(t) // H=5
	mustAddListing(t, s, 0, "L", 100, 1, 0)
	h1 := mustHold(t, s, 10, "L", 10, 13) // 有效期至 15
	h2 := mustHold(t, s, 10, "L", 20, 23) // 有效期至 15
	if err := s.Pay(15, h1); err != nil {
		t.Fatalf("有效期最后一刻付款应成功: %v", err)
	}
	expectNotBookable(t, s, 15, "L", 20, 23, ReasonBookingConflict) // h2 最后一刻仍占用
	expectErr(t, s.Pay(16, h2), ErrInvalidState, ReasonNone)        // 下一刻付款：保留已失效
	expectBookable(t, s, 16, "L", 20, 23)                           // 失效后日历已释放
	b, _ := s.GetBooking(h1)
	if b.Status != StatusConfirmed {
		t.Fatalf("h1 应为已确认，got %s", b.Status)
	}
}

// 退款阶梯：>=P 全额，>=Q 且 <P 半额，<Q 不退；边界取等归较宽松档；
// 入住日当天及之后不可取消；取消后日期立即可订。
func TestCancelRefundTiers(t *testing.T) {
	s := newTestService(t) // P=7, Q=3, 每晚 100
	mustAddListing(t, s, 0, "L", 100, 1, 0)
	b1 := mustConfirm(t, s, 0, "L", 20, 22) // 金额 200
	b2 := mustConfirm(t, s, 0, "L", 30, 32)
	b3 := mustConfirm(t, s, 0, "L", 40, 42)
	b4 := mustConfirm(t, s, 0, "L", 50, 52)
	b5 := mustConfirm(t, s, 0, "L", 60, 63) // 金额 300
	if r, err := s.Cancel(13, b1); err != nil || r != 200 {
		t.Fatalf("距入住 7 天(=P)应全额退 200: r=%d err=%v", r, err)
	}
	expectBookable(t, s, 13, "L", 20, 22) // 取消后立即可订
	if r, err := s.Cancel(27, b2); err != nil || r != 100 {
		t.Fatalf("距入住 3 天(=Q)应退半 100: r=%d err=%v", r, err)
	}
	if r, err := s.Cancel(39, b3); err != nil || r != 0 {
		t.Fatalf("距入住 1 天(<Q)应不退: r=%d err=%v", r, err)
	}
	expectErr(t, cancelErr(s, 50, b4), ErrInvalidState, ReasonNone) // 入住日当天不可取消
	if r, err := s.Cancel(54, b5); err != nil || r != 150 {
		t.Fatalf("距入住 6 天(Q<=d<P)应退半 150: r=%d err=%v", r, err)
	}
	expectErr(t, cancelErr(s, 54, b5), ErrInvalidState, ReasonNone) // 已取消再操作
}

// 修改失败原预订保持不变；成功只记录差价；只能修改一次。
func TestModifyBooking(t *testing.T) {
	s := newTestService(t)
	mustAddListing(t, s, 0, "L", 100, 1, 0)
	a := mustConfirm(t, s, 0, "L", 10, 13) // 金额 300
	b := mustConfirm(t, s, 0, "L", 20, 23) // 金额 300
	expectErr(t, s.ModifyBooking(0, a, 20, 24), ErrNotBookable, ReasonBookingConflict)
	ba, _ := s.GetBooking(a)
	if ba.CheckIn != 10 || ba.CheckOut != 13 || ba.Status != StatusConfirmed || ba.Modified {
		t.Fatalf("修改失败后原预订被改变: %+v", ba)
	}
	expectNotBookable(t, s, 0, "L", 10, 13, ReasonBookingConflict) // 原日期仍被占用
	if err := s.ModifyBooking(0, a, 30, 33); err != nil {
		t.Fatalf("修改应成功: %v", err)
	}
	ba, _ = s.GetBooking(a)
	if !ba.Modified || ba.PriceDiff != 0 || ba.Amount != 300 {
		t.Fatalf("修改后状态不正确: %+v", ba)
	}
	expectBookable(t, s, 0, "L", 10, 13)                                     // 原日期已释放
	expectErr(t, s.ModifyBooking(0, a, 40, 43), ErrInvalidState, ReasonNone) // 已修改再修改
	if err := s.ModifyBooking(0, b, 50, 52); err != nil {
		t.Fatalf("修改应成功: %v", err)
	}
	bb, _ := s.GetBooking(b)
	if bb.PriceDiff != -100 || bb.Amount != 300 {
		t.Fatalf("差价只记录不结算: %+v", bb)
	}
	if r, err := s.Cancel(0, b); err != nil || r != 300 {
		t.Fatalf("修改不改变原付款，应退 300: r=%d err=%v", r, err)
	}
}

// 封锁：与已有预订冲突的封锁被拒绝；缩短总是允许；延长按新增部分判定。
func TestBlockShrinkAndExtend(t *testing.T) {
	s := newTestService(t)
	mustAddListing(t, s, 0, "L", 100, 1, 0)
	mustConfirm(t, s, 0, "L", 25, 28)
	if _, err := s.AddBlock(0, "L", 26, 30); err == nil {
		t.Fatal("与预订冲突的封锁应被拒绝")
	} else {
		expectErr(t, err, ErrNotBookable, ReasonBookingConflict)
	}
	blk, err := s.AddBlock(0, "L", 20, 23)
	if err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	expectErr(t, s.ModifyBlock(0, "L", blk, 20, 26), ErrNotBookable, ReasonBookingConflict) // 延长冲突
	b := s.listings["L"].blocks[blk]
	if b.Start != 20 || b.End != 23 {
		t.Fatalf("延长被拒后封锁被改变: %+v", b)
	}
	if err := s.ModifyBlock(0, "L", blk, 21, 22); err != nil {
		t.Fatalf("缩短总是允许: %v", err)
	}
	if err := s.ModifyBlock(0, "L", blk, 19, 24); err != nil {
		t.Fatalf("延长到空闲区应成功: %v", err)
	}
	expectNotBookable(t, s, 0, "L", 22, 25, ReasonBlocked) // 封锁生效
	expectBookable(t, s, 0, "L", 24, 25)                   // 封锁端点半开
}

// 错误类别按固定次序只报告第一个：参数 > 时钟 > 不存在 > 状态 > 不可订。
func TestErrorPrecedence(t *testing.T) {
	s := newTestService(t)
	mustAddListing(t, s, 0, "L", 100, 1, 0)
	a := mustConfirm(t, s, 0, "L", 10, 13)
	if err := s.SetGap(10, "L", 0); err != nil { // 推进时钟到 10
		t.Fatalf("SetGap: %v", err)
	}
	// 参数非法 优先于 时钟回退
	expectErr(t, mustHoldErr(s, 5, "L", 12, 12), ErrInvalidParam, ReasonNone)
	// 时钟回退 优先于 不存在
	expectErr(t, mustHoldErr(s, 5, "nope", 12, 14), ErrClockRollback, ReasonNone)
	expectErr(t, s.Pay(5, 999), ErrClockRollback, ReasonNone)
	// 不存在 优先于 状态
	expectErr(t, s.Pay(10, 999), ErrNotFound, ReasonNone)
	_, err := s.Cancel(10, 999)
	expectErr(t, err, ErrNotFound, ReasonNone)
	// 状态 优先于 日期不可订：已取消预订改到被占日期，仍报状态错误
	c := mustConfirm(t, s, 10, "L", 30, 33)
	if _, err := s.Cancel(10, c); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	expectErr(t, s.ModifyBooking(10, c, 10, 12), ErrInvalidState, ReasonNone)
	// 保留已失效报状态错误而非不存在
	h := mustHold(t, s, 10, "L", 40, 43) // 有效期至 15
	expectErr(t, s.Pay(16, h), ErrInvalidState, ReasonNone)
	// 入住日已到：取消与修改都报状态错误
	expectErr(t, cancelErr(s, 10, a), ErrInvalidState, ReasonNone)
	expectErr(t, s.ModifyBooking(10, a, 20, 22), ErrInvalidState, ReasonNone)
}

func mustHoldErr(s *Service, now int64, listing string, in, out int64) error {
	_, err := s.Hold(now, listing, in, out)
	return err
}

// 被拒绝的操作不改变日历、预订状态与时钟。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s := newTestService(t)
	mustAddListing(t, s, 0, "L", 100, 1, 0)
	a := mustConfirm(t, s, 0, "L", 10, 13)
	// 失败的 Hold 不消耗 ID、不改变日历
	expectErr(t, mustHoldErr(s, 0, "L", 11, 14), ErrNotBookable, ReasonBookingConflict)
	if s.nextBooking != 2 {
		t.Fatalf("失败的 Hold 消耗了预订 ID: next=%d", s.nextBooking)
	}
	expectBookable(t, s, 0, "L", 14, 16)
	// 失败操作不推进时钟、不失效保留
	h := mustHold(t, s, 10, "L", 20, 23) // 有效期至 15
	mustConfirm(t, s, 10, "L", 25, 28)
	expectErr(t, mustHoldErr(s, 20, "L", 26, 27), ErrNotBookable, ReasonBookingConflict)
	if err := s.Pay(14, h); err != nil {
		t.Fatalf("失败操作不应推进时钟/失效保留，now=14 付款应成功: %v", err)
	}
	// 失败的取消不改变预订状态与占用
	expectErr(t, cancelErr(s, 14, a), ErrInvalidState, ReasonNone) // 入住日 10 已过
	ba, _ := s.GetBooking(a)
	if ba.Status != StatusConfirmed {
		t.Fatalf("失败的取消改变了状态: %s", ba.Status)
	}
	if got := s.listings["L"].occupied[11]; got != a {
		t.Fatalf("失败的取消释放了夜 11: occupant=%d", got)
	}
	// 失败的延长不改变封锁
	mustConfirm(t, s, 14, "L", 35, 37)
	blk, err := s.AddBlock(14, "L", 30, 32)
	if err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	expectErr(t, s.ModifyBlock(14, "L", blk, 30, 36), ErrNotBookable, ReasonBookingConflict)
	b := s.listings["L"].blocks[blk]
	if b.Start != 30 || b.End != 32 {
		t.Fatalf("失败的延长改变了封锁: %+v", b)
	}
}

// 查询反映 now 下应有的状态，但本身只读：不推进时钟、不改变状态。
func TestQueryReflectsNowButReadOnly(t *testing.T) {
	s := newTestService(t)
	mustAddListing(t, s, 0, "L", 100, 1, 0)
	mustHold(t, s, 10, "L", 20, 23)                                 // 有效期至 15
	expectNotBookable(t, s, 15, "L", 20, 23, ReasonBookingConflict) // 最后一刻仍占用
	expectBookable(t, s, 16, "L", 20, 23)                           // 下一刻自动失效
	if _, err := s.Hold(12, "L", 30, 33); err != nil {
		t.Fatalf("查询不应推进时钟，now=12 应被接受: %v", err)
	}
	_, err := s.CheckAvailability(5, "L", 20, 23)
	expectErr(t, err, ErrClockRollback, ReasonNone) // 查询也受时钟约束
}

func cancelErr(s *Service, now, id int64) error {
	_, err := s.Cancel(now, id)
	return err
}
