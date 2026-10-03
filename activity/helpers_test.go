package activity

import "testing"

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBasicRejections(t *testing.T) {
	e := NewDiscard()
	cfg := Config{S2C: 10, SC: 100, M: 2, D0: 1, Cap: 1}
	if err := e.Schedule(nil, cfg, 0); err != ErrArgument {
		t.Fatalf("empty id = %v", err)
	}
	if err := e.Schedule([]byte("x"), Config{S2C: 0, SC: 0, M: 2, D0: 1, Cap: 1}, 0); err != ErrConfig {
		t.Fatalf("bad config = %v", err)
	}
	must(t, e.Schedule([]byte("x"), cfg, 5))
	if err := e.Schedule([]byte("x"), cfg, 5); err != ErrExists {
		t.Fatalf("dup schedule = %v", err)
	}
	// ErrClock 先于不存在/Stale。
	if err := e.Heartbeat([]byte("x"), 1, 4, 0); err != ErrClock {
		t.Fatalf("clock back = %v", err)
	}
	if err := e.Heartbeat([]byte("missing"), 1, 5, 0); err != ErrNotFound {
		t.Fatalf("missing = %v", err)
	}
	if err := e.Heartbeat([]byte("x"), 0, 6, 0); err != ErrArgument {
		t.Fatalf("bad k = %v", err)
	}
	// k 不匹配 → ErrStale；活动 Scheduled，Start 合法。
	if err := e.Heartbeat([]byte("x"), 2, 6, 0); err != ErrStale {
		t.Fatalf("stale hb = %v", err)
	}
	if _, _, err := e.Start([]byte("x"), 6); err != nil {
		t.Fatal(err)
	}
	// Running 时 Start → ErrState。
	if _, _, err := e.Start([]byte("x"), 6); err != ErrState {
		t.Fatalf("start while running = %v", err)
	}
	// 成功才推进时钟：被拒后 now=5 仍合法。
	if err := e.Heartbeat([]byte("x"), 1, 7, 0); err != nil {
		t.Fatal(err)
	}
}

func TestAppFailuresAndS2CRetry(t *testing.T) {
	e := NewDiscard()
	id := []byte("f")
	must(t, e.Schedule(id, Config{S2C: 10, SC: 100, M: 2, D0: 5, Cap: 20}, 0))
	e.Start(id, 0)
	// 不可重试 → Failed(app)。
	must(t, e.Fail(id, 1, 3, false))
	st, _ := e.Status(id, 3)
	if st.Terminal != TermFailed || st.Reason != App {
		t.Fatalf("non-retryable: %+v", st)
	}

	e2 := NewDiscard()
	id2 := []byte("f2")
	must(t, e2.Schedule(id2, Config{S2C: 3, SC: 100, M: 1, D0: 1, Cap: 1}, 0))
	e2.Start(id2, 0)
	// k=M 时 s2c 到期 → Failed(s2c)@3。
	st2, _ := e2.Status(id2, 3)
	if st2.Terminal != TermFailed || st2.Reason != S2C || st2.At != 3 {
		t.Fatalf("s2c k=M: %+v", st2)
	}
}

func TestCrossAttemptInOneAdvance(t *testing.T) {
	// hb=2, d0=1, cap=1：hb 失败后每毫秒重试一次，无人再 Start。
	// Status@10 一次推演跨越 1→...→sc 到期。
	e := NewDiscard()
	id := []byte("z")
	must(t, e.Schedule(id, Config{S2S: 0, S2C: 100, HB: 2, SC: 10, M: 20, D0: 1, Cap: 1}, 0))
	if _, _, err := e.Start(id, 0); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Status(id, 10)
	if st.Terminal != TermTimedOutSC || st.At != 10 {
		t.Fatalf("cross attempts: %+v", st)
	}
}

func TestS2SNoRetryAndCapBackoff(t *testing.T) {
	// s2s=3 第一次排队无人领取即终局，与 M 无关。
	e := NewDiscard()
	id := []byte("s")
	must(t, e.Schedule(id, Config{S2S: 3, S2C: 100, SC: 100, M: 5, D0: 1, Cap: 1}, 0))
	st, _ := e.Status(id, 3)
	if st.Terminal != TermTimedOutS2S || st.Attempt != 1 {
		t.Fatalf("s2s: %+v", st)
	}

	// d0=5 但 cap=7：退避序列 5,7,7。
	e2 := NewDiscard()
	id2 := []byte("s2")
	must(t, e2.Schedule(id2, Config{S2C: 10, HB: 1, SC: 100, M: 4, D0: 5, Cap: 7}, 0))
	e2.Start(id2, 0)
	e2.Status(id2, 1) // hb 失败@1，Waiting 到 6
	if _, _, err := e2.Start(id2, 6); err != nil {
		t.Fatalf("start k2@6: %v", err)
	}
	e2.Status(id2, 7) // hb@7，退避 min(10,7)=7，g'=14
	if _, _, err := e2.Start(id2, 14); err != nil {
		t.Fatalf("start k3@14 (cap): %v", err)
	}
	e2.Status(id2, 15) // hb@15，退避仍 7，g'=22
	if _, _, err := e2.Start(id2, 22); err != nil {
		t.Fatalf("start k4@22: %v", err)
	}
}
