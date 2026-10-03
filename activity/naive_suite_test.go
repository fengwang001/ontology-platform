package activity

import (
	"math/rand"
	"testing"
)

func runScenario(t *testing.T, cfg Config, ops []op) {
	t.Helper()
	e := NewDiscard()
	if err := e.Schedule([]byte("n"), cfg, 0); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	m := &reference{cfg: cfg, k: 1, state: Scheduled, g: 0, t0: 0, clock: 0}
	for i, o := range ops {
		wantErr, wk, wp := m.apply(o)
		var gotErr error
		var gk int
		var gp int64
		switch o.kind {
		case opStart:
			gk, gp, gotErr = e.Start([]byte("n"), o.t)
		case opHB:
			gotErr = e.Heartbeat([]byte("n"), o.k, o.t, o.prog)
		case opComplete:
			gotErr = e.Complete([]byte("n"), o.k, o.t)
		default:
			gotErr = e.Fail([]byte("n"), o.k, o.t, o.kind == opFailRetry)
		}
		if !errEq(gotErr, wantErr) {
			t.Fatalf("op %d %+v: err=%v want=%v", i, o, gotErr, wantErr)
		}
		if gotErr == nil && o.kind == opStart && (gk != wk || gp != wp) {
			t.Fatalf("op %d start=(%d,%d) want=(%d,%d)", i, gk, gp, wk, wp)
		}
		gs, _ := e.Status([]byte("n"), o.t)
		ws := m.status(o.t)
		if gs != ws {
			t.Fatalf("op %d %+v: status=%+v want=%+v", i, o, gs, ws)
		}
	}
}

func randomConfig(rng *rand.Rand) Config {
	c := Config{
		S2S: int64(rng.Intn(6)),
		S2C: int64(rng.Intn(8)),
		HB:  int64(rng.Intn(6)),
		SC:  int64(rng.Intn(40)),
		M:   1 + rng.Intn(4),
		D0:  1 + int64(rng.Intn(4)),
		Cap: 1 + int64(rng.Intn(8)),
	}
	if c.Cap < c.D0 {
		c.Cap = c.D0
	}
	if c.S2C == 0 && c.SC == 0 {
		c.S2C = 1
	}
	return c
}

func randomOps(rng *rand.Rand, n int) []op {
	ops := make([]op, 0, n)
	var t int64
	for i := 0; i < n; i++ {
		t += int64(rng.Intn(3))
		kind := opKind(rng.Intn(5))
		ops = append(ops, op{
			kind: kind, t: t, k: 1 + rng.Intn(4),
			prog: int64(rng.Intn(10)),
		})
	}
	return ops
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 2000; iter++ {
		runScenario(t, randomConfig(rng), randomOps(rng, 30))
	}
}

func FuzzCompare(f *testing.F) {
	f.Add(int64(42), 5)
	f.Fuzz(func(t *testing.T, seed int64, n int) {
		if n < 0 {
			n = -n
		}
		if n > 60 {
			n = 60
		}
		rng := rand.New(rand.NewSource(seed))
		runScenario(t, randomConfig(rng), randomOps(rng, n))
	})
}
