package planningpoker

import (
	"errors"
	"testing"
)

func TestSpecialCardsCountButExcluded(t *testing.T) {
	s := testSession(t, false, 2, 100, 0)
	for _, u := range []string{"a", "b", "c"} {
		mustJoin(t, s, u, RoleVoter, 0)
	}
	mustStart(t, s, 0)
	mustVote(t, s, "a", CardUncertain, 0)
	mustVote(t, s, "b", CardBreak, 0)
	mustVote(t, s, "c", numCard(5), 0)
	r := mustReveal(t, s, "host", 1)
	if r.Category != ResultConsensus || r.Value != 5 || r.NumericVotes != 1 {
		t.Fatalf("special+numeric: %+v", r)
	}
	if distCount(r, CardUncertain) != 1 || distCount(r, CardBreak) != 1 || distCount(r, numCard(5)) != 1 {
		t.Fatalf("distribution: %+v", r.Distribution)
	}

	// 只有特殊牌：有票可揭示，但无数值票 => 无有效票，可 Revote。
	s = testSession(t, false, 2, 100, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", CardUncertain, 0)
	r = mustReveal(t, s, "host", 1)
	if r.Category != ResultNoValidVotes || r.Final || r.NumericVotes != 0 {
		t.Fatalf("only special: %+v", r)
	}
	if err := s.Revote("host", 2); err != nil {
		t.Fatalf("revote: %v", err)
	}
	if _, err := s.Vote("a", Card{Value: 99}, 3); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unknown card: %v", err)
	}
	if _, err := s.Vote("a", Card{Value: -3, Special: true}, 3); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("fake special: %v", err)
	}
}

func TestForcedLowerMedian(t *testing.T) {
	r := revealedOutcome(t, []Card{numCard(1), numCard(2), numCard(5), numCard(8)}, 1)
	if r.Category != ResultForcedValue || r.Value != 2 || !r.Final {
		t.Fatalf("even lower median: %+v", r)
	}
	r = revealedOutcome(t, []Card{numCard(1), numCard(3), numCard(8)}, 1)
	if r.Category != ResultForcedValue || r.Value != 3 {
		t.Fatalf("odd lower median: %+v", r)
	}
	r = revealedOutcome(t, []Card{numCard(1), numCard(1), numCard(8), numCard(13)}, 1)
	if r.Category != ResultForcedValue || r.Value != 1 {
		t.Fatalf("duplicate lower median: %+v", r)
	}
}

func TestExpiryBoundary(t *testing.T) {
	s := testSession(t, false, 2, 10, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", numCard(3), 5)

	// 差一秒未到期。
	v := mustPeek(t, s, "a", 9)
	if v.Phase != PhaseVoting {
		t.Fatalf("one second before deadline should be voting: %+v", v)
	}
	// 恰等于到期时刻：到期时刻揭示。
	v = mustPeek(t, s, "a", 10)
	if v.Outcome == nil || v.Outcome.Category != ResultConsensus || v.Outcome.RevealedAt != 10 {
		t.Fatalf("at deadline: %+v", v.Outcome)
	}

	// 到期且无人投票（非末轮）=> 无有效票，可 Revote。
	s = testSession(t, false, 2, 10, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustStart(t, s, 0)
	v = mustPeek(t, s, "a", 10)
	if v.Outcome == nil || v.Outcome.Category != ResultNoValidVotes || v.Outcome.Final {
		t.Fatalf("expiry no votes: %+v", v.Outcome)
	}

	// 末轮到期无人投票 => 终局无结果。
	s = testSession(t, false, 1, 10, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustStart(t, s, 0)
	v = mustPeek(t, s, "a", 10)
	if v.Outcome == nil || v.Outcome.Category != ResultTerminalNoResult || !v.Outcome.Final {
		t.Fatalf("terminal no result: %+v", v.Outcome)
	}
	if err := s.Revote("host", 11); !errors.Is(err, ErrState) {
		t.Fatalf("revote after terminal: %v", err)
	}
}

func TestExpiryAppliedBeforeRejectedOp(t *testing.T) {
	s := testSession(t, false, 1, 10, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustJoin(t, s, "o", RoleObserver, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", numCard(3), 1)
	// 观察者投票会被权限拒绝，但通过参数/时钟检查后须先做到期处理。
	if _, err := s.Vote("o", numCard(1), 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden: %v", err)
	}
	v := mustPeek(t, s, "a", 10)
	if v.Outcome == nil || v.Outcome.Category != ResultConsensus {
		t.Fatalf("expiry must precede rejected op: %+v", v.Outcome)
	}
}
