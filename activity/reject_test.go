package activity

import (
	"errors"
	"testing"
)

func isErr(err, target error) bool { return errors.Is(err, target) }

func TestRejectionsAndMisc(t *testing.T) {
	e := NewExecutor()
	id := []byte("x")
	if err := e.Schedule(id, 0, 0, 0, 0, 3, 1, 10, 0); !isErr(err, ErrConfig) {
		t.Fatalf("cfg=%v", err)
	}
	if err := e.Schedule(id, 0, 10, 0, 100, 0, 1, 10, 0); !isErr(err, ErrConfig) {
		t.Fatalf("M=%v", err)
	}
	if err := e.Schedule(nil, 0, 10, 0, 100, 3, 1, 10, 0); !isErr(err, ErrArgument) {
		t.Fatalf("id=%v", err)
	}
	must(t, e.Schedule(id, 0, 10, 0, 100, 3, 1, 10, 0))
	if err := e.Schedule(id, 0, 10, 0, 100, 3, 1, 10, 0); !isErr(err, ErrExists) {
		t.Fatalf("dup=%v", err)
	}
	if _, _, err := e.Start([]byte("nope"), 0); !isErr(err, ErrNotFound) {
		t.Fatalf("missing=%v", err)
	}
	if _, _, err := e.Start(id, -1); !isErr(err, ErrArgument) {
		t.Fatalf("now=%v", err)
	}
	if err := e.Heartbeat(id, 0, 0, 1); !isErr(err, ErrArgument) {
		t.Fatalf("k=%v", err)
	}
	startMust(t, e, id, 0)
	// now 小于时钟：ErrClock（晚些时刻先推进时钟）。
	must(t, e.Heartbeat(id, 1, 5, 1))
	if _, _, err := e.Start(id, 4); !isErr(err, ErrClock) {
		t.Fatalf("clock=%v", err)
	}
	if _, _, err := e.Start(id, 6); !isErr(err, ErrState) {
		t.Fatalf("restart=%v", err)
	}
	// hb=0 时 Heartbeat 仍合法。
	must(t, e.Heartbeat(id, 1, 6, 42))
	if st, _ := e.Status(id, 7); st.State != Running {
		t.Fatalf("hb0=%+v", st)
	}
	// Waiting 期间 HB：ErrState（k 相同）。
	st, _ := e.Status(id, 17) // s2c=10（r=0）于 10 失败，11 重新排队
	if st.State != Scheduled || st.Attempt != 2 {
		t.Fatalf("@12=%+v", st)
	}
	if err := e.Heartbeat(id, 2, 12, 1); !isErr(err, ErrState) {
		t.Fatalf("hb scheduled=%v", err)
	}
}

func TestFailKindsAndComplete(t *testing.T) {
	e := NewExecutor()
	id := []byte("f")
	must(t, e.Schedule(id, 0, 100, 0, 1000, 1, 1, 10, 0))
	startMust(t, e, id, 0)
	// k=M=1 时可重试失败也直接终局 Failed(app)。
	must(t, e.Fail(id, 1, 5, true))
	st, _ := e.Status(id, 5)
	if !st.Terminal || st.Reason != ReasonApp || st.Time != 5 {
		t.Fatalf("fail=%+v", st)
	}
	if err := e.Complete(id, 1, 6); !isErr(err, ErrTerminal) {
		t.Fatalf("after term=%v", err)
	}

	e2 := NewExecutor()
	id2 := []byte("g")
	must(t, e2.Schedule(id2, 0, 100, 0, 1000, 3, 1, 10, 0))
	startMust(t, e2, id2, 0)
	must(t, e2.Fail(id2, 1, 4, false))
	if st, _ := e2.Status(id2, 4); !st.Terminal || st.Reason != ReasonApp {
		t.Fatalf("non-retryable=%+v", st)
	}

	e3 := NewExecutor()
	id3 := []byte("h")
	must(t, e3.Schedule(id3, 0, 100, 0, 1000, 3, 1, 10, 0))
	startMust(t, e3, id3, 0)
	must(t, e3.Complete(id3, 1, 7))
	st, _ = e3.Status(id3, 7)
	if !st.Terminal || st.Reason != ReasonCompleted || st.Time != 7 {
		t.Fatalf("complete=%+v", st)
	}
}
