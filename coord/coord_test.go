package coord

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"ontology/participant"
	"ontology/vote"
)

// fakeClock 是注入协调者的可控时钟。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newStarted(t *testing.T, ids ...string) (*Coordinator, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	c := New(clock.now)
	for _, id := range ids {
		if err := c.Register(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Begin(time.Minute); err != nil {
		t.Fatal(err)
	}
	return c, clock
}

func TestUnanimousCommit(t *testing.T) {
	c, _ := newStarted(t, "a", "b")
	_ = c.CastVote("a", vote.Agree)
	_ = c.CastVote("b", vote.Agree)
	v, err := c.Drive()
	if err != nil || v.Decision != vote.Commit {
		t.Fatalf("want COMMIT, got %v err=%v", v, err)
	}
	st := c.Query()
	for _, id := range []string{"a", "b"} {
		if st.Participants[id] != participant.Committed {
			t.Fatalf("%s must be Committed, got %v", id, st.Participants[id])
		}
	}
}

func TestOneRejectAbortsAllIncludingAgreed(t *testing.T) {
	c, _ := newStarted(t, "a", "b")
	_ = c.CastVote("a", vote.Agree)
	_ = c.CastVote("b", vote.Reject)
	v, err := c.Drive()
	if err != nil || v.Decision != vote.Abort || v.Cause != vote.Rejected || v.Culprit != "b" {
		t.Fatalf("want ABORT/rejected/b, got %v err=%v", v, err)
	}
	st := c.Query()
	if st.Participants["a"] != participant.Aborted {
		t.Fatalf("agreed participant must roll back to Aborted, got %v", st.Participants["a"])
	}
	if st.Participants["b"] != participant.Aborted {
		t.Fatalf("rejector must be Aborted, got %v", st.Participants["b"])
	}
}

func TestTimeoutAbortsWithDistinctCause(t *testing.T) {
	c, clock := newStarted(t, "a", "b")
	_ = c.CastVote("a", vote.Agree)
	clock.advance(2 * time.Minute)
	v, err := c.Drive()
	if err != nil || v.Decision != vote.Abort || v.Cause != vote.TimedOut || v.Culprit != "b" {
		t.Fatalf("want ABORT/timeout/b, got %v err=%v", v, err)
	}
}

func TestDeadlineIsLeftClosedRightOpen(t *testing.T) {
	c, clock := newStarted(t, "a")
	clock.advance(time.Minute - time.Nanosecond)
	if v, _ := c.Drive(); v.Decision != vote.None {
		t.Fatalf("before deadline must stay pending, got %v", v)
	}
	clock.advance(time.Nanosecond) // now == deadline
	v, _ := c.Drive()
	if v.Decision != vote.Abort || v.Cause != vote.TimedOut || v.Culprit != "a" {
		t.Fatalf("now == deadline must be a timeout, got %v", v)
	}
}

func TestDecisionWrittenOnceLateVotesIgnored(t *testing.T) {
	c, clock := newStarted(t, "a", "b")
	_ = c.CastVote("a", vote.Reject)
	v1, _ := c.Drive()
	// 迟到的同意票与重复驱动都不得改写决议。
	if err := c.CastVote("b", vote.Agree); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Hour)
	v2, _ := c.Drive()
	if v1 != v2 || v2.Decision != vote.Abort || v2.Cause != vote.Rejected || v2.Culprit != "a" {
		t.Fatalf("decision must be immutable: v1=%v v2=%v", v1, v2)
	}
	if st := c.Query(); st.Participants["b"] != participant.Aborted {
		t.Fatalf("late agree must not resurrect b, got %v", st.Participants["b"])
	}
}

func TestReplayAfterCrashKeepsStateAndCount(t *testing.T) {
	c, _ := newStarted(t, "a", "b")
	_ = c.CastVote("a", vote.Agree)
	_ = c.CastVote("b", vote.Agree)
	_, _ = c.Drive()
	before := c.Query()
	for _, id := range []string{"a", "b"} {
		if before.CommitCounts[id] != 1 {
			t.Fatalf("%s must have committed exactly once, got %d", id, before.CommitCounts[id])
		}
	}
	if err := c.Replay(); err != nil {
		t.Fatal(err)
	}
	after := c.Query()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("replay must not change state: %+v vs %+v", before, after)
	}
	for _, id := range []string{"a", "b"} {
		if after.CommitCounts[id] != 1 {
			t.Fatalf("replay must not re-run commit for %s, got %d", id, after.CommitCounts[id])
		}
	}
}
