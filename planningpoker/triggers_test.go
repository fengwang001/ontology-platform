package planningpoker

import (
	"errors"
	"testing"
)

func TestAutoRevealTriggers(t *testing.T) {
	// 最后一张投票触发。
	s := testSession(t, true, 2, 1000, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustJoin(t, s, "b", RoleVoter, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", numCard(1), 1)
	r := mustVote(t, s, "b", numCard(13), 2)
	if r == nil || r.Category != ResultDiverged {
		t.Fatalf("auto reveal on last vote: %+v", r)
	}

	// 未投票投票者离开触发。
	s = testSession(t, true, 2, 1000, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustJoin(t, s, "b", RoleVoter, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", numCard(5), 1)
	r, err := s.Leave("b", 2)
	if err != nil {
		t.Fatalf("leave: %v", err)
	}
	if r == nil || r.Category != ResultConsensus || r.Value != 5 {
		t.Fatalf("auto reveal on leave: %+v", r)
	}

	// 未投票投票者被改为观察者触发。
	s = testSession(t, true, 2, 1000, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustJoin(t, s, "b", RoleVoter, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", CardUncertain, 1)
	r, err = s.SetRole("b", RoleObserver, 2)
	if err != nil {
		t.Fatalf("setrole: %v", err)
	}
	if r == nil || r.Category != ResultNoValidVotes {
		t.Fatalf("auto reveal on demote: %+v", r)
	}

	// 已投票者离开后房间已无投票者 => 不满足“在室投票者 >= 1”，不触发。
	s = testSession(t, true, 2, 1000, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustJoin(t, s, "b", RoleVoter, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", numCard(2), 1)
	mustVote(t, s, "b", numCard(5), 2)
	r, err = s.Leave("b", 3) // b 的票撤销，a 仍在且已投，但此时……
	if err != nil || r != nil {
		t.Fatalf("expect no reveal: r=%+v err=%v", r, err)
	}
	// 真正触发场景：三名投票者，未投者离开，剩余两人全投。
	s = testSession(t, true, 2, 1000, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustJoin(t, s, "b", RoleVoter, 0)
	mustJoin(t, s, "c", RoleVoter, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", numCard(2), 1)
	mustVote(t, s, "b", numCard(5), 2)
	r, err = s.Leave("c", 3)
	if err != nil || r == nil {
		t.Fatalf("leave non-voter should reveal: r=%+v err=%v", r, err)
	}

	// 观察者离开不触发（尚有未投投票者）。
	s = testSession(t, true, 2, 1000, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustJoin(t, s, "o", RoleObserver, 0)
	mustStart(t, s, 0)
	r, err = s.Leave("o", 1)
	if err != nil || r != nil {
		t.Fatalf("observer leave: r=%+v err=%v", r, err)
	}

	// 投票中途加入新投票者不应立即揭示（即使老成员全投）。
	s = testSession(t, true, 2, 1000, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", numCard(3), 1)
	r = mustJoin(t, s, "b", RoleVoter, 2)
	if r != nil {
		t.Fatalf("join mid-vote must not reveal: %+v", r)
	}
}

func TestRoundLimitAndNewTopicReset(t *testing.T) {
	s := testSession(t, false, 2, 1000, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustJoin(t, s, "b", RoleVoter, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", numCard(1), 0)
	mustVote(t, s, "b", numCard(13), 0)
	r := mustReveal(t, s, "host", 1)
	if r.Category != ResultDiverged {
		t.Fatalf("r1: %+v", r)
	}
	if err := s.Revote("host", 2); err != nil {
		t.Fatalf("revote: %v", err)
	}
	v := mustPeek(t, s, "a", 3)
	if v.Phase != PhaseVoting || v.Round != 2 || v.VotedCount != 0 || v.OwnVote != nil {
		t.Fatalf("round2 state: %+v", v)
	}
	mustVote(t, s, "a", numCard(5), 4)
	mustVote(t, s, "b", numCard(5), 4)
	r = mustReveal(t, s, "host", 5)
	if r.Category != ResultConsensus {
		t.Fatalf("r2 consensus: %+v", r)
	}

	// 新议题：轮数重置、成员保留、票清空、结果清空。
	if err := s.Start("host", 6); err != nil {
		t.Fatalf("restart: %v", err)
	}
	v = mustPeek(t, s, "a", 7)
	if v.Round != 1 || v.VotedCount != 0 || v.VoterTotal != 2 || v.Outcome != nil {
		t.Fatalf("new topic reset: %+v", v)
	}

	// 已用尽：R=1 分歧强制取值，Revote 与后续投票/揭示均拒绝。
	s = testSession(t, false, 1, 1000, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustJoin(t, s, "b", RoleVoter, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", numCard(1), 0)
	mustVote(t, s, "b", numCard(13), 0)
	r = mustReveal(t, s, "host", 1)
	if r.Category != ResultForcedValue || !r.Final {
		t.Fatalf("forced: %+v", r)
	}
	if err := s.Revote("host", 2); !errors.Is(err, ErrState) {
		t.Fatalf("exhausted revote: %v", err)
	}
}

func TestChangeVoteUnvoteAndRejects(t *testing.T) {
	s := testSession(t, false, 2, 100, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", numCard(1), 1)
	mustVote(t, s, "a", numCard(8), 2)
	v := mustPeek(t, s, "a", 3)
	if v.OwnVote == nil || v.OwnVote.Value != 8 || v.VotedCount != 1 {
		t.Fatalf("change vote: %+v", v)
	}
	if _, err := s.Unvote("a", 4); err != nil {
		t.Fatalf("unvote: %v", err)
	}
	v = mustPeek(t, s, "a", 5)
	if v.OwnVote != nil || v.VotedCount != 0 {
		t.Fatalf("after unvote: %+v", v)
	}
	if _, err := s.Reveal("host", 6); !errors.Is(err, ErrNoVotes) {
		t.Fatalf("reveal empty: %v", err)
	}
	// 再次 Join 同一成员报已存在。
	if _, err := s.Join("a", RoleVoter, 7); err != ErrAlreadyExists {
		t.Fatalf("duplicate join: %v", err)
	}
}
