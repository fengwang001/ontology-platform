package planningpoker

import (
	"errors"
	"testing"
)

func TestNewSessionValidation(t *testing.T) {
	bad := []struct {
		name string
		cfg  Config
		host string
		now  int64
	}{
		{"too few cards", Config{Cards: []int{1}, RoundLimit: 2, RoundSeconds: 10}, "host", 0},
		{"too many cards", Config{Cards: makeInts(21), RoundLimit: 2, RoundSeconds: 10}, "host", 0},
		{"not sorted", Config{Cards: []int{2, 1}, RoundLimit: 2, RoundSeconds: 10}, "host", 0},
		{"duplicate", Config{Cards: []int{1, 1}, RoundLimit: 2, RoundSeconds: 10}, "host", 0},
		{"non positive", Config{Cards: []int{0, 1}, RoundLimit: 2, RoundSeconds: 10}, "host", 0},
		{"round zero", Config{Cards: []int{1, 2}, RoundLimit: 0, RoundSeconds: 10}, "host", 0},
		{"round six", Config{Cards: []int{1, 2}, RoundLimit: 6, RoundSeconds: 10}, "host", 0},
		{"secs zero", Config{Cards: []int{1, 2}, RoundLimit: 2, RoundSeconds: 0}, "host", 0},
		{"secs too big", Config{Cards: []int{1, 2}, RoundLimit: 2, RoundSeconds: 86401}, "host", 0},
		{"now negative", Config{Cards: []int{1, 2}, RoundLimit: 2, RoundSeconds: 10}, "host", -1},
		{"now too big", Config{Cards: []int{1, 2}, RoundLimit: 2, RoundSeconds: 10}, "host", 1_000_000_000_001},
		{"empty host", Config{Cards: []int{1, 2}, RoundLimit: 2, RoundSeconds: 10}, "", 0},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSession(tc.host, tc.cfg, tc.now); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("%s: want ErrInvalidArgument, got %v", tc.name, err)
			}
		})
	}
}

func makeInts(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

func TestClockRewindAndRejectionPriority(t *testing.T) {
	s := testSession(t, false, 2, 100, 10)
	if _, err := s.Join("a", RoleVoter, 5); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("rewind: %v", err)
	}
	if _, err := s.Join("a", RoleVoter, 10); err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, err := s.Join("a", Role(99), 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid before rewind: %v", err)
	}
	if _, err := s.Vote("ghost", numCard(1), 9); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("rewind before membership: %v", err)
	}
	mustStart(t, s, 11)
	if _, err := s.Reveal("host", 10); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("rewind before no-votes: %v", err)
	}
	if _, err := s.Vote("ghost", numCard(1), 11); !errors.Is(err, ErrNotInSession) {
		t.Fatalf("not-in-session: %v", err)
	}
	mustJoin(t, s, "o", RoleObserver, 12)
	if _, err := s.Vote("o", numCard(1), 13); !errors.Is(err, ErrForbidden) {
		t.Fatalf("observer vote: %v", err)
	}
	if err := s.Start("intruder", 14); !errors.Is(err, ErrNotInSession) {
		t.Fatalf("non-host outsider start: %v", err)
	}
	mustJoin(t, s, "member2", RoleVoter, 14)
	if err := s.Start("member2", 15); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-host member start: %v", err)
	}
	if _, err := s.Reveal("host", 16); !errors.Is(err, ErrNoVotes) {
		t.Fatalf("no votes last: %v", err)
	}
	// 被拒绝操作不改变时钟：5/9/10 等都不应更新 lastNow。
	if _, err := s.Join("z", RoleVoter, 16); err != nil {
		t.Fatalf("clock after rejects: %v", err)
	}
}

func TestPeekNoLeakBeforeReveal(t *testing.T) {
	s := testSession(t, false, 3, 100, 0)
	mustJoin(t, s, "a", RoleVoter, 0)
	mustJoin(t, s, "b", RoleVoter, 0)
	mustJoin(t, s, "obs", RoleObserver, 0)
	mustStart(t, s, 0)
	mustVote(t, s, "a", numCard(5), 1)
	vb := mustPeek(t, s, "b", 2)
	if vb.OwnVote != nil || vb.VotedCount != 1 || vb.VoterTotal != 2 || vb.Outcome != nil {
		t.Fatalf("b leak: %+v", vb)
	}
	vo := mustPeek(t, s, "obs", 3)
	if vo.OwnVote != nil || vo.VotedCount != 1 {
		t.Fatalf("observer view: %+v", vo)
	}
	va := mustPeek(t, s, "a", 4)
	if va.OwnVote == nil || va.OwnVote.Value != 5 {
		t.Fatalf("own vote hidden from self: %+v", va)
	}
}

func TestRevealCategories(t *testing.T) {
	r := revealedOutcome(t, []Card{numCard(3), numCard(3)}, 2)
	if r.Category != ResultConsensus || r.Value != 3 || !r.Final || r.NumericVotes != 2 {
		t.Fatalf("consensus: %+v", r)
	}

	r = revealedOutcome(t, []Card{numCard(2), numCard(3)}, 2)
	if r.Category != ResultConverged || r.Value != 3 || !r.Final {
		t.Fatalf("converged: %+v", r)
	}

	// 2 与 5 数值不相邻，且牌组中隔了 3：分歧。
	r = revealedOutcome(t, []Card{numCard(2), numCard(5)}, 2)
	if r.Category != ResultDiverged || r.Final {
		t.Fatalf("diverged: %+v", r)
	}
}

func revealedOutcome(t *testing.T, votes []Card, rounds int) *RevealResult {
	t.Helper()
	s := testSession(t, false, rounds, 100, 0)
	for i := range votes {
		mustJoin(t, s, userName(i), RoleVoter, 0)
	}
	mustStart(t, s, 0)
	for i, c := range votes {
		mustVote(t, s, userName(i), c, 0)
	}
	return mustReveal(t, s, "host", 1)
}

func userName(i int) string { return "u" + itoaTest(i) }

func itoaTest(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
