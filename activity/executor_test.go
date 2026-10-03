package activity

import (
	"errors"
	"log"
	"os"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func startMust(t *testing.T, e *Executor, id []byte, now int64) (int, int64) {
	t.Helper()
	k, p, err := e.Start(id, now)
	must(t, err)
	return k, p
}

func newEx() *Executor {
	e := NewExecutor()
	e.SetLogger(log.New(os.Stdout, "", log.LstdFlags))
	return e
}

// 题目示例：hb 连续失败两次后第 3 次失败 k=M 终局 Failed(hb) 于 30。
func TestExampleHBChain(t *testing.T) {
	e := newEx()
	id := []byte("a")
	must(t, e.Schedule(id, 0, 10, 4, 100, 3, 5, 20, 0))
	k, p := startMust(t, e, id, 0)
	if k != 1 || p != 0 {
		t.Fatalf("start got (%d,%d)", k, p)
	}
	must(t, e.Heartbeat(id, 1, 3, 7))
	st, err := e.Status(id, 8)
	must(t, err)
	if st.State != Waiting || st.Attempt != 2 {
		t.Fatalf("status@8=%+v", st)
	}
	k, p = startMust(t, e, id, 12)
	if k != 2 || p != 7 {
		t.Fatalf("start@12=(%d,%d)", k, p)
	}
	st, _ = e.Status(id, 26)
	if st.State != Scheduled || st.Attempt != 3 {
		t.Fatalf("status@26=%+v", st)
	}
	startMust(t, e, id, 26)
	st, _ = e.Status(id, 30)
	if !st.Terminal || st.Reason != ReasonHB || st.Time != 30 || st.Attempt != 3 {
		t.Fatalf("final=%+v", st)
	}
}

// sc=26：第 2 次失败于 16，g'=26 ≥ 26，终局 Failed(hb) 于 16。
func TestExampleSCEqualBackoff(t *testing.T) {
	e := newEx()
	id := []byte("b")
	must(t, e.Schedule(id, 0, 10, 4, 26, 3, 5, 20, 0))
	startMust(t, e, id, 0)
	must(t, e.Heartbeat(id, 1, 3, 7))
	startMust(t, e, id, 12)
	st, _ := e.Status(id, 100)
	if !st.Terminal || st.Reason != ReasonHB || st.Time != 16 {
		t.Fatalf("sc=26 final=%+v", st)
	}
}

// sc=27：26 重新排队无人领取，27 时刻 TimedOutSC。
func TestExampleSCWins(t *testing.T) {
	e := newEx()
	id := []byte("c")
	must(t, e.Schedule(id, 0, 10, 4, 27, 3, 5, 20, 0))
	startMust(t, e, id, 0)
	must(t, e.Heartbeat(id, 1, 3, 7))
	st, _ := e.Status(id, 12)
	if st.State != Scheduled || st.Attempt != 2 {
		t.Fatalf("@12=%+v", st)
	}
	st, _ = e.Status(id, 27)
	if !st.Terminal || st.Reason != ReasonSC || st.Time != 27 {
		t.Fatalf("sc=27 final=%+v", st)
	}
}

func TestExampleRejections(t *testing.T) {
	e := newEx()
	id := []byte("d")
	must(t, e.Schedule(id, 0, 10, 4, 100, 3, 5, 20, 0))
	startMust(t, e, id, 0)
	must(t, e.Heartbeat(id, 1, 3, 7))
	// Complete(1,7) 恰在 hb 到期当刻：旧尝试号 -> ErrStale。
	if err := e.Complete(id, 1, 7); !errors.Is(err, ErrStale) {
		t.Fatalf("complete stale=%v", err)
	}
	// Waiting 期间 Start -> ErrState（10<12 未到排队点）。
	if _, _, err := e.Start(id, 10); !errors.Is(err, ErrState) {
		t.Fatalf("start waiting=%v", err)
	}
	startMust(t, e, id, 12)
	startMust(t, e, id, 26)
	// 30 到期当刻 Complete(k=3) -> ErrTerminal。
	if err := e.Complete(id, 3, 30); !errors.Is(err, ErrTerminal) {
		t.Fatalf("complete terminal=%v", err)
	}
}
