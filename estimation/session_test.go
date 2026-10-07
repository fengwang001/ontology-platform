package estimation

import (
	"fmt"
	"sync"
	"testing"
)

var testDeck = []int{1, 2, 3, 5, 8, 13}

func newTestSession(t *testing.T, rounds int, ttl int64, auto bool) *Session {
	t.Helper()
	s, err := NewSession("host", testDeck, rounds, ttl, auto)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return s
}

func joinOK(t *testing.T, s *Session, user string, role Role, now int64) {
	t.Helper()
	if _, err := s.Join(user, role, now); err != nil {
		t.Fatalf("Join(%s,%s,%d): %v", user, role, now, err)
	}
}

func startOK(t *testing.T, s *Session, now int64) {
	t.Helper()
	if _, err := s.Start("host", now); err != nil {
		t.Fatalf("Start(%d): %v", now, err)
	}
}

func voteOK(t *testing.T, s *Session, user string, card Card, now int64) {
	t.Helper()
	if _, err := s.Vote(user, card, now); err != nil {
		t.Fatalf("Vote(%s,%s,%d): %v", user, card, now, err)
	}
}

func revealOK(t *testing.T, s *Session, now int64) *RevealOutcome {
	t.Helper()
	out, err := s.Reveal("host", now)
	if err != nil {
		t.Fatalf("Reveal(%d): %v", now, err)
	}
	if out == nil {
		t.Fatalf("Reveal(%d): 应返回揭示结果", now)
	}
	return out
}

func expectErr(t *testing.T, op string, err *Error, code ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: 应拒绝为 %s，实际成功", op, code)
	}
	if err.Code != code {
		t.Fatalf("%s: 应拒绝为 %s，实际 %s (%v)", op, code, err.Code, err)
	}
}

// 时限：恰等于到期时刻视为已到期，差一秒则未到期。
func TestDeadlineExactAndOneSecondBefore(t *testing.T) {
	s := newTestSession(t, 3, 100, false)
	joinOK(t, s, "alice", RoleVoter, 0)
	startOK(t, s, 1000) // 到期时刻 1100

	// 差一秒（1099）：未到期，投票正常受理，不产生揭示。
	out, err := s.Vote("alice", Card(1), 1099)
	if err != nil || out != nil {
		t.Fatalf("差一秒时投票应受理且无揭示: out=%v err=%v", out, err)
	}
	view, exp, err := s.Peek("alice", 1099)
	if err != nil || exp != nil || !view.HasVote || view.Self != Card(1) || view.VotedCount != 1 {
		t.Fatalf("差一秒时 Peek 应正常且无到期揭示: view=%+v exp=%v err=%v", view, exp, err)
	}

	// 恰等于到期时刻（1100）：先在到期时刻揭示，投票因议题已结束被拒。
	out, err = s.Vote("alice", Card(2), 1100)
	expectErr(t, "恰等于到期时刻投票", err, ErrInvalidState)
	if out == nil || out.Trigger != TriggerExpired || out.RevealedAt != 1100 {
		t.Fatalf("应在到期时刻 1100 惰性揭示: %+v", out)
	}
	if out.Kind != ResultConsensus || !out.HasValue || out.Value != 1 {
		t.Fatalf("到期揭示应按已投的 1 票判定共识: %+v", out)
	}
	if got := s.Status(); got.Phase != PhaseIdle {
		t.Fatalf("共识后议题应结束: %+v", got)
	}
}

// 自动揭示由投票者离开触发。
func TestAutoRevealTriggeredByLeave(t *testing.T) {
	s := newTestSession(t, 3, 1000, true)
	joinOK(t, s, "a", RoleVoter, 0)
	joinOK(t, s, "b", RoleVoter, 0)
	startOK(t, s, 10)
	voteOK(t, s, "a", Card(3), 11)

	// b 尚未投票，此时离开使“全体在室投票者都已投票”成立。
	out, err := s.Leave("b", 12)
	if err != nil {
		t.Fatalf("Leave: %v", err)
	}
	if out == nil || out.Trigger != TriggerAuto || out.RevealedAt != 12 {
		t.Fatalf("离开未投票者应触发自动揭示: %+v", out)
	}
	if out.Kind != ResultConsensus || out.Value != 3 || out.NumericVotes != 1 {
		t.Fatalf("揭示结果不符: %+v", out)
	}
}

// 自动揭示由投票者改为观察者触发（含主持人代改与本人改）。
func TestAutoRevealTriggeredBySetRole(t *testing.T) {
	s := newTestSession(t, 3, 1000, true)
	joinOK(t, s, "a", RoleVoter, 0)
	joinOK(t, s, "b", RoleVoter, 0)
	startOK(t, s, 10)
	voteOK(t, s, "a", Card(5), 11)

	out, err := s.SetRole("host", "b", RoleObserver, 12)
	if err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	if out == nil || out.Trigger != TriggerAuto || out.RevealedAt != 12 {
		t.Fatalf("改角色应触发自动揭示: %+v", out)
	}
	if out.Kind != ResultConsensus || out.Value != 5 {
		t.Fatalf("揭示结果不符: %+v", out)
	}

	// 本人改角色同样触发。
	s2 := newTestSession(t, 3, 1000, true)
	joinOK(t, s2, "a", RoleVoter, 0)
	joinOK(t, s2, "b", RoleVoter, 0)
	startOK(t, s2, 10)
	voteOK(t, s2, "a", Card(8), 11)
	out, err = s2.SetRole("b", "b", RoleObserver, 12)
	if err != nil {
		t.Fatalf("SetRole self: %v", err)
	}
	if out == nil || out.Trigger != TriggerAuto || out.Value != 8 {
		t.Fatalf("本人改角色应触发自动揭示: %+v", out)
	}
}

// 特殊牌计入已投人数与分布，但不参与统计。
func TestSpecialCardsCountedButNotTallied(t *testing.T) {
	s := newTestSession(t, 3, 1000, false)
	joinOK(t, s, "a", RoleVoter, 0)
	joinOK(t, s, "b", RoleVoter, 0)
	startOK(t, s, 10)
	voteOK(t, s, "a", CardUnsure, 11)
	voteOK(t, s, "b", CardBreak, 12)

	view, _, err := s.Peek("a", 13)
	if err != nil || !view.HasVote || view.Self != CardUnsure || view.VotedCount != 2 {
		t.Fatalf("特殊牌应计入已投人数: view=%+v err=%v", view, err)
	}

	// 只有特殊牌也满足“至少一张已投”，Reveal 不报无人投票。
	out := revealOK(t, s, 14)
	if out.NumericVotes != 0 || out.Kind != ResultNoValidVotes {
		t.Fatalf("特殊牌不参与统计，应为无有效票: %+v", out)
	}
	if out.Distribution[CardUnsure] != 1 || out.Distribution[CardBreak] != 1 {
		t.Fatalf("分布应包含特殊牌: %+v", out.Distribution)
	}

	// 特殊牌与数值牌混合：统计只看数值牌。
	s2 := newTestSession(t, 3, 1000, false)
	joinOK(t, s2, "a", RoleVoter, 0)
	joinOK(t, s2, "b", RoleVoter, 0)
	startOK(t, s2, 10)
	voteOK(t, s2, "a", Card(5), 11)
	voteOK(t, s2, "b", CardUnsure, 12)
	out = revealOK(t, s2, 13)
	if out.NumericVotes != 1 || out.Kind != ResultConsensus || out.Value != 5 {
		t.Fatalf("混合投票应只统计数值牌: %+v", out)
	}
}

// 相邻为收敛（取较大者），不相邻为分歧。
func TestAdjacentVsNonAdjacent(t *testing.T) {
	// 1 与 2 在牌组中相邻 -> 收敛，取较大者 2。
	s := newTestSession(t, 3, 1000, false)
	joinOK(t, s, "a", RoleVoter, 0)
	joinOK(t, s, "b", RoleVoter, 0)
	startOK(t, s, 10)
	voteOK(t, s, "a", Card(1), 11)
	voteOK(t, s, "b", Card(2), 12)
	out := revealOK(t, s, 13)
	if out.Kind != ResultConverged || !out.HasValue || out.Value != 2 || !out.IssueEnded {
		t.Fatalf("相邻应为收敛并取较大者: %+v", out)
	}

	// 1 与 3 在牌组中隔一位 -> 分歧，议题未结束。
	s2 := newTestSession(t, 3, 1000, false)
	joinOK(t, s2, "a", RoleVoter, 0)
	joinOK(t, s2, "b", RoleVoter, 0)
	startOK(t, s2, 10)
	voteOK(t, s2, "a", Card(1), 11)
	voteOK(t, s2, "b", Card(3), 12)
	out = revealOK(t, s2, 13)
	if out.Kind != ResultDiverged || out.HasValue || out.IssueEnded {
		t.Fatalf("不相邻应为分歧: %+v", out)
	}
	if got := s2.Status(); got.Phase != PhaseRevealed {
		t.Fatalf("分歧后应处于已揭示待决状态: %+v", got)
	}
}

// 轮次用尽时分歧强制取值：数值票按牌组位置排序取下中位，
// 偶数张取靠前的那一张。
func TestForcedLowerMedian(t *testing.T) {
	// 偶数张：{1,2,3,5} -> 下中位为第 2 张 2。
	s := newTestSession(t, 1, 1000, false)
	for i, u := range []string{"a", "b", "c", "d"} {
		joinOK(t, s, u, RoleVoter, int64(i))
	}
	startOK(t, s, 10)
	voteOK(t, s, "a", Card(1), 11)
	voteOK(t, s, "b", Card(2), 12)
	voteOK(t, s, "c", Card(3), 13)
	voteOK(t, s, "d", Card(5), 14)
	out := revealOK(t, s, 15)
	if out.Kind != ResultForced || !out.HasValue || out.Value != 2 || !out.IssueEnded {
		t.Fatalf("偶数张应强制取下中位 2: %+v", out)
	}

	// 奇数张：{1,2,8} -> 中位 2。
	s2 := newTestSession(t, 1, 1000, false)
	for i, u := range []string{"a", "b", "c"} {
		joinOK(t, s2, u, RoleVoter, int64(i))
	}
	startOK(t, s2, 10)
	voteOK(t, s2, "a", Card(1), 11)
	voteOK(t, s2, "b", Card(2), 12)
	voteOK(t, s2, "c", Card(8), 13)
	out = revealOK(t, s2, 14)
	if out.Kind != ResultForced || out.Value != 2 {
		t.Fatalf("奇数张应强制取中位 2: %+v", out)
	}

	// 含重复票：{1,1,13,13} -> 排序后 [1,1,13,13]，下中位为 1。
	s3 := newTestSession(t, 1, 1000, false)
	for i, u := range []string{"a", "b", "c", "d"} {
		joinOK(t, s3, u, RoleVoter, int64(i))
	}
	startOK(t, s3, 10)
	voteOK(t, s3, "a", Card(1), 11)
	voteOK(t, s3, "b", Card(1), 12)
	voteOK(t, s3, "c", Card(13), 13)
	voteOK(t, s3, "d", Card(13), 14)
	out = revealOK(t, s3, 15)
	if out.Kind != ResultForced || out.Value != 1 {
		t.Fatalf("含重复票应强制取下中位 1: %+v", out)
	}
}

// 分歧的两条路径：轮次未用尽可 Revote；轮次用尽强制取值且议题结束。
func TestDivergenceRoundsPaths(t *testing.T) {
	s := newTestSession(t, 2, 1000, false)
	joinOK(t, s, "a", RoleVoter, 0)
	joinOK(t, s, "b", RoleVoter, 0)
	startOK(t, s, 10)
	voteOK(t, s, "a", Card(1), 11)
	voteOK(t, s, "b", Card(3), 12)
	out := revealOK(t, s, 13)
	if out.Kind != ResultDiverged || out.Round != 1 {
		t.Fatalf("第 1 轮应为分歧: %+v", out)
	}

	// 轮次未用尽：Revote 开始第 2 轮，清空全部投票。
	if _, err := s.Revote("host", 20); err != nil {
		t.Fatalf("Revote: %v", err)
	}
	st := s.Status()
	if st.Phase != PhaseVoting || st.Round != 2 || st.RoundStart != 20 || st.Voted != 0 {
		t.Fatalf("新一轮状态不符: %+v", st)
	}

	// 第 2 轮仍分歧且轮次用尽 -> 强制取值，议题结束，不可再 Revote。
	voteOK(t, s, "a", Card(1), 21)
	voteOK(t, s, "b", Card(3), 22)
	out = revealOK(t, s, 23)
	if out.Kind != ResultForced || !out.IssueEnded || out.Value != 1 {
		t.Fatalf("轮次用尽应强制取值: %+v", out)
	}
	_, err := s.Revote("host", 30)
	expectErr(t, "议题结束后 Revote", err, ErrInvalidState)
	_, err = s.Vote("a", Card(1), 31)
	expectErr(t, "议题结束后 Vote", err, ErrInvalidState)
	_, err = s.Reveal("host", 32)
	expectErr(t, "议题结束后 Reveal", err, ErrInvalidState)
}

// 无有效票的两条路径：轮次未用尽可 Revote；轮次用尽终局无结果。
// 另覆盖到期时无人投票按无有效票处理。
func TestNoValidVotesTwoPaths(t *testing.T) {
	s := newTestSession(t, 2, 1000, false)
	joinOK(t, s, "a", RoleVoter, 0)
	startOK(t, s, 10)
	voteOK(t, s, "a", CardUnsure, 11)
	out := revealOK(t, s, 12)
	if out.Kind != ResultNoValidVotes || out.IssueEnded {
		t.Fatalf("第 1 轮应为无有效票且未终局: %+v", out)
	}
	if _, err := s.Revote("host", 20); err != nil {
		t.Fatalf("无有效票且轮次未尽应可 Revote: %v", err)
	}
	voteOK(t, s, "a", CardBreak, 21)
	out = revealOK(t, s, 22)
	if out.Kind != ResultFinalNoResult || !out.IssueEnded || out.HasValue {
		t.Fatalf("轮次用尽应终局无结果: %+v", out)
	}

	// 到期时无人投票 -> 按无有效票处理（R=1 直接终局无结果）。
	s2 := newTestSession(t, 1, 50, false)
	joinOK(t, s2, "a", RoleVoter, 0)
	startOK(t, s2, 100)
	out, err := s2.Join("b", RoleVoter, 150) // 恰等于到期时刻
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if out == nil || out.Trigger != TriggerExpired || out.RevealedAt != 150 ||
		out.Kind != ResultFinalNoResult || out.NumericVotes != 0 || len(out.Distribution) != 0 {
		t.Fatalf("到期无人投票应按无有效票处理: %+v", out)
	}
}

// 新议题：轮数重置、投票清空、成员保留。
func TestNewIssueResets(t *testing.T) {
	s := newTestSession(t, 2, 1000, false)
	joinOK(t, s, "a", RoleVoter, 0)
	joinOK(t, s, "b", RoleObserver, 0)
	startOK(t, s, 10)
	voteOK(t, s, "a", Card(1), 11)
	joinOK(t, s, "c", RoleVoter, 12) // 投票阶段加入，立即计入全体
	voteOK(t, s, "c", Card(2), 13)
	out := revealOK(t, s, 14)
	if out.Kind != ResultConverged || !out.IssueEnded {
		t.Fatalf("第 1 个议题应收敛结束: %+v", out)
	}

	// 议题结束后除 Start 外不再接受投票与揭示。
	_, err := s.Vote("a", Card(1), 15)
	expectErr(t, "议题结束后 Vote", err, ErrInvalidState)
	_, err = s.Reveal("host", 15)
	expectErr(t, "议题结束后 Reveal", err, ErrInvalidState)

	// Start 开启新议题：轮数重置为 1，投票清空，成员保留。
	startOK(t, s, 100)
	st := s.Status()
	if st.Phase != PhaseVoting || st.Round != 1 || st.RoundStart != 100 || st.Voted != 0 || st.Voters != 2 {
		t.Fatalf("新议题状态不符: %+v", st)
	}
	// 成员保留：a、c 无需重新 Join 即可投票。
	voteOK(t, s, "a", Card(8), 101)
	voteOK(t, s, "c", Card(8), 102)
	out = revealOK(t, s, 103)
	if out.Kind != ResultConsensus || out.Value != 8 || out.Round != 1 {
		t.Fatalf("新议题第 1 轮应为共识: %+v", out)
	}
}

// 揭示前零泄露：Peek 只返回本人投票与全体已投人数。
func TestZeroLeakBeforeReveal(t *testing.T) {
	s := newTestSession(t, 3, 1000, false)
	joinOK(t, s, "a", RoleVoter, 0)
	joinOK(t, s, "b", RoleVoter, 0)
	joinOK(t, s, "o", RoleObserver, 0)
	startOK(t, s, 10)
	voteOK(t, s, "a", Card(13), 11)

	// b 只能看到已投人数，看不到 a 的牌。
	view, _, err := s.Peek("b", 12)
	if err != nil || view.HasVote || view.VotedCount != 1 {
		t.Fatalf("他人视角不应含任何牌: view=%+v err=%v", view, err)
	}
	// 观察者同样只能看到已投人数。
	view, _, err = s.Peek("o", 13)
	if err != nil || view.HasVote || view.VotedCount != 1 {
		t.Fatalf("观察者视角不应含任何牌: view=%+v err=%v", view, err)
	}
	// 本人能看到自己的票。
	view, _, err = s.Peek("a", 14)
	if err != nil || !view.HasVote || view.Self != Card(13) || view.VotedCount != 1 {
		t.Fatalf("本人视角应含本人的票: view=%+v err=%v", view, err)
	}
	// 改投后他人视角仍只有人数。
	voteOK(t, s, "a", Card(5), 15)
	view, _, err = s.Peek("b", 16)
	if err != nil || view.HasVote || view.VotedCount != 1 {
		t.Fatalf("改投后他人视角仍应只有人数: view=%+v err=%v", view, err)
	}
}

// 被拒绝次序：参数非法 > 时钟回退 > 不在会话 > 权限不足 > 状态不允许 > 无人投票。
func TestErrorPrecedence(t *testing.T) {
	s := newTestSession(t, 3, 1000, false)
	joinOK(t, s, "v", RoleVoter, 0)
	joinOK(t, s, "o", RoleObserver, 1)
	startOK(t, s, 10) // lastNow = 10

	// 参数非法优先于时钟回退与不在会话。
	_, err := s.Vote("ghost", Card(999), 5)
	expectErr(t, "非法牌+回退+不在会话", err, ErrInvalidParam)
	// 时钟回退优先于不在会话。
	_, err = s.Vote("ghost", Card(1), 5)
	expectErr(t, "回退+不在会话", err, ErrClockRegression)
	// 不在会话优先于权限不足。
	_, err = s.Reveal("ghost", 10)
	expectErr(t, "不在会话+无权限", err, ErrNotInSession)
	// 权限不足优先于状态不允许：观察者投票，且当前并非投票阶段问题不存在——
	// 这里议题在投票阶段，改用议题结束后观察者投票验证。
	_, err = s.SetRole("o", "v", RoleObserver, 11)
	expectErr(t, "非主持人改他人角色", err, ErrPermissionDenied)

	// 结束议题：制造共识。
	voteOK(t, s, "v", Card(2), 12)
	out := revealOK(t, s, 13)
	if out.Kind != ResultConsensus {
		t.Fatalf("应为共识: %+v", out)
	}
	// 权限不足优先于状态不允许：议题已结束，观察者投票报权限而非状态。
	_, err = s.Vote("o", Card(2), 14)
	expectErr(t, "观察者+议题结束", err, ErrPermissionDenied)
	// 状态不允许优先于无人投票：议题已结束，主持人揭示报状态而非无人投票。
	_, err = s.Reveal("host", 15)
	expectErr(t, "议题结束后 Reveal", err, ErrInvalidState)

	// 无人投票：投票阶段零票时 Reveal。
	startOK(t, s, 100)
	_, err = s.Reveal("host", 101)
	expectErr(t, "零票 Reveal", err, ErrNoVotes)
}

// 被拒绝的操作不改变任何状态与时钟。
func TestRejectedOpKeepsStateAndClock(t *testing.T) {
	s := newTestSession(t, 3, 1000, false)
	joinOK(t, s, "a", RoleVoter, 0)
	startOK(t, s, 10) // lastNow = 10

	// 非法牌被拒：时钟不推进、状态不变。
	_, err := s.Vote("a", Card(4), 500)
	expectErr(t, "非法牌", err, ErrInvalidParam)
	if got := s.Status().LastNow; got != 10 {
		t.Fatalf("被拒操作不应推进时钟: lastNow=%d", got)
	}
	// 回退被拒：时钟不推进。
	_, err = s.Vote("a", Card(1), 9)
	expectErr(t, "时钟回退", err, ErrClockRegression)
	if got := s.Status().LastNow; got != 10 {
		t.Fatalf("被拒操作不应推进时钟: lastNow=%d", got)
	}
	// 介于两者之间的 now 仍被接受，证明时钟未被拒操作污染。
	voteOK(t, s, "a", Card(1), 12)
	if got := s.Status().LastNow; got != 12 {
		t.Fatalf("接受后时钟应推进: lastNow=%d", got)
	}
}

// 通过参数与时钟检查的操作，即使随后被拒，也先完成到期处理。
func TestExpiryProcessedOnRejectedOp(t *testing.T) {
	s := newTestSession(t, 3, 50, false)
	joinOK(t, s, "a", RoleVoter, 0)
	joinOK(t, s, "o", RoleObserver, 1)
	startOK(t, s, 100) // 到期时刻 150
	voteOK(t, s, "a", Card(3), 101)

	// 观察者投票将被拒（权限不足），但到期处理须先完成。
	out, err := s.Vote("o", Card(1), 200)
	expectErr(t, "观察者投票", err, ErrPermissionDenied)
	if out == nil || out.Trigger != TriggerExpired || out.RevealedAt != 150 ||
		out.Kind != ResultConsensus || out.Value != 3 {
		t.Fatalf("被拒操作仍应先完成到期揭示: %+v", out)
	}
	// 被拒操作不推进时钟。
	if got := s.Status().LastNow; got != 101 {
		t.Fatalf("被拒操作不应推进时钟: lastNow=%d", got)
	}
	// 议题已因到期揭示而结束。
	if got := s.Status().Phase; got != PhaseIdle {
		t.Fatalf("到期共识后议题应结束: %v", got)
	}
}

// 改投与撤回。
func TestVoteChangeAndUnvote(t *testing.T) {
	s := newTestSession(t, 3, 1000, false)
	joinOK(t, s, "a", RoleVoter, 0)
	startOK(t, s, 10)
	voteOK(t, s, "a", Card(1), 11)
	voteOK(t, s, "a", Card(2), 12) // 改投
	view, _, err := s.Peek("a", 13)
	if err != nil || !view.HasVote || view.Self != Card(2) || view.VotedCount != 1 {
		t.Fatalf("改投后本人视角应为新牌: view=%+v err=%v", view, err)
	}
	if _, err := s.Unvote("a", 14); err != nil {
		t.Fatalf("Unvote: %v", err)
	}
	view, _, err = s.Peek("a", 15)
	if err != nil || view.HasVote || view.VotedCount != 0 {
		t.Fatalf("撤回后本人视角应无票: view=%+v err=%v", view, err)
	}
	// 无票时 Unvote 为空操作。
	if _, err := s.Unvote("a", 16); err != nil {
		t.Fatalf("无票 Unvote 应为空操作: %v", err)
	}
	// 撤回后揭示报无人投票。
	_, err = s.Reveal("host", 17)
	expectErr(t, "撤回后 Reveal", err, ErrNoVotes)
}

// 离开的投票者其票被撤销且不再计入全体。
func TestLeaveRevokesVote(t *testing.T) {
	s := newTestSession(t, 3, 1000, false)
	joinOK(t, s, "a", RoleVoter, 0)
	joinOK(t, s, "b", RoleVoter, 1)
	startOK(t, s, 10)
	voteOK(t, s, "a", Card(5), 11)
	if _, err := s.Leave("a", 12); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	view, _, err := s.Peek("b", 13)
	if err != nil || view.VotedCount != 0 {
		t.Fatalf("离开者的票应被撤销: view=%+v err=%v", view, err)
	}
	_, err = s.Reveal("host", 14)
	expectErr(t, "全部离开后 Reveal", err, ErrNoVotes)
}

// 权限与成员规则。
func TestPermissionsAndMembership(t *testing.T) {
	s := newTestSession(t, 3, 1000, false)
	joinOK(t, s, "v", RoleVoter, 0)
	joinOK(t, s, "o", RoleObserver, 1)

	// 非主持人不能 Start/Reveal/Revote。
	_, err := s.Start("v", 2)
	expectErr(t, "成员 Start", err, ErrPermissionDenied)
	startOK(t, s, 10)
	_, err = s.Reveal("v", 11)
	expectErr(t, "成员 Reveal", err, ErrPermissionDenied)
	_, err = s.Revote("o", 11)
	expectErr(t, "成员 Revote", err, ErrPermissionDenied)

	// 观察者不能投票、不能撤回。
	_, err = s.Vote("o", Card(1), 12)
	expectErr(t, "观察者 Vote", err, ErrPermissionDenied)
	_, err = s.Unvote("o", 12)
	expectErr(t, "观察者 Unvote", err, ErrPermissionDenied)

	// 主持人未加入前不能投票（不在会话），加入后可投票。
	_, err = s.Vote("host", Card(1), 13)
	expectErr(t, "主持人未加入时 Vote", err, ErrNotInSession)
	joinOK(t, s, "host", RoleVoter, 14)
	voteOK(t, s, "host", Card(1), 15)

	// 已在会话内报已存在。
	_, err = s.Join("v", RoleObserver, 16)
	expectErr(t, "重复 Join", err, ErrAlreadyExists)
	voteOK(t, s, "v", Card(2), 16)

	// 非投票阶段不能 Revote；投票阶段不能 Start。
	_, err = s.Revote("host", 17)
	expectErr(t, "投票阶段 Revote", err, ErrInvalidState)
	_, err = s.Start("host", 18)
	expectErr(t, "投票阶段 Start", err, ErrInvalidState)

	// 主持人离开仅失去成员身份，仍保留主持人权限。
	if _, err := s.Leave("host", 19); err != nil {
		t.Fatalf("主持人 Leave: %v", err)
	}
	out := revealOK(t, s, 20)
	if out.Kind != ResultConsensus || out.Value != 2 {
		t.Fatalf("主持人离开后仍应可揭示: %+v", out)
	}
}

// 创建参数与操作参数的合法性校验。
func TestParamValidation(t *testing.T) {
	if _, err := NewSession("", testDeck, 3, 100, false); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("空主持人应报参数非法: %v", err)
	}
	if _, err := NewSession("h", []int{1}, 3, 100, false); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("牌组过小应报参数非法: %v", err)
	}
	if _, err := NewSession("h", []int{1, 2, 2}, 3, 100, false); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("牌组重复应报参数非法: %v", err)
	}
	if _, err := NewSession("h", []int{2, 1}, 3, 100, false); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("牌组非递增应报参数非法: %v", err)
	}
	if _, err := NewSession("h", []int{0, 1}, 3, 100, false); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("非正整数牌应报参数非法: %v", err)
	}
	if _, err := NewSession("h", testDeck, 0, 100, false); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("R 越界应报参数非法: %v", err)
	}
	if _, err := NewSession("h", testDeck, 6, 100, false); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("R 越界应报参数非法: %v", err)
	}
	if _, err := NewSession("h", testDeck, 3, 0, false); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("T 越界应报参数非法: %v", err)
	}
	if _, err := NewSession("h", testDeck, 3, 86401, false); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("T 越界应报参数非法: %v", err)
	}

	s := newTestSession(t, 3, 1000, false)
	if _, err := s.Join("u", Role(9), 0); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("非法角色应报参数非法: %v", err)
	}
	if _, err := s.Join("u", RoleVoter, -1); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("now 越界应报参数非法: %v", err)
	}
	if _, err := s.Join("u", RoleVoter, MaxNow+1); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("now 越界应报参数非法: %v", err)
	}
	if _, err := s.Vote("u", Card(0), 0); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("非法牌应报参数非法: %v", err)
	}
	if _, err := s.Vote("u", Card(-3), 0); err == nil || err.Code != ErrInvalidParam {
		t.Fatalf("非法牌应报参数非法: %v", err)
	}
}

// 大规模会话：20 万投票者的加入、投票与揭示在计数器设计下应迅速完成。
func TestLargeSession(t *testing.T) {
	const n = 200_000
	s := newTestSession(t, 3, MaxRoundTTL, false)
	for i := 0; i < n; i++ {
		if _, err := s.Join(fmt.Sprintf("u%d", i), RoleVoter, 0); err != nil {
			t.Fatalf("Join %d: %v", i, err)
		}
	}
	startOK(t, s, 0)
	for i := 0; i < n; i++ {
		if _, err := s.Vote(fmt.Sprintf("u%d", i), Card(3), 0); err != nil {
			t.Fatalf("Vote %d: %v", i, err)
		}
	}
	out := revealOK(t, s, 0)
	if out.Kind != ResultConsensus || out.Value != 3 || out.NumericVotes != n {
		t.Fatalf("大规模共识判定不符: %+v", out)
	}
}

// 并发冒烟：多 goroutine 并发调用，配合 -race 验证可串行化。
func TestConcurrentSmoke(t *testing.T) {
	s := newTestSession(t, 3, 5, true)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			user := fmt.Sprintf("g%d", g)
			var now int64
			for i := 0; i < 300; i++ {
				now++
				switch i % 7 {
				case 0:
					_, _ = s.Join(user, RoleVoter, now)
				case 1:
					_, _ = s.Vote(user, Card(1+int32(i)%6), now)
				case 2:
					_, _ = s.Unvote(user, now)
				case 3:
					_, _, _ = s.Peek(user, now)
				case 4:
					_, _ = s.SetRole(user, user, RoleObserver, now)
				case 5:
					_, _ = s.SetRole(user, user, RoleVoter, now)
				case 6:
					_, _ = s.Leave(user, now)
				}
			}
		}(g)
	}
	// 主持人操作并发进行。
	for i := int64(0); i < 300; i++ {
		_, _ = s.Start("host", i)
		_, _ = s.Reveal("host", i)
		_, _ = s.Revote("host", i)
	}
	wg.Wait()
}
