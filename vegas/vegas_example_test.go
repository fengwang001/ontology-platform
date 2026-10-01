package vegas

import "testing"

func exampleConfig() Config {
	return Config{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cd: 10, Wm: 100}
}

func mustAcquire(t *testing.T, l *Limiter, now int64, wantSeq int64) Token {
	t.Helper()
	tok, err := l.Acquire(now)
	if err != nil {
		t.Fatalf("t=%d Acquire: %v", now, err)
	}
	if tok.Seq != wantSeq {
		t.Fatalf("t=%d Acquire: got seq %d want %d", now, tok.Seq, wantSeq)
	}
	return tok
}

func mustErr(t *testing.T, got, want error) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestSpecWalkthrough replays the exact sequence described in the problem.
func TestSpecWalkthrough(t *testing.T) {
	l, err := New(exampleConfig())
	if err != nil {
		t.Fatal(err)
	}
	var toks [11]Token
	for i := int64(1); i <= 10; i++ {
		toks[i] = mustAcquire(t, l, 0, i)
		if toks[i].w != i {
			t.Fatalf("token %d w = %d", i, toks[i].w)
		}
	}
	if _, err := l.Acquire(0); err != ErrAtLimit {
		t.Fatalf("11th Acquire: %v", err)
	}

	if err := l.Release(toks[10], ResultSuccess, 100, 10); err != nil {
		t.Fatal(err)
	}
	if got := l.State(); got.L != 11 || got.N != 9 || got.WindowMinRTT != 100 {
		t.Fatalf("t=10: %+v", got)
	}

	if err := l.Release(toks[9], ResultSuccess, 150, 20); err != nil {
		t.Fatal(err)
	}
	if got := l.State().L; got != 11 {
		t.Fatalf("t=20 L=%d", got)
	}

	if err := l.Release(toks[8], ResultSuccess, 300, 30); err != nil {
		t.Fatal(err)
	}
	if got := l.State().L; got != 10 {
		t.Fatalf("t=30 L=%d", got)
	}

	if err := l.Release(toks[7], ResultDrop, 0, 35); err != nil {
		t.Fatal(err)
	}
	s := l.State()
	if s.L != 9 || !s.HasLastCut || s.LastCut != 35 {
		t.Fatalf("t=35: %+v", s)
	}

	if err := l.Release(toks[6], ResultDrop, 0, 40); err != nil {
		t.Fatal(err)
	}
	if got := l.State().L; got != 9 {
		t.Fatalf("t=40 cooldown L=%d", got)
	}

	if err := l.Release(toks[5], ResultDrop, 0, 45); err != nil {
		t.Fatal(err)
	}
	s = l.State()
	if s.L != 8 || s.N != 4 || s.LastCut != 45 {
		t.Fatalf("t=45: %+v", s)
	}

	t11 := mustAcquire(t, l, 50, 11)
	if t11.w != 1 {
		t.Fatalf("token 11 w=%d", t11.w)
	}
	if got := l.State(); got.L != 8 || got.N != 1 {
		t.Fatalf("t=50 after acquire: %+v", got)
	}
	if err := l.Release(toks[3], ResultSuccess, 100, 50); err != ErrTokenTimedOut {
		t.Fatalf("reaped token release: %v", err)
	}

	t12 := mustAcquire(t, l, 95, 12)
	if t12.w != 2 {
		t.Fatalf("token 12 w=%d", t12.w)
	}

	if err := l.Release(t12, ResultSuccess, 200, 115); err != nil {
		t.Fatal(err)
	}
	s = l.State()
	if s.L != 7 || s.N != 0 || s.WindowMinRTT != 150 || s.WindowSize != 3 || s.LastCut != 115 {
		t.Fatalf("t=115: %+v", s)
	}
}
