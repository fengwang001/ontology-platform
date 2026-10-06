package planningpoker

import "testing"

func numCard(v int) Card { return Card{Value: v} }

func testSession(t *testing.T, auto bool, r, secs int, now int64) *Session {
	t.Helper()
	s, err := NewSession("host", Config{
		Cards: []int{1, 2, 3, 5, 8, 13}, RoundLimit: r, RoundSeconds: secs, AutoReveal: auto,
	}, now)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return s
}

func mustJoin(t *testing.T, s *Session, u string, role Role, now int64) *RevealResult {
	t.Helper()
	r, err := s.Join(u, role, now)
	if err != nil {
		t.Fatalf("Join(%s): %v", u, err)
	}
	return r
}

func mustStart(t *testing.T, s *Session, now int64) {
	t.Helper()
	if err := s.Start("host", now); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func mustVote(t *testing.T, s *Session, u string, c Card, now int64) *RevealResult {
	t.Helper()
	r, err := s.Vote(u, c, now)
	if err != nil {
		t.Fatalf("Vote(%s): %v", u, err)
	}
	return r
}

func mustReveal(t *testing.T, s *Session, u string, now int64) *RevealResult {
	t.Helper()
	r, err := s.Reveal(u, now)
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	return r
}

func mustPeek(t *testing.T, s *Session, u string, now int64) *PeekView {
	t.Helper()
	v, err := s.Peek(u, now)
	if err != nil {
		t.Fatalf("Peek(%s): %v", u, err)
	}
	return v
}

func distCount(r *RevealResult, c Card) int {
	for _, cc := range r.Distribution {
		if cc.Card.Special == c.Special && cc.Card.Value == c.Value {
			return cc.Count
		}
	}
	return -1
}
