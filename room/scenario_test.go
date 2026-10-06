package room

import "testing"

// 倒计时到期：恰等于到期时刻视为已到期；差一秒仍处于倒计时。
func TestScenarioCountdownExactAndOneSecond(t *testing.T) {
	d := newDual(t, Config{L: 2, U: 3, C: 5, R: 10})
	d.run(op{kind: opJoin, user: "a", now: 1}, "首个加入者为房主")
	d.run(op{kind: opJoin, user: "b", now: 2}, "人数达到下限")
	d.run(op{kind: opReady, user: "a", now: 3}, "房主就绪")
	d.run(op{kind: opReady, user: "b", now: 4}, "全体就绪 -> 起算=4，到期=9")
	d.run(op{kind: opSnapshot, now: 8}, "now=8 < 9：仍倒计时")
	d.run(op{kind: opSnapshot, now: 9}, "now=9 == 9：恰到期，以 9 开局")
	s, _ := d.fast.Snapshot(9)
	if s.Phase != PhasePlaying || s.StartAt != 9 {
		t.Fatalf("应已开局于 9，实际 phase=%s start=%d", s.Phase, s.StartAt)
	}

	d2 := newDual(t, Config{L: 2, U: 2, C: 5, R: 10})
	d2.run(op{kind: opJoin, user: "a", now: 0}, "")
	d2.run(op{kind: opJoin, user: "b", now: 0}, "满 U")
	d2.run(op{kind: opReady, user: "a", now: 0}, "")
	d2.run(op{kind: opReady, user: "b", now: 0}, "起算=0，到期=5")
	d2.run(op{kind: opSnapshot, now: 4}, "now=4：差一秒，仍倒计时")
	s2, _ := d2.fast.Snapshot(4)
	if s2.Phase != PhaseCountdown {
		t.Fatalf("差一秒不应到期，实际 %s", s2.Phase)
	}
	d2.log()
	d.log()
}

// 倒计时中离开，但离开后人数达标且剩余者仍全部就绪：倒计时不受影响。
func TestScenarioLeaveDuringCountdownConditionHolds(t *testing.T) {
	d := newDual(t, Config{L: 2, U: 4, C: 10, R: 10})
	d.run(op{kind: opJoin, user: "a", now: 0}, "")
	d.run(op{kind: opJoin, user: "b", now: 0}, "")
	d.run(op{kind: opJoin, user: "c", now: 0}, "三人在室")
	d.run(op{kind: opReady, user: "a", now: 0}, "")
	d.run(op{kind: opReady, user: "b", now: 0}, "")
	d.run(op{kind: opReady, user: "c", now: 0}, "起算=0，到期=10")
	d.run(op{kind: opLeave, user: "c", now: 2}, "已就绪者离开；剩 2 人仍全就绪，起算时刻不变")
	s, _ := d.fast.Snapshot(2)
	if s.Phase != PhaseCountdown || s.CountdownStart != 0 {
		t.Fatalf("倒计时应保持，phase=%s start=%d", s.Phase, s.CountdownStart)
	}
	d.run(op{kind: opSnapshot, now: 9}, "仍倒计时")
	d.run(op{kind: opSnapshot, now: 10}, "到期，按原起算时刻 0 开局")
	s2, _ := d.fast.Snapshot(10)
	if s2.Phase != PhasePlaying {
		t.Fatalf("应进行中，实际 %s", s2.Phase)
	}
	d.log()
}

// 房主迁移后原房主在进行中离开：算中途退出而非消失，新房主可 End。
func TestScenarioOwnerMigrationThenQuit(t *testing.T) {
	d := newDual(t, Config{L: 2, U: 3, C: 2, R: 10})
	d.run(op{kind: opJoin, user: "a", now: 0}, "a 为房主")
	d.run(op{kind: opJoin, user: "b", now: 0}, "")
	d.run(op{kind: opJoin, user: "c", now: 0}, "")
	d.run(op{kind: opReady, user: "a", now: 0}, "")
	d.run(op{kind: opReady, user: "b", now: 0}, "")
	d.run(op{kind: opReady, user: "c", now: 0}, "倒计时起算=0")
	d.run(op{kind: opLeave, user: "a", now: 1}, "倒计时中房主离开；b,c 仍全就绪 -> 倒计时保留，房主迁移给 b")
	s, _ := d.fast.Snapshot(1)
	if s.Owner != "b" || s.Phase != PhaseCountdown {
		t.Fatalf("房主应为 b 且倒计时保留，owner=%s phase=%s", s.Owner, s.Phase)
	}
	d.run(op{kind: opSnapshot, now: 2}, "到期，开局名单 b,c（a 已不在室），开局于 2")
	s2, _ := d.fast.Snapshot(2)
	if s2.Phase != PhasePlaying || len(s2.Roster) != 2 {
		t.Fatalf("对局名单应为 b,c，实际 %v", s2.Roster)
	}

	// 进入对局后原房主再离开：中途退出 + 迁移，新房主 End，胜者可含退出者。
	d3 := newDual(t, Config{L: 2, U: 3, C: 1, R: 20})
	d3.run(op{kind: opJoin, user: "a", now: 0}, "a 房主")
	d3.run(op{kind: opJoin, user: "b", now: 0}, "")
	d3.run(op{kind: opJoin, user: "c", now: 0}, "")
	for _, u := range []string{"a", "b", "c"} {
		d3.run(op{kind: opReady, user: u, now: 0}, "")
	}
	d3.run(op{kind: opSnapshot, now: 1}, "到期开局，名单 a,b,c")
	d3.run(op{kind: opLeave, user: "a", now: 2}, "房主 a 进行中离开 -> 中途退出，房主迁移给 b")
	s3, _ := d3.fast.Snapshot(2)
	if s3.Owner != "b" || !s3.Quitters["a"] {
		t.Fatalf("房主应迁移给 b 且 a 为中途退出者，owner=%s quit=%v", s3.Owner, s3.Quitters)
	}
	d3.run(op{kind: opEnd, user: "a", now: 3}, "已退出的 a End -> 不在室")
	d3.run(op{kind: opEnd, user: "c", now: 3}, "c 在室但非房主 -> not_owner")
	d3.run(op{kind: opEnd, user: "b", now: 3}, "新房主 b End -> 结算中，期限=23")
	s4, _ := d3.fast.Snapshot(3)
	if s4.Phase != PhaseSettling || s4.ReportDeadline != 23 {
		t.Fatalf("应进入结算且期限 23，phase=%s due=%d", s4.Phase, s4.ReportDeadline)
	}
	d3.run(op{kind: opReport, user: "a", winner: "b", now: 4}, "中途退出者上报被拒")
	d3.run(op{kind: opReport, user: "b", winner: "a", now: 4}, "b 可上报已退出的 a")
	d3.run(op{kind: opReport, user: "c", winner: "a", now: 5}, "c 一致 -> 结束，胜者 a")
	s5, _ := d3.fast.Snapshot(5)
	if s5.Phase != PhaseEnded || s5.Winner != "a" {
		t.Fatalf("应结束且胜者 a，phase=%s winner=%s", s5.Phase, s5.Winner)
	}
	d3.log()
	d.log()
}

// 进行中在室玩家降到不足 2：立即作废，终态后变更报 terminated。
func TestScenarioPlayingBelowTwoVoid(t *testing.T) {
	d := newDual(t, Config{L: 2, U: 2, C: 1, R: 10})
	d.run(op{kind: opJoin, user: "a", now: 0}, "")
	d.run(op{kind: opJoin, user: "b", now: 0}, "")
	d.run(op{kind: opReady, user: "a", now: 0}, "")
	d.run(op{kind: opReady, user: "b", now: 0}, "")
	d.run(op{kind: opSnapshot, now: 1}, "开局")
	d.run(op{kind: opLeave, user: "a", now: 2}, "a 退出，在室仅剩 b -> 立即作废")
	s, _ := d.fast.Snapshot(2)
	if s.Phase != PhaseVoided || s.Void != VoidTooFew {
		t.Fatalf("应以 too_few 作废，phase=%s void=%s", s.Phase, s.Void)
	}
	d.run(op{kind: opJoin, user: "x", now: 3}, "终态变更 -> terminated")
	d.log()
}
