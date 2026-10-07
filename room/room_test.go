package room_test

import (
	"testing"

	"ontology/room"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func mustCode(t *testing.T, err error, want room.ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got success", want)
	}
	re, ok := err.(*room.Error)
	if !ok {
		t.Fatalf("expected *room.Error, got %T (%v)", err, err)
	}
	if re.Code != want {
		t.Fatalf("expected error code %s, got %s (%v)", want, re.Code, err)
	}
}

func newRoom(t *testing.T, cfg room.Config) *room.Room {
	t.Helper()
	r, err := room.NewRoom(cfg)
	if err != nil {
		t.Fatalf("NewRoom: %v", err)
	}
	return r
}

func query(t *testing.T, r *room.Room, now int64) room.Snapshot {
	t.Helper()
	snap, err := r.Query(now)
	if err != nil {
		t.Fatalf("Query(%d): %v", now, err)
	}
	return snap
}

// joinAll 让 ids 依次加入并全部就绪；返回促成倒计时的起算 now。
func joinAllReady(t *testing.T, r *room.Room, ids []string, now int64) int64 {
	t.Helper()
	for _, id := range ids {
		mustOK(t, r.Join(id, now))
		now++
	}
	for _, id := range ids {
		mustOK(t, r.SetReady(id, true, now))
		now++
	}
	return now - 1
}

// 倒计时到期：恰等于到期时刻视为已到期，差一秒则未到期。
func TestCountdownExpiryExactAndOffByOne(t *testing.T) {
	r := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 10, ReportWindow: 60})
	start := joinAllReady(t, r, []string{"a", "b"}, 0) // 起算时刻 = 3
	snap := query(t, r, start)
	if snap.State != room.StateCountdown {
		t.Fatalf("expected countdown, got %s", snap.State)
	}
	expiry := start + 10
	if snap.CountdownStart != start || snap.CountdownExpiry != expiry {
		t.Fatalf("countdown [%d,%d], want [%d,%d]", snap.CountdownStart, snap.CountdownExpiry, start, expiry)
	}

	snap = query(t, r, expiry-1)
	t.Logf("判定依据: now=%d < expiry=%d，差一秒未到期，仍为倒计时", expiry-1, expiry)
	if snap.State != room.StateCountdown {
		t.Fatalf("at expiry-1: expected countdown, got %s", snap.State)
	}

	snap = query(t, r, expiry)
	t.Logf("判定依据: now=%d == expiry=%d，恰等于视为已到期，以到期时刻为开局时刻进入进行中", expiry, expiry)
	if snap.State != room.StateInProgress {
		t.Fatalf("at expiry: expected in_progress, got %s", snap.State)
	}
	if snap.MatchStart != expiry {
		t.Fatalf("MatchStart=%d, want %d (到期时刻而非当前 now)", snap.MatchStart, expiry)
	}
	if len(snap.Roster) != 2 {
		t.Fatalf("roster size=%d, want 2", len(snap.Roster))
	}
}

// 倒计时中离开但条件仍成立：倒计时不受影响，起算时刻不变。
func TestLeaveDuringCountdownConditionHolds(t *testing.T) {
	r := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 10, ReportWindow: 60})
	start := joinAllReady(t, r, []string{"a", "b", "c"}, 0)
	mustOK(t, r.Leave("c", start+3))
	snap := query(t, r, start+3)
	t.Logf("判定依据: 离开后剩 2 人 >= L=2 且全体就绪，倒计时继续，起算时刻 %d 不变", start)
	if snap.State != room.StateCountdown {
		t.Fatalf("expected countdown to survive, got %s", snap.State)
	}
	if snap.CountdownStart != start || snap.CountdownExpiry != start+10 {
		t.Fatalf("countdown restart detected: [%d,%d], want [%d,%d]",
			snap.CountdownStart, snap.CountdownExpiry, start, start+10)
	}
	snap = query(t, r, start+10)
	if snap.State != room.StateInProgress || len(snap.Roster) != 2 {
		t.Fatalf("expected in_progress with 2 players, got %s roster=%d", snap.State, len(snap.Roster))
	}
}

// 倒计时中离开使条件不再成立：退回等待并清除倒计时。
func TestLeaveDuringCountdownBreaksCondition(t *testing.T) {
	r := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 10, ReportWindow: 60})
	start := joinAllReady(t, r, []string{"a", "b"}, 0)
	mustOK(t, r.Leave("b", start+3))
	snap := query(t, r, start+3)
	t.Logf("判定依据: 离开后剩 1 人 < L=2，退回等待并清除倒计时")
	if snap.State != room.StateWaiting {
		t.Fatalf("expected waiting, got %s", snap.State)
	}
	// 原到期时刻之后也不应开局。
	snap = query(t, r, start+10)
	if snap.State != room.StateWaiting {
		t.Fatalf("countdown should have been cleared, got %s", snap.State)
	}
}

// 倒计时期间加入与取消就绪都使房间立即退回等待。
func TestCountdownCancelledByJoinAndUnready(t *testing.T) {
	r := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 10, ReportWindow: 60})
	start := joinAllReady(t, r, []string{"a", "b"}, 0)
	mustOK(t, r.Join("c", start+2))
	snap := query(t, r, start+2)
	t.Logf("判定依据: 倒计时期间加入，立即退回等待")
	if snap.State != room.StateWaiting {
		t.Fatalf("join should cancel countdown, got %s", snap.State)
	}

	mustOK(t, r.SetReady("c", true, start+4))
	snap = query(t, r, start+4)
	if snap.State != room.StateCountdown || snap.CountdownStart != start+4 {
		t.Fatalf("expected new countdown from %d, got %s start=%d", start+4, snap.State, snap.CountdownStart)
	}

	mustOK(t, r.SetReady("b", false, start+6))
	snap = query(t, r, start+6)
	t.Logf("判定依据: 倒计时期间取消就绪，立即退回等待")
	if snap.State != room.StateWaiting {
		t.Fatalf("unready should cancel countdown, got %s", snap.State)
	}
}

// 等待阶段最后一名未就绪者离开：条件立即成立，以该操作的 now 起算倒计时。
func TestLastUnreadyLeavesStartsCountdown(t *testing.T) {
	r := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 10, ReportWindow: 60})
	mustOK(t, r.Join("a", 0))
	mustOK(t, r.Join("b", 1))
	mustOK(t, r.Join("c", 2))
	mustOK(t, r.SetReady("a", true, 3))
	mustOK(t, r.SetReady("b", true, 4))
	// c 未就绪；c 离开后 a、b 全体就绪且人数 2 >= L。
	mustOK(t, r.Leave("c", 5))
	snap := query(t, r, 5)
	t.Logf("判定依据: 最后一名未就绪者离开，条件成立，倒计时起算时刻 = 离开操作的 now = 5")
	if snap.State != room.StateCountdown || snap.CountdownStart != 5 {
		t.Fatalf("expected countdown from 5, got %s start=%d", snap.State, snap.CountdownStart)
	}
}

// startMatch 驱动房间进入进行中，返回开局时刻与房间。
func startMatch(t *testing.T, cfg room.Config, ids []string) (*room.Room, int64) {
	t.Helper()
	r := newRoom(t, cfg)
	start := joinAllReady(t, r, ids, 0)
	expiry := start + cfg.Countdown
	snap := query(t, r, expiry)
	if snap.State != room.StateInProgress {
		t.Fatalf("expected in_progress, got %s", snap.State)
	}
	return r, expiry
}

// 房主迁移：等待阶段房主离开，迁移给加入次序最早者；
// 进行中房主（迁移后的）离开视为中途退出，保留在对局名单中。
func TestHostMigrationAndMidGameQuit(t *testing.T) {
	r := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 10, ReportWindow: 60})
	mustOK(t, r.Join("a", 0))
	mustOK(t, r.Join("b", 1))
	mustOK(t, r.Join("c", 2))
	mustOK(t, r.Join("d", 3))
	mustOK(t, r.Leave("a", 4))
	snap := query(t, r, 4)
	t.Logf("判定依据: 房主 a 离开，迁移给加入次序最早的在室玩家 b")
	if snap.Host != "b" {
		t.Fatalf("host=%s, want b", snap.Host)
	}

	// 就绪并进入进行中。
	mustOK(t, r.SetReady("b", true, 5))
	mustOK(t, r.SetReady("c", true, 6))
	mustOK(t, r.SetReady("d", true, 7))
	snap = query(t, r, 17)
	if snap.State != room.StateInProgress {
		t.Fatalf("expected in_progress, got %s", snap.State)
	}

	// 进行中房主 b 离开：中途退出，保留在对局名单，房主迁移给 c。
	mustOK(t, r.Leave("b", 18))
	snap = query(t, r, 18)
	t.Logf("判定依据: 房主 b 在进行中离开 -> 中途退出（保留名单、取消上报资格），房主迁移给 c")
	if snap.Host != "c" {
		t.Fatalf("host=%s, want c", snap.Host)
	}
	if snap.State != room.StateInProgress {
		t.Fatalf("expected in_progress, got %s", snap.State)
	}
	var bInfo *room.MatchPlayerInfo
	for i := range snap.Roster {
		if snap.Roster[i].ID == "b" {
			bInfo = &snap.Roster[i]
		}
	}
	if bInfo == nil || bInfo.InRoom {
		t.Fatalf("b should remain in roster as quitter: %+v", bInfo)
	}

	// 中途退出者无上报资格；但可被上报为胜者。
	mustOK(t, r.End("c", 19))
	mustCode(t, r.Report("b", "b", 20), room.ErrCodeNotInRoom)
	mustOK(t, r.Report("c", "b", 21))
	mustOK(t, r.Report("d", "b", 22))
	snap = query(t, r, 22)
	if snap.State != room.StateEnded || snap.Winner != "b" {
		t.Fatalf("expected ended with winner b, got %s winner=%s", snap.State, snap.Winner)
	}
}

// 进行中在室对局玩家不足两人：对局立即作废。
func TestInProgressBelowTwoAborts(t *testing.T) {
	r, now := startMatch(t, room.Config{MinPlayers: 2, MaxPlayers: 2, Countdown: 5, ReportWindow: 60}, []string{"a", "b"})
	mustOK(t, r.Leave("a", now+1))
	snap := query(t, r, now+1)
	t.Logf("判定依据: 在室对局玩家 1 < 2，对局立即作废（不论上报情况）")
	if snap.State != room.StateAborted || snap.AbortReason != room.AbortInsufficient {
		t.Fatalf("expected aborted/insufficient, got %s/%s", snap.State, snap.AbortReason)
	}
}

// 全体在室对局玩家上报一致：进入已结束并记录胜者。
func TestSettleUnanimous(t *testing.T) {
	r, now := startMatch(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 5, ReportWindow: 60}, []string{"a", "b", "c"})
	mustOK(t, r.End("a", now+1))
	mustOK(t, r.Report("a", "b", now+2))
	mustOK(t, r.Report("b", "b", now+3))
	mustOK(t, r.Report("c", "b", now+4))
	snap := query(t, r, now+4)
	t.Logf("判定依据: 全体已上报且一致 -> 已结束，胜者 b")
	if snap.State != room.StateEnded || snap.Winner != "b" {
		t.Fatalf("expected ended/b, got %s/%s", snap.State, snap.Winner)
	}
}

// 全体已上报但不一致：作废，原因争议。
func TestSettleDispute(t *testing.T) {
	r, now := startMatch(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 5, ReportWindow: 60}, []string{"a", "b", "c"})
	mustOK(t, r.End("a", now+1))
	mustOK(t, r.Report("a", "a", now+2))
	mustOK(t, r.Report("b", "b", now+3))
	mustOK(t, r.Report("c", "b", now+4))
	snap := query(t, r, now+4)
	t.Logf("判定依据: 全体已上报但不一致 -> 已作废（争议）")
	if snap.State != room.StateAborted || snap.AbortReason != room.AbortDispute {
		t.Fatalf("expected aborted/dispute, got %s/%s", snap.State, snap.AbortReason)
	}
}

// 期限到期：已上报者一致且严格过半 -> 以该胜者结束。
func TestSettleTimeoutMajority(t *testing.T) {
	r, now := startMatch(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 5, ReportWindow: 10}, []string{"a", "b", "c", "d"})
	end := now + 1
	mustOK(t, r.End("a", end))
	mustOK(t, r.Report("a", "c", end+1))
	mustOK(t, r.Report("b", "c", end+2))
	mustOK(t, r.Report("c", "c", end+3))
	// d 未上报；期限 = end+10。3 > 4/2 且一致 -> 结束。
	snap := query(t, r, end+10)
	t.Logf("判定依据: 到期时刻未全体上报，3/4 已上报且一致，3 > 2 严格过半 -> 已结束，胜者 c")
	if snap.State != room.StateEnded || snap.Winner != "c" {
		t.Fatalf("expected ended/c, got %s/%s", snap.State, snap.Winner)
	}
}

// 期限到期：不过半（2/4 恰为一半，非严格多数）-> 作废（超时）。
func TestSettleTimeoutNoMajority(t *testing.T) {
	r, now := startMatch(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 5, ReportWindow: 10}, []string{"a", "b", "c", "d"})
	end := now + 1
	mustOK(t, r.End("a", end))
	mustOK(t, r.Report("a", "a", end+1))
	mustOK(t, r.Report("b", "a", end+2))
	snap := query(t, r, end+10)
	t.Logf("判定依据: 2/4 已上报，2 不严格多于 2 -> 已作废（超时）")
	if snap.State != room.StateAborted || snap.AbortReason != room.AbortTimeout {
		t.Fatalf("expected aborted/timeout, got %s/%s", snap.State, snap.AbortReason)
	}
}

// 拒绝次序：参数非法 > 时钟回退 > 阶段不允许 > 不在室 > 权限不足 > 状态冲突。
func TestRejectionPrecedence(t *testing.T) {
	r, now := startMatch(t, room.Config{MinPlayers: 2, MaxPlayers: 2, Countdown: 5, ReportWindow: 60}, []string{"a", "b"})

	// 参数非法优先于时钟回退。
	mustCode(t, r.SetReady("", true, now-1), room.ErrCodeInvalidParam)
	// 时钟回退优先于阶段不允许。
	mustCode(t, r.Join("x", now-1), room.ErrCodeClockRollback)
	// 阶段不允许优先于不在室（进行中不能 Join，无论用户是谁）。
	mustCode(t, r.Join("ghost", now), room.ErrCodePhaseNotAllowed)
	// 不在室优先于权限不足（非在室玩家 End）。
	mustCode(t, r.End("ghost", now), room.ErrCodeNotInRoom)
	// 权限不足（在室但非房主）。
	mustCode(t, r.End("b", now), room.ErrCodeNotHost)
	// 状态冲突：已就绪再就绪（另起一个等待中的房间）。
	r2 := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 2, Countdown: 5, ReportWindow: 60})
	mustOK(t, r2.Join("a", 0))
	mustOK(t, r2.SetReady("a", true, 1))
	mustCode(t, r2.SetReady("a", true, 2), room.ErrCodeAlreadyReady)
	// 状态冲突：已加入、已满。
	mustCode(t, r2.Join("a", 3), room.ErrCodeAlreadyJoined)
	mustOK(t, r2.Join("b", 4))
	mustCode(t, r2.Join("c", 5), room.ErrCodeRoomFull)
	t.Logf("判定依据: 各类拒绝按优先级只报第一个原因")
}

// 通过参数与时钟检查的操作即使最终被拒绝，也先做到期处理并推进时钟；
// 参数非法或时钟回退被拒的操作不做任何处理。
func TestRejectedOpStillAdvancesClock(t *testing.T) {
	r, now := startMatch(t, room.Config{MinPlayers: 2, MaxPlayers: 2, Countdown: 5, ReportWindow: 60}, []string{"a", "b"})

	// 阶段不允许的 Join（now=now+10）被拒绝，但时钟推进到 now+10。
	mustCode(t, r.Join("ghost", now+10), room.ErrCodePhaseNotAllowed)
	mustCode(t, r.Leave("a", now+5), room.ErrCodeClockRollback)
	t.Logf("判定依据: 阶段拒绝的操作已把时钟推进到 %d，now=%d 构成回退", now+10, now+5)

	// 时钟回退被拒的操作不推进时钟。
	mustOK(t, r.Leave("a", now+10))
	snap := query(t, r, now+10)
	if snap.Now != now+10 {
		t.Fatalf("clock=%d, want %d", snap.Now, now+10)
	}

	// 参数非法的操作不推进时钟、不触发到期处理。
	r2 := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 2, Countdown: 5, ReportWindow: 60})
	mustOK(t, r2.Join("a", 0))
	mustCode(t, r2.Join("", 100), room.ErrCodeInvalidParam)
	snap2 := query(t, r2, 100)
	if snap2.Now != 100 {
		t.Fatalf("clock=%d, want 100", snap2.Now)
	}
}

// 无人在室且处于等待或倒计时：房间作废。
func TestEmptyRoomAborts(t *testing.T) {
	r := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 10, ReportWindow: 60})
	mustOK(t, r.Join("a", 0))
	mustOK(t, r.Leave("a", 1))
	snap := query(t, r, 1)
	t.Logf("判定依据: 等待阶段最后一人离开 -> 已作废（空房间）")
	if snap.State != room.StateAborted || snap.AbortReason != room.AbortEmpty {
		t.Fatalf("expected aborted/empty, got %s/%s", snap.State, snap.AbortReason)
	}

	// 倒计时阶段所有人离开同样作废。
	r2 := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 10, ReportWindow: 60})
	start := joinAllReady(t, r2, []string{"a", "b"}, 0)
	mustOK(t, r2.Leave("a", start+1))
	mustOK(t, r2.Leave("b", start+2))
	snap = query(t, r2, start+2)
	if snap.State != room.StateAborted || snap.AbortReason != room.AbortEmpty {
		t.Fatalf("expected aborted/empty, got %s/%s", snap.State, snap.AbortReason)
	}
}

// 终态之后所有变更类操作报已终止；查询仍可用。
func TestTerminalStateRejectsMutations(t *testing.T) {
	r, now := startMatch(t, room.Config{MinPlayers: 2, MaxPlayers: 2, Countdown: 5, ReportWindow: 60}, []string{"a", "b"})
	mustOK(t, r.Leave("a", now+1)) // 不足两人 -> 作废
	snap := query(t, r, now+1)
	if snap.State != room.StateAborted {
		t.Fatalf("expected aborted, got %s", snap.State)
	}
	mustCode(t, r.Join("x", now+2), room.ErrCodeTerminated)
	mustCode(t, r.Leave("b", now+2), room.ErrCodeTerminated)
	mustCode(t, r.SetReady("b", true, now+2), room.ErrCodeTerminated)
	mustCode(t, r.End("b", now+2), room.ErrCodeTerminated)
	mustCode(t, r.Report("b", "b", now+2), room.ErrCodeTerminated)
	if _, err := r.Query(now + 2); err != nil {
		t.Fatalf("query on terminal room should succeed: %v", err)
	}
	t.Logf("判定依据: 已结束与已作废为终态，变更类操作一律报已终止")
}

// 其他校验：非法配置、时钟回退、胜者须在对局名单、非在室不能上报。
func TestValidationMisc(t *testing.T) {
	if _, err := room.NewRoom(room.Config{MinPlayers: 1, MaxPlayers: 4, Countdown: 5, ReportWindow: 60}); err == nil {
		t.Fatal("L=1 should be rejected")
	}
	if _, err := room.NewRoom(room.Config{MinPlayers: 2, MaxPlayers: 21, Countdown: 5, ReportWindow: 60}); err == nil {
		t.Fatal("U=21 should be rejected")
	}
	if _, err := room.NewRoom(room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 0, ReportWindow: 60}); err == nil {
		t.Fatal("C=0 should be rejected")
	}
	if _, err := room.NewRoom(room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 5, ReportWindow: 3601}); err == nil {
		t.Fatal("R=3601 should be rejected")
	}

	r, now := startMatch(t, room.Config{MinPlayers: 2, MaxPlayers: 3, Countdown: 5, ReportWindow: 60}, []string{"a", "b", "c"})
	mustOK(t, r.End("a", now+1))
	// 胜者不在对局名单。
	mustCode(t, r.Report("a", "ghost", now+2), room.ErrCodeNotInRoom)
	// 上报者不在室。
	mustCode(t, r.Report("ghost", "a", now+2), room.ErrCodeNotInRoom)
	// 时钟回退。
	mustCode(t, r.Report("a", "a", now), room.ErrCodeClockRollback)
	// now 超出取值范围。
	mustCode(t, r.Report("a", "a", room.MaxNow+1), room.ErrCodeInvalidParam)
	t.Logf("判定依据: 配置、时钟、名单合法性按优先级校验")
}

// 期限到期：过半数上报但彼此不一致 -> 作废（超时）。
func TestSettleTimeoutDisagreeingMajority(t *testing.T) {
	r, now := startMatch(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 5, ReportWindow: 10}, []string{"a", "b", "c", "d"})
	end := now + 1
	mustOK(t, r.End("a", end))
	mustOK(t, r.Report("a", "a", end+1))
	mustOK(t, r.Report("b", "b", end+2))
	mustOK(t, r.Report("c", "a", end+3))
	snap := query(t, r, end+10)
	t.Logf("判定依据: 3/4 已上报过半但不一致 -> 已作废（超时）")
	if snap.State != room.StateAborted || snap.AbortReason != room.AbortTimeout {
		t.Fatalf("expected aborted/timeout, got %s/%s", snap.State, snap.AbortReason)
	}
}

// 期限内可改报，以最后一次上报为准。
func TestReReportWithinDeadline(t *testing.T) {
	r, now := startMatch(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 5, ReportWindow: 10}, []string{"a", "b", "c"})
	end := now + 1
	mustOK(t, r.End("a", end))
	mustOK(t, r.Report("a", "a", end+1))
	mustOK(t, r.Report("b", "b", end+2))
	// 改报使双方一致。
	mustOK(t, r.Report("b", "a", end+3))
	mustOK(t, r.Report("c", "a", end+4))
	snap := query(t, r, end+4)
	t.Logf("判定依据: b 在期限内改报为 a，全体一致 -> 已结束，胜者 a")
	if snap.State != room.StateEnded || snap.Winner != "a" {
		t.Fatalf("expected ended/a after re-report, got %s/%s", snap.State, snap.Winner)
	}
}

// 到期先于操作本身生效：倒计时到期时刻的 Join 按进行中阶段拒绝；
// 上报期限到期时刻的 Report 先触发超时裁决再被拒绝。
func TestExpiryProcessedBeforeOp(t *testing.T) {
	r := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 4, Countdown: 10, ReportWindow: 10})
	start := joinAllReady(t, r, []string{"a", "b"}, 0)
	expiry := start + 10
	mustCode(t, r.Join("c", expiry), room.ErrCodePhaseNotAllowed)
	snap := query(t, r, expiry)
	t.Logf("判定依据: now=%d 恰等于到期时刻，先以到期时刻开局，Join 在进行中阶段被拒绝", expiry)
	if snap.State != room.StateInProgress || snap.MatchStart != expiry {
		t.Fatalf("expected in_progress from %d, got %s", expiry, snap.State)
	}

	// 进入结算中，只有 a 上报（1/2 不过半），期限到期时刻的 Report 应先触发超时作废。
	mustOK(t, r.End("a", expiry+1))
	deadline := expiry + 1 + 10
	mustOK(t, r.Report("a", "a", deadline-1))
	// 参数非法的操作不做任何处理（连惰性到期也不触发）。
	mustCode(t, r.Report("", "a", deadline), room.ErrCodeInvalidParam)
	mustCode(t, r.Report("b", "a", deadline), room.ErrCodeTerminated)
	snap = query(t, r, deadline)
	t.Logf("判定依据: 期限恰到期，先裁决（1/2 不过半 -> 超时作废），随后的 Report 被终态拒绝")
	if snap.State != room.StateAborted || snap.AbortReason != room.AbortTimeout {
		t.Fatalf("expected aborted/timeout, got %s/%s", snap.State, snap.AbortReason)
	}
}
