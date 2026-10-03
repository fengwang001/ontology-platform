package activity

import "testing"

func TestS2STieAndNoRetry(t *testing.T) {
	e := NewExecutor()
	id := []byte("s2s")
	must(t, e.Schedule(id, 5, 10, 0, 100, 3, 1, 10, 0))
	st, _ := e.Status(id, 4)
	if st.State != Scheduled || st.Terminal {
		t.Fatalf("@4=%+v", st)
	}
	st, _ = e.Status(id, 5) // 恰等到期
	if !st.Terminal || st.Reason != ReasonS2S || st.Time != 5 {
		t.Fatalf("@5=%+v", st)
	}
}

func TestS2CTie(t *testing.T) {
	e := NewExecutor()
	id := []byte("s2c")
	must(t, e.Schedule(id, 0, 5, 0, 100, 3, 1, 10, 0))
	startMust(t, e, id, 0)
	st, _ := e.Status(id, 4)
	if st.State != Running {
		t.Fatalf("@4=%+v", st)
	}
	st, _ = e.Status(id, 5) // 恰等 s2c -> 尝试失败，进入 Waiting
	if st.State != Waiting || st.Attempt != 2 {
		t.Fatalf("@5=%+v", st)
	}
}

func TestHBTie(t *testing.T) {
	e := NewExecutor()
	id := []byte("hb")
	must(t, e.Schedule(id, 0, 100, 4, 1000, 3, 1, 10, 0))
	startMust(t, e, id, 0)
	must(t, e.Heartbeat(id, 1, 3, 9))
	st, _ := e.Status(id, 6)
	if st.State != Running {
		t.Fatalf("@6=%+v", st)
	}
	st, _ = e.Status(id, 7) // 3+4 恰等
	if st.State != Waiting || st.Attempt != 2 {
		t.Fatalf("@7=%+v", st)
	}
}

func TestSCTie(t *testing.T) {
	e := NewExecutor()
	id := []byte("sc")
	must(t, e.Schedule(id, 0, 100, 0, 5, 3, 1, 10, 0))
	startMust(t, e, id, 0)
	st, _ := e.Status(id, 4)
	if st.State != Running {
		t.Fatalf("@4=%+v", st)
	}
	st, _ = e.Status(id, 5)
	if !st.Terminal || st.Reason != ReasonSC || st.Time != 5 {
		t.Fatalf("@5=%+v", st)
	}
}

func TestTieOrder(t *testing.T) {
	cases := []struct {
		name             string
		s2s, s2c, hb, sc int64
		m                int
		start            bool
		hbAt             int64
		want             Reason
	}{
		{"sc<s2c", 0, 5, 0, 5, 1, true, -1, ReasonSC},
		{"s2c<hb", 0, 5, 5, 100, 1, true, -1, ReasonS2C},
		{"sc<s2s", 5, 100, 0, 5, 1, false, -1, ReasonSC},
		{"sc<hb", 0, 100, 5, 5, 1, true, -1, ReasonSC},
	}
	for _, c := range cases {
		e := NewExecutor()
		id := []byte(c.name)
		must(t, e.Schedule(id, c.s2s, c.s2c, c.hb, c.sc, c.m, 1, 10, 0))
		if c.start {
			startMust(t, e, id, 0)
		}
		st, _ := e.Status(id, 5)
		if !st.Terminal || st.Reason != c.want || st.Time != 5 {
			t.Fatalf("%s: %+v want %s", c.name, st, c.want)
		}
	}
}

func TestBackoffCap(t *testing.T) {
	// d0=5 cap=7：退避序列 5,7,7；s2c=3 驱动失败时刻 3/15/26/37。
	e := NewExecutor()
	id := []byte("cap")
	must(t, e.Schedule(id, 0, 3, 0, 1000, 4, 5, 7, 0))
	startMust(t, e, id, 0)
	st, _ := e.Status(id, 3)
	if st.State != Waiting || st.Attempt != 2 {
		t.Fatalf("f1=%+v", st)
	}
	startMust(t, e, id, 8) // g'=3+5
	st, _ = e.Status(id, 11)
	if st.State != Waiting || st.Attempt != 3 {
		t.Fatalf("f2=%+v", st)
	}
	startMust(t, e, id, 18) // 11+7
	st, _ = e.Status(id, 21)
	if st.State != Waiting || st.Attempt != 4 {
		t.Fatalf("f3=%+v", st)
	}
	startMust(t, e, id, 28) // 21+7
	st, _ = e.Status(id, 31)
	if !st.Terminal || st.Reason != ReasonS2C || st.Time != 31 || st.Attempt != 4 {
		t.Fatalf("f4=%+v", st)
	}
}

func TestCrossAttemptsOneCall(t *testing.T) {
	// s2c=2 于 2 失败 -> 退避 1 -> 3 重新排队；s2s=2 -> 5 时刻 s2s 终局。
	// 一次 Status(10) 跨越 wake(3) 与 s2s(5) 两个到期、两个尝试号。
	e := NewExecutor()
	id := []byte("cross")
	must(t, e.Schedule(id, 2, 2, 0, 100, 3, 1, 10, 0))
	startMust(t, e, id, 0)
	st, _ := e.Status(id, 10)
	if !st.Terminal || st.Reason != ReasonS2S || st.Time != 5 || st.Attempt != 2 {
		t.Fatalf("cross=%+v", st)
	}
}

func TestProgressAcrossAttempts(t *testing.T) {
	e := NewExecutor()
	id := []byte("p")
	must(t, e.Schedule(id, 0, 10, 4, 1000, 3, 1, 10, 0))
	_, p := startMust(t, e, id, 0)
	if p != 0 {
		t.Fatalf("initial p=%d", p)
	}
	must(t, e.Heartbeat(id, 1, 1, 55))
	st, _ := e.Status(id, 5) // hb 到期于 5
	if st.State != Waiting {
		t.Fatalf("%+v", st)
	}
	_, p = startMust(t, e, id, 6)
	if p != 55 {
		t.Fatalf("carried p=%d", p)
	}
}
