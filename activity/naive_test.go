package activity

import (
	"math/rand"
	"testing"
)

func TestNaiveDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		s2s := int64(rng.Intn(7))
		s2c := int64(rng.Intn(7))
		hb := int64(rng.Intn(7))
		sc := []int64{0, int64(3 + rng.Intn(40))}[rng.Intn(2)]
		if s2c == 0 && sc == 0 {
			s2c = 2
		}
		m := 1 + rng.Intn(4)
		d0 := int64(1 + rng.Intn(3))
		cap := d0 + int64(rng.Intn(6))
		ex := NewExecutor()
		id := []byte("z")
		if err := ex.Schedule(id, s2s, s2c, hb, sc, m, d0, cap, 0); err != nil {
			t.Fatalf("schedule: %v", err)
		}
		rm := &refModel{
			a: &refAct{state: Scheduled, k: 1, g: 0, s2s: s2s, s2c: s2c, hb: hb, sc: sc},
			m: m, d0: d0, cap: cap, exists: true,
		}
		for op := 0; op < 60; op++ {
			now := rm.clock + int64(rng.Intn(5))
			switch rng.Intn(5) {
			case 0:
				gotK, gotP, gotE := ex.Start(id, now)
				wantK, wantP, wantE := rm.start(now)
				if !errEq(gotE, wantE) || gotK != wantK || gotP != wantP {
					t.Fatalf("iter%d op%d Start now=%d got=(%d,%d,%v) want=(%d,%d,%v)",
						iter, op, now, gotK, gotP, gotE, wantK, wantP, wantE)
				}
				if gotE == nil {
					rm.commit(now)
				}
			case 1:
				k := 1 + rng.Intn(m)
				got := ex.Heartbeat(id, k, now, int64(op))
				want := rm.kcall(k, now)
				if !errEq(got, want) {
					t.Fatalf("iter%d op%d HB k=%d now=%d got=%v want=%v st=%+v",
						iter, op, k, now, got, want, rm.a.snapshot())
				}
				if got == nil {
					rm.a.state = Running
					rm.a.h, rm.a.progress = now, int64(op)
					rm.commit(now)
				}
			case 2:
				k := 1 + rng.Intn(m)
				got := ex.Complete(id, k, now)
				want := rm.kcall(k, now)
				if !errEq(got, want) {
					t.Fatalf("iter%d op%d Complete k=%d now=%d got=%v want=%v", iter, op, k, now, got, want)
				}
				if got == nil {
					rm.a.state, rm.a.reason, rm.a.termAt = Terminal, ReasonCompleted, now
					rm.commit(now)
				}
			case 3:
				k := 1 + rng.Intn(m)
				retryable := rng.Intn(2) == 0
				got := ex.Fail(id, k, now, retryable)
				want := rm.kcall(k, now)
				if !errEq(got, want) {
					t.Fatalf("iter%d op%d Fail k=%d now=%d r=%v got=%v want=%v",
						iter, op, k, now, retryable, got, want)
				}
				if got == nil {
					if !retryable {
						rm.a.state, rm.a.reason, rm.a.termAt = Terminal, ReasonApp, now
					} else {
						rm.a.fail(now, ReasonApp, rm.m, rm.d0, rm.cap)
					}
					rm.commit(now)
				}
			case 4:
				got, gerr := ex.Status(id, now)
				_, werr := rmStatus(rm, now)
				if !errEq(gerr, werr) {
					t.Fatalf("iter%d op%d Status now=%d err got=%v want=%v", iter, op, now, gerr, werr)
				}
				if gerr == nil {
					want := rmStatusSnap(rm, now)
					if got != want {
						t.Fatalf("iter%d op%d Status now=%d got=%+v want=%+v", iter, op, now, got, want)
					}
					if ex.probes > rm.lastEvents+1 {
						t.Fatalf("iter%d op%d probes=%d events=%d", iter, op, ex.probes, rm.lastEvents)
					}
				}
			}
		}
	}
}
