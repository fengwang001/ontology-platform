package room

import "testing"

// 全体一致结束 / 不一致争议作废 / 期限内改报。
func TestScenarioSettlementUnanimousDisputeChange(t *testing.T) {
	d := newDual(t, Config{L: 2, U: 3, C: 1, R: 100})
	for _, u := range []string{"a", "b", "c"} {
		d.run(op{kind: opJoin, user: u, now: 0}, "")
		d.run(op{kind: opReady, user: u, now: 0}, "")
	}
	d.run(op{kind: opSnapshot, now: 1}, "开局 3 人")
	d.run(op{kind: opEnd, user: "a", now: 2}, "a 房主 End")
	d.run(op{kind: opReport, user: "a", winner: "x", now: 3}, "胜者 x 不在名单 -> not_present")
	d.run(op{kind: opReport, user: "a", winner: "a", now: 3}, "a 报 a")
	d.run(op{kind: opReport, user: "b", winner: "a", now: 4}, "b 报 a；c 未报，仍结算")
	d.run(op{kind: opReport, user: "b", winner: "b", now: 5}, "b 改报 b，覆盖旧票，仍结算")
	s, _ := d.fast.Snapshot(5)
	if s.Phase != PhaseSettling {
		t.Fatalf("改报后不应提前裁决，phase=%s", s.Phase)
	}
	d.run(op{kind: opReport, user: "a", winner: "b", now: 6}, "a 改报 b（旧 a 票撤销），c 尚未报")
	d.run(op{kind: opReport, user: "c", winner: "b", now: 7}, "c 报 b -> 全体一致，结束")
	s2, _ := d.fast.Snapshot(7)
	if s2.Phase != PhaseEnded || s2.Winner != "b" {
		t.Fatalf("应以胜者 b 结束，phase=%s winner=%s", s2.Phase, s2.Winner)
	}

	d2 := newDual(t, Config{L: 2, U: 3, C: 1, R: 100})
	for _, u := range []string{"a", "b", "c"} {
		d2.run(op{kind: opJoin, user: u, now: 0}, "")
		d2.run(op{kind: opReady, user: u, now: 0}, "")
	}
	d2.run(op{kind: opSnapshot, now: 1}, "开局")
	d2.run(op{kind: opEnd, user: "a", now: 2}, "")
	d2.run(op{kind: opReport, user: "a", winner: "a", now: 3}, "")
	d2.run(op{kind: opReport, user: "b", winner: "a", now: 3}, "")
	d2.run(op{kind: opReport, user: "c", winner: "b", now: 3}, "全体已报但不一致 -> 争议作废")
	s3, _ := d2.fast.Snapshot(3)
	if s3.Phase != PhaseVoided || s3.Void != VoidDispute {
		t.Fatalf("应争议作废，phase=%s void=%s", s3.Phase, s3.Void)
	}
	d2.log()
	d.log()
}

// 超时：过半一致结束；恰好半数 / 不一致则超时作废。
func TestScenarioTimeoutMajority(t *testing.T) {
	d := newDual(t, Config{L: 2, U: 4, C: 1, R: 10})
	for _, u := range []string{"a", "b", "c", "e"} {
		d.run(op{kind: opJoin, user: u, now: 0}, "")
		d.run(op{kind: opReady, user: u, now: 0}, "")
	}
	d.run(op{kind: opSnapshot, now: 1}, "开局，4 人")
	d.run(op{kind: opEnd, user: "a", now: 2}, "期限=12")
	d.run(op{kind: opReport, user: "a", winner: "a", now: 3}, "")
	d.run(op{kind: opReport, user: "b", winner: "a", now: 3}, "已报 2/4 一致，恰半数")
	d.run(op{kind: opReport, user: "c", winner: "a", now: 11}, "期限前再报：3/4，尚未全体")
	s, _ := d.fast.Snapshot(11)
	if s.Phase != PhaseSettling {
		t.Fatalf("未满员上报应继续结算，phase=%s", s.Phase)
	}
	d.run(op{kind: opSnapshot, now: 12}, "到期：3>2 过半一致 -> 结束，胜者 a")
	s2, _ := d.fast.Snapshot(12)
	if s2.Phase != PhaseEnded || s2.Winner != "a" {
		t.Fatalf("应过半一致结束，phase=%s winner=%s", s2.Phase, s2.Winner)
	}

	// 2 人对局仅 1 人上报（1 不 > 1）-> 超时作废。
	d2 := newDual(t, Config{L: 2, U: 2, C: 1, R: 5})
	d2.run(op{kind: opJoin, user: "a", now: 0}, "")
	d2.run(op{kind: opJoin, user: "b", now: 0}, "")
	d2.run(op{kind: opReady, user: "a", now: 0}, "")
	d2.run(op{kind: opReady, user: "b", now: 0}, "")
	d2.run(op{kind: opSnapshot, now: 1}, "开局")
	d2.run(op{kind: opEnd, user: "a", now: 2}, "期限=7")
	d2.run(op{kind: opReport, user: "a", winner: "b", now: 3}, "仅 a 上报")
	d2.run(op{kind: opSnapshot, now: 7}, "到期恰等于：1 票不严格过半 -> 超时作废")
	s3, _ := d2.fast.Snapshot(7)
	if s3.Phase != PhaseVoided || s3.Void != VoidTimeout {
		t.Fatalf("应超时作废，phase=%s void=%s", s3.Phase, s3.Void)
	}

	// 已上报者不一致：即使票数过半也作废（无一致胜者）。
	d3 := newDual(t, Config{L: 2, U: 3, C: 1, R: 5})
	for _, u := range []string{"a", "b", "c"} {
		d3.run(op{kind: opJoin, user: u, now: 0}, "")
		d3.run(op{kind: opReady, user: u, now: 0}, "")
	}
	d3.run(op{kind: opSnapshot, now: 1}, "开局 3 人")
	d3.run(op{kind: opEnd, user: "a", now: 2}, "期限=7")
	d3.run(op{kind: opReport, user: "a", winner: "a", now: 3}, "")
	d3.run(op{kind: opReport, user: "b", winner: "b", now: 3}, "两派不一致")
	d3.run(op{kind: opReport, user: "b", winner: "a", now: 8}, "到期后的上报被拒（到期先于操作生效）")
	s4, _ := d3.fast.Snapshot(8)
	if s4.Phase != PhaseVoided || s4.Void != VoidTimeout {
		t.Fatalf("到期应超时作废且上报被拒，phase=%s void=%s", s4.Phase, s4.Void)
	}
	d3.log()
	d2.log()
	d.log()
}

// 到期先于操作生效：倒计时恰到期时的“取消就绪/加入”不再取消倒计时，
// 而是先开局再按进行中规则拒绝。
func TestScenarioExpiryPrecedesOperation(t *testing.T) {
	d := newDual(t, Config{L: 2, U: 3, C: 5, R: 10})
	d.run(op{kind: opJoin, user: "a", now: 0}, "")
	d.run(op{kind: opJoin, user: "b", now: 0}, "")
	d.run(op{kind: opReady, user: "a", now: 0}, "")
	d.run(op{kind: opReady, user: "b", now: 0}, "起算=0，到期=5")
	d.run(op{kind: opUnready, user: "a", now: 5}, "now==到期：先开局，进行中 SetReady 阶段不允许")
	s, _ := d.fast.Snapshot(5)
	if s.Phase != PhasePlaying || s.StartAt != 5 {
		t.Fatalf("应先开局，phase=%s start=%d", s.Phase, s.StartAt)
	}
	d.run(op{kind: opJoin, user: "c", now: 6}, "进行中加入 -> 阶段不允许")

	// 空房作废同样惰性可见：等待态清空后房间立即作废。
	d2 := newDual(t, Config{L: 2, U: 3, C: 5, R: 10})
	d2.run(op{kind: opJoin, user: "a", now: 0}, "")
	d2.run(op{kind: opLeave, user: "a", now: 1}, "唯一玩家在等待态离开 -> 已作废")
	s2, _ := d2.fast.Snapshot(1)
	if s2.Phase != PhaseVoided || s2.Void != VoidEmpty {
		t.Fatalf("应空房作废，phase=%s void=%s", s2.Phase, s2.Void)
	}
	d2.log()
	d.log()
}

// 拒绝次序与时钟回退：只报第一个原因；回退操作无任何副作用。
func TestScenarioRejectOrderAndClock(t *testing.T) {
	d := newDual(t, Config{L: 2, U: 2, C: 5, R: 10})
	d.run(op{kind: opJoin, user: "", now: 0}, "空标识：参数非法优先于一切")
	d.run(op{kind: opJoin, user: "a", now: -1}, "负 now：参数非法")
	d.run(op{kind: opJoin, user: "a", now: 5}, "合法：房间时钟推进到 5")
	d.run(op{kind: opJoin, user: "b", now: 4}, "时钟回退：不做任何处理（即便房间已满也先报回退）")
	d.run(op{kind: opJoin, user: "c", now: 5}, "当前等待态 1 人未满，但 c 并未因上一条加入")
	s, _ := d.fast.Snapshot(5)
	if len(s.Present) != 2 || s.Owner != "a" {
		t.Fatalf("回退操作不得产生变化，present=%v", s.Present)
	}
	d.run(op{kind: opJoin, user: "b", now: 6}, "已加入：状态冲突")
	d.run(op{kind: opJoin, user: "c", now: 6}, "房间已满：状态冲突")
	d.run(op{kind: opReady, user: "x", now: 6}, "不在室")
	d.run(op{kind: opReady, user: "a", now: 6}, "a 就绪")
	d.run(op{kind: opReady, user: "a", now: 7}, "已就绪再就绪：冲突")
	d.run(op{kind: opLeave, user: "x", now: 7}, "不存在玩家离开：不在室")

	// 终态变更统一报 terminated，优先于阶段/身份等检查。
	d2 := newDual(t, Config{L: 2, U: 2, C: 1, R: 2})
	d2.run(op{kind: opJoin, user: "a", now: 0}, "")
	d2.run(op{kind: opJoin, user: "b", now: 0}, "")
	d2.run(op{kind: opReady, user: "a", now: 0}, "")
	d2.run(op{kind: opReady, user: "b", now: 0}, "")
	d2.run(op{kind: opSnapshot, now: 1}, "开局")
	d2.run(op{kind: opLeave, user: "a", now: 1}, "剩 1 人 -> 作废")
	d2.run(op{kind: opReport, user: "b", winner: "b", now: 2}, "终态 -> terminated（优先于阶段/身份）")
	d2.log()
	d.log()
}

// 最后一名未就绪者离开：等待操作完成后条件成立，立即进入倒计时。
func TestScenarioLastUnreadyLeavesStartsCountdown(t *testing.T) {
	d := newDual(t, Config{L: 2, U: 3, C: 10, R: 10})
	d.run(op{kind: opJoin, user: "a", now: 0}, "")
	d.run(op{kind: opJoin, user: "b", now: 0}, "")
	d.run(op{kind: opJoin, user: "c", now: 0}, "")
	d.run(op{kind: opReady, user: "a", now: 0}, "")
	d.run(op{kind: opReady, user: "b", now: 0}, "a,b 就绪；c 未就绪，等待中")
	d.run(op{kind: opLeave, user: "c", now: 3}, "未就绪者离开：剩 2 人全就绪 -> 立即倒计时，起算=3")
	s, _ := d.fast.Snapshot(3)
	if s.Phase != PhaseCountdown || s.CountdownStart != 3 {
		t.Fatalf("应自 now=3 起倒计时，phase=%s start=%d", s.Phase, s.CountdownStart)
	}
	d.run(op{kind: opSnapshot, now: 12}, "到期 13 未到：仍倒计时")
	d.run(op{kind: opSnapshot, now: 13}, "到期：以 13 开局")
	s2, _ := d.fast.Snapshot(13)
	if s2.Phase != PhasePlaying || s2.StartAt != 13 {
		t.Fatalf("应于 13 开局，phase=%s start=%d", s2.Phase, s2.StartAt)
	}
	d.log()
}
