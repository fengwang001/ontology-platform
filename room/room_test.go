package room

import (
	"fmt"
	"reflect"
	"testing"
)

// step 是脚本化操作：执行 call 并期望返回 want。
type step struct {
	name string
	call func(m *Manager) error
	want error
}

func runSteps(t *testing.T, m *Manager, steps []step) {
	t.Helper()
	for _, s := range steps {
		if got := s.call(m); got != s.want {
			t.Fatalf("%s: got %v, want %v", s.name, got, s.want)
		}
	}
}

func stNewRoom(rid string, tm, s, d int, h int64, want error) step {
	return step{fmt.Sprintf("NewRoom(%s,%d,%d,%d,%d)", rid, tm, s, d, h),
		func(m *Manager) error { return m.NewRoom(rid, tm, s, d, h) }, want}
}

func stForm(pid string, members []string, want error) step {
	return step{fmt.Sprintf("FormParty(%s,%v)", pid, members),
		func(m *Manager) error { return m.FormParty(pid, members) }, want}
}

func stReserve(now int64, rid, pid string, want error) step {
	return step{fmt.Sprintf("Reserve(%d,%s,%s)", now, rid, pid),
		func(m *Manager) error { return m.Reserve(now, rid, pid) }, want}
}

func stConfirm(now int64, rid, player string, want error) step {
	return step{fmt.Sprintf("Confirm(%d,%s,%s)", now, rid, player),
		func(m *Manager) error { return m.Confirm(now, rid, player) }, want}
}

func stLeave(now int64, rid, player string, want error) step {
	return step{fmt.Sprintf("Leave(%d,%s,%s)", now, rid, player),
		func(m *Manager) error { return m.Leave(now, rid, player) }, want}
}

func stKick(now int64, rid, by, target string, want error) step {
	return step{fmt.Sprintf("Kick(%d,%s,%s,%s)", now, rid, by, target),
		func(m *Manager) error { return m.Kick(now, rid, by, target) }, want}
}

func res(team, idx int, who string) SeatView { return SeatView{team, idx, who, Reserved} }
func sit(team, idx int, who string) SeatView { return SeatView{team, idx, who, Seated} }

// mkView 构造完整房间快照：未列出的席位均为空。
func mkView(teams, size int, owner string, filled ...SeatView) RoomView {
	v := RoomView{Owner: owner}
	byPos := make(map[[2]int]SeatView, len(filled))
	for _, f := range filled {
		byPos[[2]int{f.Team, f.Index}] = f
	}
	for tm := 0; tm < teams; tm++ {
		for i := 0; i < size; i++ {
			sv, ok := byPos[[2]int{tm, i}]
			if !ok {
				sv = SeatView{Team: tm, Index: i, State: Empty}
			}
			v.Seats = append(v.Seats, sv)
		}
	}
	return v
}

func checkSeats(t *testing.T, m *Manager, now int64, rid string, want RoomView) {
	t.Helper()
	got, err := m.Seats(now, rid)
	if err != nil {
		t.Fatalf("Seats(%d,%s): unexpected err %v", now, rid, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Seats(%d,%s):\n got %+v\nwant %+v", now, rid, got, want)
	}
}

// TestManager 表驱动合并全部规则用例。
func TestManager(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"题面例1-整组选队确认与房主移交", testWorkedExample},
		{"到期取等", testExpiryBoundary},
		{"部分确认后到期只放未确认者", testPartialConfirmExpiry},
		{"到期席位可再分配", testExpiryReassign},
		{"选队并列取队号最小", testPickTeamTie},
		{"空席不足先于失衡", testNoSeatBeforeImbalance},
		{"房主移交按确认序号而非席位号", testOwnerHandover},
		{"房主为空后的再产生", testOwnerReborn},
		{"禁入", testBan},
		{"跨房间占用拒绝", testCrossRoomBusy},
		{"拒绝次序", testRejectOrder},
		{"被拒不改状态不推进时钟", testRejectedKeepsState},
		{"参数校验", testParamValidation},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}

// 题面例：T=2,S=3,D=2,H=100 的完整序列。
func testWorkedExample(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("r1", 2, 3, 2, 100, nil),
		stForm("P1", []string{"a", "b"}, nil),
		stForm("P2", []string{"c"}, nil),
		stForm("P3", []string{"d", "e"}, nil),
		stReserve(0, "r1", "P1", nil),               // a=(0,0) b=(0,1)，到期 100
		stReserve(10, "r1", "P2", nil),              // 占用 2:0，c=(1,0)，到期 110
		stReserve(20, "r1", "P3", nil),              // 占用 2:1，d=(1,1) e=(1,2)，差 1
		stConfirm(30, "r1", "b", nil),               // b 序号 1，成为房主
		stConfirm(50, "r1", "a", nil),               // a 序号 2
		stLeave(60, "r1", "b", nil),                 // 房主移交 a
		stConfirm(109, "r1", "c", nil),              // c 序号 3
		stConfirm(110, "r1", "c", ErrNoReservation), // 取等失效
	})
	checkSeats(t, m, 119, "r1", mkView(2, 3, "a",
		sit(0, 0, "a"), sit(1, 0, "c"), res(1, 1, "d"), res(1, 2, "e"),
	))
	checkSeats(t, m, 120, "r1", mkView(2, 3, "a",
		sit(0, 0, "a"), sit(1, 0, "c"), // now=120 起 d、e 的预留释放
	))
}

// 到期取等：now 等于到期时刻即失效。
func testExpiryBoundary(t *testing.T) {
	// 子情形 A：now=99 < 到期 100，确认成功。
	mA := NewManager()
	runSteps(t, mA, []step{
		stNewRoom("r1", 2, 2, 2, 100, nil),
		stForm("P1", []string{"a", "b"}, nil),
		stReserve(0, "r1", "P1", nil),
		stConfirm(99, "r1", "a", nil),
		stConfirm(99, "r1", "b", nil),
	})
	checkSeats(t, mA, 100, "r1", mkView(2, 2, "a", sit(0, 0, "a"), sit(0, 1, "b")))

	// 子情形 B：now=100 == 到期 100，确认报无预留，席位已空。
	mB := NewManager()
	runSteps(t, mB, []step{
		stNewRoom("r1", 2, 2, 2, 100, nil),
		stForm("P1", []string{"a", "b"}, nil),
		stReserve(0, "r1", "P1", nil),
		stConfirm(100, "r1", "a", ErrNoReservation),
	})
	checkSeats(t, mB, 100, "r1", mkView(2, 2, ""))
}

// 部分确认后到期：已确认者留下，未确认者释放。
func testPartialConfirmExpiry(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("r1", 2, 2, 2, 100, nil),
		stForm("P1", []string{"a", "b"}, nil),
		stReserve(0, "r1", "P1", nil),
		stConfirm(50, "r1", "a", nil), // a 入座，不再到期
	})
	checkSeats(t, m, 150, "r1", mkView(2, 2, "a", sit(0, 0, "a"))) // b 被释放
	runSteps(t, m, []step{
		stConfirm(151, "r1", "b", ErrNoReservation),
		stLeave(152, "r1", "b", ErrNotInRoom),
	})
}

// 到期释放的席位可被后续组队分得。
func testExpiryReassign(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("r1", 2, 2, 2, 100, nil),
		stForm("P1", []string{"a", "b"}, nil),
		stForm("P2", []string{"c", "d"}, nil),
		stReserve(0, "r1", "P1", nil),   // 到期 100
		stReserve(150, "r1", "P2", nil), // P1 已失效，c、d 进队 0
	})
	checkSeats(t, m, 150, "r1", mkView(2, 2, "", res(0, 0, "c"), res(0, 1, "d")))
}

// 占用数并列时取队号最小。
func testPickTeamTie(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("r1", 3, 3, 3, 1000, nil),
		stForm("PA", []string{"a"}, nil),
		stForm("PB", []string{"b"}, nil),
		stForm("PC", []string{"c"}, nil),
		stForm("PD", []string{"d", "e"}, nil),
		stReserve(0, "r1", "PA", nil), // [1,0,0] -> 队 0
		stReserve(1, "r1", "PB", nil), // 队 1、2 并列 -> 队 1
		stReserve(2, "r1", "PC", nil), // -> 队 2
		stReserve(3, "r1", "PD", nil), // [1,1,1] 并列 -> 队 0，按序得位 1、2
	})
	checkSeats(t, m, 4, "r1", mkView(3, 3, "",
		res(0, 0, "a"), res(0, 1, "d"), res(0, 2, "e"),
		res(1, 0, "b"), res(2, 0, "c"),
	))
}

// 空席不足先于失衡判定（题面例2）。
func testNoSeatBeforeImbalance(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("r1", 2, 3, 1, 1000, nil),
		stForm("PA", []string{"p1"}, nil),
		stForm("PB", []string{"p2"}, nil),
		stForm("PC", []string{"p3", "p4"}, nil),
		stForm("PD", []string{"p5"}, nil),
		stForm("PE", []string{"p6"}, nil),
		stForm("PF", []string{"p7"}, nil),
		stForm("PG", []string{"p8", "p9"}, nil),
		stReserve(0, "r1", "PA", nil),          // [1,0]
		stReserve(1, "r1", "PB", nil),          // [1,1]
		stReserve(2, "r1", "PC", ErrImbalance), // 两人组进队 0 将 3:1，差 2 > D
		stReserve(3, "r1", "PD", nil),          // 单人组进队 0 -> [2,1]
		stReserve(4, "r1", "PE", nil),          // [2,2]
		stReserve(5, "r1", "PG", ErrNoSeat),    // 队 0 并列最小但只剩 1 位；
		// 若先判失衡，[4,2] 差 2 > D 也会报失衡 —— 空席不足优先
	})
	checkSeats(t, m, 6, "r1", mkView(2, 3, "",
		res(0, 0, "p1"), res(0, 1, "p5"), res(1, 0, "p2"), res(1, 1, "p6"),
	))

	// 题面例2 的 2 比 3：选中队 0 只剩 1 个空位，报空席不足。
	mB := NewManager()
	runSteps(t, mB, []step{
		stNewRoom("r1", 2, 3, 1, 1000, nil),
		stForm("PA", []string{"a"}, nil),
		stForm("PB", []string{"b"}, nil),
		stForm("PC", []string{"c"}, nil),
		stForm("PD", []string{"d"}, nil),
		stForm("PE", []string{"e"}, nil),
		stForm("PF", []string{"f"}, nil),
		stForm("PG", []string{"g", "h"}, nil),
		stReserve(0, "r1", "PA", nil),
		stReserve(1, "r1", "PB", nil),
		stReserve(2, "r1", "PC", nil),
		stReserve(3, "r1", "PD", nil),
		stReserve(4, "r1", "PE", nil),
		stReserve(5, "r1", "PF", nil),       // [3,3]
		stLeave(6, "r1", "a", nil),          // [2,3]
		stReserve(7, "r1", "PG", ErrNoSeat), // 选中队 0 只剩 1 位
	})
}

// 房主移交按确认序号最小者，而非席位号最小者。
func testOwnerHandover(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("r1", 2, 2, 2, 1000, nil),
		stForm("P1", []string{"a", "b"}, nil),
		stForm("P2", []string{"c"}, nil),
		stReserve(0, "r1", "P1", nil), // a=(0,0) b=(0,1)
		stReserve(1, "r1", "P2", nil), // c=(1,0)
		stConfirm(10, "r1", "b", nil), // b 序号 1（席位 (0,1)）
		stConfirm(20, "r1", "c", nil), // c 序号 2（席位 (1,0)）
		stConfirm(30, "r1", "a", nil), // a 序号 3（席位 (0,0)）
		stLeave(40, "r1", "b", nil),   // 移交序号最小的 c，而非席位号最小的 a
	})
	checkSeats(t, m, 41, "r1", mkView(2, 2, "c", sit(0, 0, "a"), sit(1, 0, "c")))
	runSteps(t, m, []step{
		stLeave(50, "r1", "c", nil),
	})
	checkSeats(t, m, 51, "r1", mkView(2, 2, "a", sit(0, 0, "a")))
}

// 全部在座者离开后房主置空，下一个确认成功者重新成为房主。
func testOwnerReborn(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("r1", 2, 2, 2, 1000, nil),
		stForm("P1", []string{"a"}, nil),
		stForm("P2", []string{"b"}, nil),
		stReserve(0, "r1", "P1", nil),
		stConfirm(10, "r1", "a", nil),
		stLeave(20, "r1", "a", nil), // 无人在座，房主置空
	})
	checkSeats(t, m, 21, "r1", mkView(2, 2, ""))
	runSteps(t, m, []step{
		stReserve(30, "r1", "P2", nil),
		stConfirm(40, "r1", "b", nil), // 房主再产生
	})
	checkSeats(t, m, 41, "r1", mkView(2, 2, "b", sit(0, 0, "b")))
}

// 踢人：释放目标并永久禁入；同组其他成员留在原位。
func testBan(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("r1", 2, 2, 2, 1000, nil),
		stForm("P1", []string{"a", "b"}, nil),
		stForm("P2", []string{"c"}, nil),
		stReserve(0, "r1", "P1", nil),
		stConfirm(10, "r1", "a", nil), // a 序号 1 成为房主
		stKick(20, "r1", "a", "b", nil),
	})
	// b 被释放并禁入，a 留在原位。
	checkSeats(t, m, 21, "r1", mkView(2, 2, "a", sit(0, 0, "a")))
	runSteps(t, m, []step{
		stLeave(30, "r1", "a", nil),          // 该组全部离开
		stReserve(40, "r1", "P1", ErrBanned), // b 在禁入名单
		stReserve(41, "r1", "P2", nil),       // 其他组队不受影响
		stConfirm(42, "r1", "c", nil),
		stKick(43, "r1", "c", "c", ErrInvalidParam), // by == target
		stKick(44, "r1", "b", "c", ErrNotOwner),     // b 不是房主（且被禁入）
		stKick(45, "r1", "c", "zzz", ErrNotInRoom),  // 目标不在房间
	})
}

// 成员在任一房间持有席位或有效预留，整组 Reserve 被拒。
func testCrossRoomBusy(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("r1", 2, 2, 2, 1000, nil),
		stNewRoom("r2", 2, 2, 2, 1000, nil),
		stForm("P1", []string{"a", "b"}, nil),
		stForm("P2", []string{"c"}, nil),
		stReserve(0, "r1", "P1", nil),
		stReserve(1, "r2", "P2", nil),
		stReserve(2, "r2", "P1", ErrBusy), // a、b 在 r1 持有预留
		stReserve(3, "r1", "P2", ErrBusy), // c 在 r2 持有预留
		stConfirm(4, "r2", "c", nil),
		stReserve(5, "r1", "P2", ErrBusy), // 已入座同样拒绝
		stLeave(6, "r2", "c", nil),
		stReserve(7, "r1", "P2", nil), // 释放后可进
	})
	checkSeats(t, m, 8, "r1", mkView(2, 2, "",
		res(0, 0, "a"), res(0, 1, "b"), res(1, 0, "c"),
	))
}

// 各操作的拒绝次序：参数非法 > 时钟回退 > 房间不存在 > 状态错误。
func testRejectOrder(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("r1", 2, 2, 2, 100, nil),
		stNewRoom("r2", 2, 2, 2, 1_000_000_000, nil),
		stForm("P1", []string{"a", "b"}, nil),
		stForm("P2", []string{"c"}, nil),
		stReserve(0, "r1", "P1", nil),  // a、b 到期 100
		stReserve(10, "r2", "P2", nil), // c 在 r2
		// Reserve：参数非法 > 时钟回退 > 房间不存在 > 组队不存在 > 占用 > 禁入 > 空席 > 失衡
		stReserve(-1, "nope", "nope", ErrInvalidParam),
		stReserve(5, "nope", "nope", ErrClockSkew),
		stReserve(20, "nope", "nope", ErrRoomNotFound),
		stReserve(30, "r1", "nope", ErrPartyNotFound),
		stReserve(40, "r1", "P2", ErrBusy), // c 在 r2 持有预留
		// Confirm：参数非法 > 时钟回退 > 房间不存在 > 无预留
		stConfirm(-1, "r1", "a", ErrInvalidParam),
		stConfirm(5, "r1", "a", ErrClockSkew),
		stConfirm(50, "nope", "a", ErrRoomNotFound),
		stConfirm(51, "r1", "nobody", ErrNoReservation),
		stConfirm(52, "r2", "a", ErrNoReservation), // 不在本房间
		stConfirm(53, "r1", "a", nil),              // a 序号 1 成为房主
		stConfirm(54, "r1", "a", ErrNoReservation), // 已入座
		// Leave：参数非法 > 时钟回退 > 房间不存在 > 不在房间
		stLeave(-1, "r1", "a", ErrInvalidParam),
		stLeave(5, "r1", "a", ErrClockSkew),
		stLeave(60, "nope", "a", ErrRoomNotFound),
		stLeave(61, "r1", "nobody", ErrNotInRoom),
		// Kick：参数非法 > 时钟回退 > 房间不存在 > 不是房主 > 目标不在房间
		stKick(-1, "r1", "a", "b", ErrInvalidParam),
		stKick(70, "r1", "a", "a", ErrInvalidParam), // by == target
		stKick(5, "r1", "a", "b", ErrClockSkew),
		stKick(71, "nope", "a", "b", ErrRoomNotFound),
		stKick(72, "r1", "b", "a", ErrNotOwner),
		stKick(73, "r1", "a", "nobody", ErrNotInRoom),
		// FormParty：参数非法 > pid 已存在 > 成员已属其他组队
		stForm("", []string{"x"}, ErrInvalidParam),
		stForm("PX", nil, ErrInvalidParam),
		stForm("PX", []string{""}, ErrInvalidParam),
		stForm("PX", []string{"x", "x"}, ErrInvalidParam),
		stForm("P1", []string{"a"}, ErrPartyExists), // pid 检查先于成员检查
		stForm("PX", []string{"a"}, ErrMemberBound),
		// NewRoom：参数非法 > rid 已存在
		stNewRoom("bad", 1, 1, 1, 1, ErrInvalidParam),
		stNewRoom("bad", 9, 1, 1, 1, ErrInvalidParam),
		stNewRoom("bad", 2, 0, 1, 1, ErrInvalidParam),
		stNewRoom("bad", 2, 17, 1, 1, ErrInvalidParam),
		stNewRoom("bad", 2, 1, 0, 1, ErrInvalidParam),
		stNewRoom("bad", 2, 1, 2, 1, ErrInvalidParam), // D > S
		stNewRoom("bad", 2, 1, 1, 0, ErrInvalidParam),
		stNewRoom("bad", 2, 1, 1, 1_000_000_001, ErrInvalidParam),
		stNewRoom("r1", 2, 1, 1, 1, ErrRoomExists),
	})
}

// 被拒操作不改变可观察状态，也不推进时钟。
func testRejectedKeepsState(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("r1", 2, 2, 2, 100, nil),
		stNewRoom("r2", 2, 2, 2, 1_000_000_000, nil),
		stForm("P1", []string{"a", "b"}, nil),
		stForm("P2", []string{"c"}, nil),
		stReserve(0, "r1", "P1", nil),  // 到期 100
		stReserve(10, "r2", "P2", nil), // 时钟推进到 10
		// 在 now=150 被拒（c 在 r2 占用）：不得落地 r1 的到期，不得推进时钟。
		stReserve(150, "r1", "P2", ErrBusy),
		// 若时钟被推进到 150，下面 now=60 会报时钟回退；
		// 若到期被落地，a 的预留（到期 100 > 60）将不存在。
		stConfirm(60, "r1", "a", nil),
		stConfirm(70, "r1", "b", nil),
		// 被拒的 Seats 同样不推进时钟。
		stLeave(90, "r1", "a", nil),
	})
	if _, err := m.Seats(200, "nope"); err != ErrRoomNotFound {
		t.Fatalf("Seats(200,nope): got %v, want %v", err, ErrRoomNotFound)
	}
	runSteps(t, m, []step{
		stLeave(95, "r1", "b", nil), // 时钟仍在 90，95 合法
	})
	checkSeats(t, m, 96, "r1", mkView(2, 2, ""))
}

// 参数边界校验。
func testParamValidation(t *testing.T) {
	m := NewManager()
	runSteps(t, m, []step{
		stNewRoom("", 2, 1, 1, 1, ErrInvalidParam),
		stNewRoom("r1", 2, 1, 1, 1, nil),
		stForm("P1", []string{"a"}, nil),
		stReserve(-1, "r1", "P1", ErrInvalidParam),
		stReserve(1_000_000_000_001, "r1", "P1", ErrInvalidParam),
		stReserve(0, "", "P1", ErrInvalidParam),
		stReserve(0, "r1", "", ErrInvalidParam),
		stConfirm(0, "r1", "", ErrInvalidParam),
		stLeave(0, "", "a", ErrInvalidParam),
		stKick(0, "r1", "", "a", ErrInvalidParam),
		stReserve(1_000_000_000_000, "r1", "P1", nil), // now 上界可用
	})
	if _, err := m.Seats(-1, "r1"); err != ErrInvalidParam {
		t.Fatalf("Seats(-1): got %v, want %v", err, ErrInvalidParam)
	}
	if _, err := m.Seats(1_000_000_000_000, ""); err != ErrInvalidParam {
		t.Fatalf("Seats(empty rid): got %v, want %v", err, ErrInvalidParam)
	}
	// 成员名单上限 16。
	names := make([]string, 17)
	for i := range names {
		names[i] = fmt.Sprintf("n%d", i)
	}
	runSteps(t, m, []step{
		stForm("big", names, ErrInvalidParam),
		stForm("ok16", names[:16], nil),
	})
}
