package coord

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"ontology/participant"
	"ontology/vote"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// clock 返回一个可手动推进的注入时钟。
func clock(at *time.Time) func() time.Time {
	return func() time.Time { return *at }
}

func newWith(t *testing.T, now *time.Time, ids ...string) (*Coordinator, map[string]*participant.Participant) {
	t.Helper()
	c := New(clock(now), base.Add(time.Minute))
	parts := make(map[string]*participant.Participant, len(ids))
	for _, id := range ids {
		p := participant.New(id)
		if err := c.Register(p); err != nil {
			t.Fatalf("Register(%s): %v", id, err)
		}
		parts[id] = p
	}
	return c, parts
}

func TestUnanimousCommit(t *testing.T) {
	now := base
	c, parts := newWith(t, &now, "a", "b", "c")
	for _, id := range []string{"a", "b", "c"} {
		if err := c.Vote(id, true); err != nil {
			t.Fatalf("Vote(%s): %v", id, err)
		}
	}
	o := c.Drive()
	if o.Decision != vote.Commit || o.Reason != vote.ReasonNone {
		t.Fatalf("got %+v, want Commit/None", o)
	}
	for id, p := range parts {
		if p.State() != participant.Committed || p.CommitCount() != 1 {
			t.Fatalf("%s: state=%v count=%d, want Committed/1",
				id, p.State(), p.CommitCount())
		}
	}
}

func TestOneNoAbortsEveryoneIncludingVotedYes(t *testing.T) {
	now := base
	c, parts := newWith(t, &now, "a", "b", "c")
	_ = c.Vote("a", true)
	_ = c.Vote("b", false)
	_ = c.Vote("c", true)
	o := c.Drive()
	if o.Decision != vote.Abort || o.Reason != vote.ReasonRejected || o.Culprit != "b" {
		t.Fatalf("got %+v, want Abort/Rejected/b", o)
	}
	for id, p := range parts {
		if p.State() != participant.Aborted {
			t.Fatalf("%s: state=%v, want Aborted（已同意者也必须回滚）", id, p.State())
		}
	}
}

func TestTimeoutDistinctFromRejectedAndNamesCulprit(t *testing.T) {
	now := base
	c, _ := newWith(t, &now, "a", "b")
	_ = c.Vote("a", true)
	now = base.Add(time.Minute) // 恰好等于截止时刻
	o := c.Drive()
	if o.Decision != vote.Abort || o.Reason != vote.ReasonTimeout || o.Culprit != "b" {
		t.Fatalf("got %+v, want Abort/Timeout/b", o)
	}
	if o.Reason == vote.ReasonRejected {
		t.Fatal("超时原因必须与否决可区分")
	}
}

func TestDecisionWrittenOnceLateVotesAndRedrive(t *testing.T) {
	now := base
	c, _ := newWith(t, &now, "a", "b")
	_ = c.Vote("a", false)
	first := c.Drive()
	// 迟到的同意票不改写决议。
	if err := c.Vote("b", true); err != nil {
		t.Fatalf("late Vote: %v", err)
	}
	if got := c.Drive(); got != first {
		t.Fatalf("decision rewritten: first %+v, then %+v", first, got)
	}
	if got := c.Decide(); got != first {
		t.Fatalf("Decide after Drive: got %+v, want %+v", got, first)
	}
}

func TestEmptyParticipantSetCommits(t *testing.T) {
	now := base
	c, _ := newWith(t, &now)
	o := c.Drive()
	if o.Decision != vote.Commit || o.Reason != vote.ReasonNone || o.Culprit != "" {
		t.Fatalf("got %+v, want Commit/None/empty（空集合 vacuous truth）", o)
	}
}

func TestDuplicateRegisterAndUnknownID(t *testing.T) {
	now := base
	c, _ := newWith(t, &now, "a")
	if err := c.Register(participant.New("a")); !errors.Is(err, ErrDuplicateParticipant) {
		t.Fatalf("got %v, want ErrDuplicateParticipant", err)
	}
	if err := c.Vote("ghost", true); !errors.Is(err, ErrUnknownParticipant) {
		t.Fatalf("got %v, want ErrUnknownParticipant", err)
	}
}

func TestQueryZeroValueBeforeDecisionAndStableAfter(t *testing.T) {
	now := base
	c, _ := newWith(t, &now, "a", "b")
	_ = c.Vote("a", true)
	s := c.Query()
	if s.Decided || s.Phase != PhaseVoting || s.Outcome != (vote.Outcome{}) {
		t.Fatalf("未裁决查询必须为零值，got %+v", s)
	}
	_ = c.Vote("b", true)
	c.Drive()
	q1, q2 := c.Query(), c.Query()
	if !reflect.DeepEqual(q1, q2) {
		t.Fatalf("已裁决查询不稳定：%+v vs %+v", q1, q2)
	}
	if !q1.Decided || q1.Phase != PhaseResolved || q1.Outcome.Decision != vote.Commit {
		t.Fatalf("got %+v, want resolved Commit", q1)
	}
	want := []MemberStatus{
		{ID: "a", State: participant.Committed},
		{ID: "b", State: participant.Committed},
	}
	if !reflect.DeepEqual(q1.Members, want) {
		t.Fatalf("members = %+v, want %+v", q1.Members, want)
	}
}

func TestReplayAfterCrashKeepsStateAndCounts(t *testing.T) {
	now := base
	c, parts := newWith(t, &now, "a", "b")
	_ = c.Vote("a", true)
	_ = c.Vote("b", true)
	c.Drive()
	before := c.Query()
	if err := c.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if err := c.Replay(); err != nil {
		t.Fatalf("Replay again: %v", err)
	}
	after := c.Query()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("重放后状态改变：%+v vs %+v", before, after)
	}
	for id, p := range parts {
		if p.CommitCount() != 1 {
			t.Fatalf("%s: CommitCount=%d, want 1（重放不得增加计数）", id, p.CommitCount())
		}
	}
}

func TestReplayBeforeDecisionFails(t *testing.T) {
	now := base
	c, _ := newWith(t, &now, "a")
	if err := c.Replay(); !errors.Is(err, ErrNotDecided) {
		t.Fatalf("got %v, want ErrNotDecided", err)
	}
}

func TestReplayAbortedTransaction(t *testing.T) {
	now := base
	c, parts := newWith(t, &now, "a", "b")
	_ = c.Vote("a", true)
	_ = c.Vote("b", false)
	c.Drive()
	if err := c.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	for id, p := range parts {
		if p.State() != participant.Aborted {
			t.Fatalf("%s: state=%v, want Aborted", id, p.State())
		}
	}
}
