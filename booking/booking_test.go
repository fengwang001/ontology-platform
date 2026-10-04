package booking

import (
	"errors"
	"testing"
)

func newExample(t *testing.T) *Booking {
	t.Helper()
	// R=60, E=30, G=10, C=120, K=2, W=10000；s: start=600, cap=3, on=2。
	b := New(60, 30, 10, 120, 2, 10000)
	if err := b.AddSlot(0, "s", 600, 3, 2); err != nil {
		t.Fatal(err)
	}
	return b
}

func book3(t *testing.T, b *Booking) {
	t.Helper()
	for _, x := range []struct {
		p  string
		ch Channel
	}{{"A", Online}, {"B", Online}, {"D", Onsite}} {
		if _, err := b.Book(100, []byte(x.p), "s", x.ch); err != nil {
			t.Fatalf("book3: %v", err)
		}
	}
}

func TestSpecExample(t *testing.T) {
	b := newExample(t)
	A, B, D, F, H, X := []byte("A"), []byte("B"), []byte("D"), []byte("F"), []byte("H"), []byte("X")
	if id, err := b.Book(100, A, "s", Online); err != nil || id != 1 {
		t.Fatalf("A = (%d,%v)", id, err)
	}
	if id, err := b.Book(100, B, "s", Online); err != nil || id != 2 {
		t.Fatalf("B = (%d,%v)", id, err)
	}
	if _, err := b.Book(100, X, "s", Online); !errors.Is(err, ErrNoQuota) {
		t.Fatalf("X 线上无余号, got %v", err)
	}
	if _, err := b.Book(100, D, "s", Onsite); err != nil {
		t.Fatalf("D: %v", err)
	}
	if _, err := b.Book(100, F, "s", Onsite); !errors.Is(err, ErrNoQuota) {
		t.Fatalf("F 现场无余号, got %v", err)
	}
	if err := b.Cancel(480, A, "s"); err != nil { // 恰等 start-C
		t.Fatalf("A 免责退号: %v", err)
	}
	if _, err := b.Book(539, F, "s", Onsite); !errors.Is(err, ErrNoQuota) {
		t.Fatalf("539 仍无余号, got %v", err)
	}
	if id, err := b.Book(540, F, "s", Onsite); err != nil || id != 4 {
		t.Fatalf("540 F 借用 id 应=4, got (%d,%v)", id, err)
	}
	if err := b.JoinWait(545, H, "s", Onsite); err != nil {
		t.Fatalf("H 候补: %v", err)
	}
	if err := b.CheckIn(570, B, "s"); err != nil {
		t.Fatalf("B 签到: %v", err)
	}
	if err := b.CheckIn(570, F, "s"); err != nil {
		t.Fatalf("F 签到: %v", err)
	}
	// 611 的任一“被接受”操作先落地：对另一时段订号（对 s 的订号会在落地前因已开诊被拒）。
	if err := b.AddSlot(611, "x", 1000, 1, 1); err != nil {
		t.Fatal(err)
	}
	// 对已开诊时段订号仍报已开诊，且不产生任何影响。
	if _, err := b.Book(611, []byte("Z"), "s", Online); !errors.Is(err, ErrSlotOpen) {
		t.Fatalf("已开诊, got %v", err)
	}
	// 递补的 H 已签到：611 再签到报已签到而非无预约。
	if err := b.CheckIn(611, H, "s"); !errors.Is(err, ErrAlreadyChecked) {
		t.Fatalf("H 递补应直接已签到, got %v", err)
	}
	// D 有 610 爽约一条；加一条 5000 后验证窗口边界。
	if b.Banned(611, D) {
		t.Fatal("单条爽约 K=2 不应禁约")
	}
	b.cred.Add(D, 5000)
	if !b.Banned(10609, D) {
		t.Fatal("now=10609 两条均在窗内，应禁约")
	}
	if err := b.AddSlot(10609, "s2", 20000, 3, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Book(10609, D, "s2", Online); !errors.Is(err, ErrBanned) {
		t.Fatalf("线上禁约, got %v", err)
	}
	if _, err := b.Book(10609, D, "s2", Onsite); err != nil {
		t.Fatalf("现场不受禁约限制: %v", err)
	}
	if err := b.Cancel(10609, D, "s2"); err != nil { // 释放现场预约，便于随后线上订
		t.Fatal(err)
	}
	if b.Banned(10610, D) {
		t.Fatal("now=10610 时 610 恰等 now-W 出窗，不应禁约")
	}
	if _, err := b.Book(10610, D, "s2", Online); err != nil {
		t.Fatalf("出窗后线上可订: %v", err)
	}
}

func TestCheckInWindowEndpoints(t *testing.T) {
	b := newExample(t)
	p := []byte("P")
	if _, err := b.Book(100, p, "s", Onsite); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckIn(569, p, "s"); !errors.Is(err, ErrTooEarly) {
		t.Fatalf("569 过早, got %v", err)
	}
	if err := b.CheckIn(570, p, "s"); err != nil { // 恰等 start-E
		t.Fatalf("570 起点可签到: %v", err)
	}
	if err := b.CheckIn(580, p, "s"); !errors.Is(err, ErrAlreadyChecked) {
		t.Fatalf("重复签到: %v", err)
	}
	// 终点恰等 610 可签到（须在落地前判定，此时尚未爽约）。
	b3 := newExample(t)
	q := []byte("Q")
	if _, err := b3.Book(100, q, "s", Online); err != nil {
		t.Fatal(err)
	}
	if err := b3.CheckIn(610, q, "s"); err != nil {
		t.Fatalf("610 恰等终点应可签到: %v", err)
	}
	// 611 由被接受操作落地爽约，时刻取 610。
	b4 := newExample(t)
	r := []byte("R")
	if _, err := b4.Book(100, r, "s", Online); err != nil {
		t.Fatal(err)
	}
	if err := b4.AddSlot(611, "x", 1000, 1, 1); err != nil {
		t.Fatal(err)
	}
	b4.cred.Add(r, 10)
	if !b4.Banned(611, r) {
		t.Fatal("610 与 10 两条均在窗内，应禁约（证明爽约时刻=610）")
	}
	if b4.Banned(10610, r) {
		t.Fatal("now=10610 时 610 恰出窗，仅剩 t=10 一条")
	}
}

func TestLateCancelAndFreeCancelBoundary(t *testing.T) {
	b := newExample(t)
	p := []byte("P")
	if _, err := b.Book(100, p, "s", Online); err != nil {
		t.Fatal(err)
	}
	if err := b.Cancel(481, p, "s"); err != nil { // start-C=480 之后为迟退
		t.Fatalf("迟退应成功释放号: %v", err)
	}
	// 迟退记一条时刻 481 的爽约：再加一条即触发禁约。
	b.cred.Add(p, 482)
	if !b.Banned(1000, p) {
		t.Fatal("迟退应记爽约")
	}
	// 号照常释放（481<540，名额仍归线上）。
	if _, err := b.Book(482, []byte("Q"), "s", Online); err != nil {
		t.Fatalf("退号后号应释放: %v", err)
	}
	// 恰等 480 免责：不记爽约。
	b2 := newExample(t)
	if _, err := b2.Book(100, p, "s", Online); err != nil {
		t.Fatal(err)
	}
	if err := b2.Cancel(480, p, "s"); err != nil {
		t.Fatal(err)
	}
	b2.cred.Add(p, 482)
	if b2.Banned(1000, p) {
		t.Fatal("免责退号不应记爽约")
	}
}

func TestWaitlistPromoteAndExpire(t *testing.T) {
	b := newExample(t)
	book3(t, b)
	H, J := []byte("H"), []byte("J")
	if err := b.JoinWait(545, H, "s", Onsite); err != nil {
		t.Fatal(err)
	}
	if err := b.JoinWait(546, J, "s", Onsite); err != nil {
		t.Fatal(err)
	}
	if err := b.JoinWait(547, []byte("X"), "s", Online); !errors.Is(err, ErrCannotWait) {
		t.Fatalf("线上不可候补, got %v", err)
	}
	// A 迟退（550>480，记一条爽约），H 队首递补为普通预约（550<start）。
	if err := b.Cancel(550, []byte("A"), "s"); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckIn(570, H, "s"); err != nil {
		t.Fatalf("H 递补为普通预约，进入签到窗后应可签到: %v", err)
	}
	// 剩余候补 J 在 610 全槽落地完成后作废：
	// D 611 落地释放一个号，J 队首递补并直接已签到（不存在“剩余作废”）。
	if err := b.AddSlot(611, "x", 1000, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckIn(611, J, "s"); !errors.Is(err, ErrAlreadyChecked) {
		t.Fatalf("J 应在落地递补时直接已签到, got %v", err)
	}
	if b.Banned(611, J) {
		t.Fatal("递补候补直接签到，不记爽约")
	}
}

func TestWaitlistExpireAfterAllLanded(t *testing.T) {
	// 槽内全部预约都已签到（落地阶段一个爽约都不产生），
	// 611 被接受操作落地阶段无号可释放，仍在队中的候补 J 作废且不记爽约。
	b := newExample(t)
	book3(t, b)
	J := []byte("J")
	if err := b.JoinWait(545, J, "s", Onsite); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"A", "B", "D"} {
		if err := b.CheckIn(570, []byte(p), "s"); err != nil {
			t.Fatalf("%s 签到: %v", p, err)
		}
	}
	if err := b.AddSlot(611, "x", 1000, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckIn(611, J, "s"); !errors.Is(err, ErrNoReservation) {
		t.Fatalf("J 应在全部落地完成后作废, got %v", err)
	}
	if b.Banned(611, J) {
		t.Fatal("作废候补不记爽约")
	}
}

func TestWaitlistPromoteAfterStartCheckedIn(t *testing.T) {
	// 释放后满员+候补；爽约发生在 start 之后，递补者直接已签到。
	b := newExample(t)
	book3(t, b)
	H := []byte("H")
	if err := b.JoinWait(545, H, "s", Onsite); err != nil {
		t.Fatal(err)
	}
	// B、D 不签到；611 落地时按 (610,id) 序：B(id=2) 先爽约 -> H 递补（now>start 直接签到）。
	if err := b.AddSlot(611, "x", 1000, 1, 1); err != nil {
		t.Fatalf("被接受操作触发落地: %v", err)
	}
	if err := b.CheckIn(611, H, "s"); !errors.Is(err, ErrAlreadyChecked) {
		t.Fatalf("H 应在递补时直接已签到, got %v", err)
	}
}

func TestRejectOrder(t *testing.T) {
	b := newExample(t)
	p := []byte("P")
	if _, err := b.Book(100, nil, "s", Online); !errors.Is(err, ErrInvalid) {
		t.Fatalf("空患者非法, got %v", err)
	}
	if _, err := b.Book(100, p, "", Online); !errors.Is(err, ErrInvalid) {
		t.Fatalf("空槽非法, got %v", err)
	}
	if _, err := b.Book(100, p, "s", Channel(9)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("非法渠道, got %v", err)
	}
	if _, err := b.Book(100, p, "s", Online); err != nil {
		t.Fatal(err)
	}
	// 重复优先于禁约与无号（人为加爽约记录造成禁约态）。
	b.cred.Add(p, 1)
	b.cred.Add(p, 2)
	if _, err := b.Book(101, p, "s", Online); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("重复优先, got %v", err)
	}
	// 时钟回退独立实例验证（可早于任何槽查询被拒）。
	b2 := newExample(t)
	if _, err := b2.Book(500, []byte("Q"), "s", Online); err != nil {
		t.Fatal(err)
	}
	if _, err := b2.Book(499, []byte("Q"), "nope", Online); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("时钟回退优先于槽不存在, got %v", err)
	}
	// 槽不存在优先于已开诊。
	if _, err := b.Book(600, p, "nope", Online); !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("槽不存在优先, got %v", err)
	}
	if _, err := b.Book(600, p, "s", Online); !errors.Is(err, ErrSlotOpen) {
		t.Fatalf("已开诊优先于重复/禁约/无号, got %v", err)
	}
}

func TestRejectedOpDoesNotSettleOrAdvanceClock(t *testing.T) {
	b := newExample(t)
	p := []byte("P")
	if _, err := b.Book(100, p, "s", Online); err != nil {
		t.Fatal(err)
	}
	// 槽不存在：在落地之前即拒绝，不推进时钟、不落地。
	if _, err := b.Book(611, []byte("Z"), "nope", Online); !errors.Is(err, ErrSlotNotFound) {
		t.Fatal(err)
	}
	// 时钟未推进到 611：now=605 可签到；若已落地，611 才到期，605 也在窗外前。
	if err := b.CheckIn(605, p, "s"); err != nil {
		t.Fatalf("被拒操作不应推进时钟, got %v", err)
	}
	// 落地之后的状态类拒绝同样回滚：构造 611 满员槽的重复订号，
	// 若落地未回滚，原预约（D）会被记 610 爽约。
	b2 := newExample(t)
	book3(t, b2)
	D := []byte("D")
	if _, err := b2.Book(611, D, "s", Onsite); !errors.Is(err, ErrSlotOpen) {
		t.Fatalf("已开诊（落地前拒绝）: %v", err)
	}
	// 已开诊在落地前；用 CheckIn 一个无预约患者触发落地后拒绝（无预约），
	// 此时 D 的到期落地必须随拒绝一起回滚。
	if err := b2.CheckIn(611, []byte("Nobody"), "s"); !errors.Is(err, ErrNoReservation) {
		t.Fatalf("无预约, got %v", err)
	}
	if b2.Banned(10609, D) {
		t.Fatal("被拒操作的落地必须回滚，D 不应有 610 爽约记录")
	}
	if err := b2.AddSlot(611, "x", 1000, 1, 1); err != nil {
		t.Fatal(err)
	}
	b2.cred.Add(D, 5000)
	if !b2.Banned(10609, D) {
		t.Fatal("被接受操作落地后，D 的 610 爽约与 5000 同在窗内应禁约")
	}
}

func TestAddSlotValidation(t *testing.T) {
	b := New(60, 30, 10, 120, 2, 10000)
	if err := b.AddSlot(0, "", 10, 1, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("空槽名非法, got %v", err)
	}
	if err := b.AddSlot(10, "s", 10, 1, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("start 须大于 now, got %v", err)
	}
	if err := b.AddSlot(0, "s", 10, 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cap 越界, got %v", err)
	}
	if err := b.AddSlot(0, "s", 10, 2, 3); !errors.Is(err, ErrInvalid) {
		t.Fatalf("on>cap, got %v", err)
	}
	if err := b.AddSlot(0, "s", 10, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.AddSlot(0, "s", 11, 1, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("槽重复非法, got %v", err)
	}
}
