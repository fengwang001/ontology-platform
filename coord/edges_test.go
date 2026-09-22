package coord

import (
	"errors"
	"reflect"
	"testing"

	"ontology/participant"
	"ontology/vote"
)

func TestEmptyParticipantSetCommits(t *testing.T) {
	c, _ := newStarted(t)
	v, err := c.Drive()
	if err != nil || v.Decision != vote.Commit {
		t.Fatalf("empty set must decide COMMIT, got %v err=%v", v, err)
	}
	if st := c.Query(); st.Phase != PhaseDecided || len(st.Participants) != 0 {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestDuplicateRegisterAndUnknownID(t *testing.T) {
	c, _ := newStarted(t, "a")
	if err := c.Register("a"); !errors.Is(err, ErrDuplicateParticipant) {
		t.Fatalf("duplicate register: %v", err)
	}
	if err := c.CastVote("ghost", vote.Agree); !errors.Is(err, ErrUnknownParticipant) {
		t.Fatalf("vote from unknown id: %v", err)
	}
	if err := c.SendCommand("ghost", vote.Commit); !errors.Is(err, ErrUnknownParticipant) {
		t.Fatalf("command to unknown id: %v", err)
	}
}

func TestQueryZeroVerdictBeforeDecisionAndStableAfter(t *testing.T) {
	c, _ := newStarted(t, "a", "b")
	_ = c.CastVote("a", vote.Agree)
	st := c.Query()
	if st.Phase != PhaseVoting || st.Verdict != (vote.Verdict{}) {
		t.Fatalf("undecided query must carry zero verdict, got %+v", st)
	}
	_ = c.CastVote("b", vote.Agree)
	_, _ = c.Drive()
	first, second := c.Query(), c.Query()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("decided query must be stable: %+v vs %+v", first, second)
	}
	if first.Phase != PhaseDecided || first.Verdict.Decision != vote.Commit {
		t.Fatalf("want decided COMMIT, got %+v", first)
	}
}

func TestReplayBeforeDecisionFails(t *testing.T) {
	c, _ := newStarted(t, "a")
	if err := c.Replay(); !errors.Is(err, ErrNoDecision) {
		t.Fatalf("replay before decision: %v", err)
	}
}

func TestSendCommandDeliversAndIllegalTransitionSurfaces(t *testing.T) {
	c, _ := newStarted(t, "a")
	if err := c.SendCommand("a", vote.Commit); !errors.Is(err, participant.ErrNotPrepared) {
		t.Fatalf("commit before prepare must surface, got %v", err)
	}
	if st := c.Query(); st.Participants["a"] != participant.Pending {
		t.Fatal("failed command must not change state")
	}
	_ = c.CastVote("a", vote.Agree)
	if err := c.SendCommand("a", vote.Commit); err != nil {
		t.Fatal(err)
	}
	if err := c.SendCommand("a", vote.Commit); err != nil {
		t.Fatal("repeated commit command must be idempotent")
	}
}

func TestBeginAndVoteGuards(t *testing.T) {
	clock := newFakeClock()
	c := New(clock.now)
	_ = c.Register("a")
	if err := c.CastVote("a", vote.Agree); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("vote before begin: %v", err)
	}
	if _, err := c.Drive(); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("drive before begin: %v", err)
	}
	if err := c.Begin(0); err != nil {
		t.Fatal(err)
	}
	if err := c.Begin(0); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("double begin: %v", err)
	}
}
