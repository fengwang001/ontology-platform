package activity

import (
	"errors"
	"testing"
)

func exampleCfg(sc int64) Config {
	return Config{S2S: 0, S2C: 10, HB: 4, SC: sc, M: 3, D0: 5, Cap: 20}
}

func TestWorkedExample(t *testing.T) {
	e := NewDiscard()
	id := []byte("a")
	must(t, e.Schedule(id, exampleCfg(100), 0))
	k, p, err := e.Start(id, 0)
	must(t, err)
	if k != 1 || p != 0 {
		t.Fatalf("start = (%d,%d)", k, p)
	}
	must(t, e.Heartbeat(id, 1, 3, 7))

	st, err := e.Status(id, 8)
	must(t, err)
	if st.State != Waiting || st.Attempt != 2 {
		t.Fatalf("status@8 = %+v, want Waiting k=2", st)
	}
	k, p, err = e.Start(id, 12)
	must(t, err)
	if k != 2 || p != 7 {
		t.Fatalf("start@12 = (%d,%d), want (2,7)", k, p)
	}
	st, _ = e.Status(id, 16)
	if st.State != Waiting || st.Attempt != 3 {
		t.Fatalf("status@16 = %+v, want Waiting k=3", st)
	}
	k, _, err = e.Start(id, 26)
	must(t, err)
	if k != 3 {
		t.Fatalf("start@26 k=%d", k)
	}
	st, _ = e.Status(id, 30)
	if st.State != StateTerminal || st.Terminal != TermFailed ||
		st.Reason != HB || st.At != 30 {
		t.Fatalf("status@30 = %+v, want Failed(hb)@30", st)
	}
}

func TestWorkedExampleSCVariants(t *testing.T) {
	// sc=26：第二次失败于 16，g'=26 ≥ 26，终败于 16。
	e := NewDiscard()
	id := []byte("b")
	must(t, e.Schedule(id, exampleCfg(26), 0))
	e.Start(id, 0)
	e.Heartbeat(id, 1, 3, 7)
	e.Start(id, 12)
	st, _ := e.Status(id, 16)
	if st.Terminal != TermFailed || st.Reason != HB || st.At != 16 {
		t.Fatalf("sc=26: %+v", st)
	}

	// sc=27：26 排队后无人领取，27 终局 TimedOutSC。
	e2 := NewDiscard()
	id2 := []byte("c")
	must(t, e2.Schedule(id2, exampleCfg(27), 0))
	e2.Start(id2, 0)
	e2.Heartbeat(id2, 1, 3, 7)
	e2.Start(id2, 12)
	st2, _ := e2.Status(id2, 27)
	if st2.Terminal != TermTimedOutSC || st2.At != 27 {
		t.Fatalf("sc=27: %+v", st2)
	}
}

func TestStaleTerminalState(t *testing.T) {
	e := NewDiscard()
	id := []byte("d")
	must(t, e.Schedule(id, exampleCfg(100), 0))
	e.Start(id, 0)
	e.Heartbeat(id, 1, 3, 7)

	// 7 恰为 hb 到期：迟到的 Complete(1,7) → ErrStale（当前尝试号已 2）。
	if err := e.Complete(id, 1, 7); !errors.Is(err, ErrStale) {
		t.Fatalf("Complete(1,7) = %v, want ErrStale", err)
	}
	// Start 在 Waiting 期间 → ErrState。
	if _, _, err := e.Start(id, 10); !errors.Is(err, ErrState) {
		t.Fatalf("Start@10 = %v, want ErrState", err)
	}
	e.Start(id, 12)
	e.Start(id, 26)
	// 30 时刻 Complete(3,30)，k 相同但已终局 → ErrTerminal。
	if err := e.Complete(id, 3, 30); !errors.Is(err, ErrTerminal) {
		t.Fatalf("Complete(3,30) = %v, want ErrTerminal", err)
	}
}

func TestExactAndNearDeadlines(t *testing.T) {
	// s2s=5：恰等 5 终局 TimedOutS2S，差 1 仍 Scheduled，且不重试。
	e := NewDiscard()
	id := []byte("q")
	must(t, e.Schedule(id, Config{S2S: 5, S2C: 100, SC: 100, M: 2, D0: 1, Cap: 1}, 0))
	if st, _ := e.Status(id, 4); st.State != Scheduled {
		t.Fatalf("s2s@4 = %+v", st)
	}
	if st, _ := e.Status(id, 5); st.Terminal != TermTimedOutS2S || st.Attempt != 1 || st.At != 5 {
		t.Fatalf("s2s@5 = %+v", st)
	}

	// 同刻 s2c 与 hb：s2c 优先。
	e2 := NewDiscard()
	id2 := []byte("q2")
	must(t, e2.Schedule(id2, Config{S2C: 4, HB: 4, SC: 100, M: 1, D0: 1, Cap: 1}, 0))
	e2.Start(id2, 0)
	st2, _ := e2.Status(id2, 4)
	if st2.Terminal != TermFailed || st2.Reason != S2C || st2.At != 4 {
		t.Fatalf("tie s2c/hb: %+v", st2)
	}

	// hb 恰等当刻的 Heartbeat 被到期超越（重试后尝试号变 2 → ErrStale）。
	e3 := NewDiscard()
	id3 := []byte("q3")
	must(t, e3.Schedule(id3, Config{S2C: 10, HB: 4, SC: 100, M: 3, D0: 1, Cap: 1}, 0))
	e3.Start(id3, 0)
	if err := e3.Heartbeat(id3, 1, 4, 1); !errors.Is(err, ErrStale) {
		t.Fatalf("Heartbeat@4 = %v, want ErrStale", err)
	}
	// hb=0 时 Heartbeat 仍合法。
	e4 := NewDiscard()
	id4 := []byte("q4")
	must(t, e4.Schedule(id4, Config{S2C: 100, HB: 0, SC: 0, M: 2, D0: 1, Cap: 1}, 0))
	e4.Start(id4, 0)
	must(t, e4.Heartbeat(id4, 1, 3, 9))
	st4, _ := e4.Status(id4, 50)
	if st4.State != Running || st4.Attempt != 1 {
		t.Fatalf("hb=0 must not expire: %+v", st4)
	}
}
