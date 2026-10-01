package writestall_test

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sync"
	"testing"

	"ontology/writestall"
)

// Deterministic-test parameters; recovery lines are
// rec(S2)=12, rec(P2)=30, rec(I)=6, rec(S1)=3, rec(P1)=7.
const (
	ts1, ts2 = 4, 16
	tp1, tp2 = 10, 40
	tImm     = 8
	tDelay   = 1000
)

func newTestController(t *testing.T) *writestall.Controller {
	t.Helper()
	c, err := writestall.NewController(ts1, ts2, tp1, tp2, tImm, tDelay)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

func observe(t *testing.T, c *writestall.Controller, n0, pend, imm int64) (writestall.State, int64) {
	t.Helper()
	st, d, err := c.Observe(n0, pend, imm)
	if err != nil {
		t.Fatalf("Observe(%d,%d,%d): %v", n0, pend, imm, err)
	}
	return st, d
}

func driveTo(t *testing.T, c *writestall.Controller, seq ...[3]int64) {
	t.Helper()
	for _, o := range seq {
		observe(t, c, o[0], o[1], o[2])
	}
}

func TestNewControllerValidation(t *testing.T) {
	cases := []struct {
		name                   string
		s1, s2, p1, p2, imm, d int64
		want                   error
	}{
		{"S1==S2", 16, 16, tp1, tp2, tImm, tDelay, writestall.ErrSlowdownThresholdOrder},
		{"S1>S2", 17, 16, tp1, tp2, tImm, tDelay, writestall.ErrSlowdownThresholdOrder},
		{"P1==P2", ts1, ts2, 40, 40, tImm, tDelay, writestall.ErrPendingThresholdOrder},
		{"P1>P2", ts1, ts2, 41, 40, tImm, tDelay, writestall.ErrPendingThresholdOrder},
		{"I==0", ts1, ts2, tp1, tp2, 0, tDelay, writestall.ErrImmThreshold},
		{"I<0", ts1, ts2, tp1, tp2, -3, tDelay, writestall.ErrImmThreshold},
		{"D==0", ts1, ts2, tp1, tp2, tImm, 0, writestall.ErrMaxDelay},
		{"D<0", ts1, ts2, tp1, tp2, tImm, -7, writestall.ErrMaxDelay},
		{"all bad reports S first", 9, 9, 9, 9, 0, 0, writestall.ErrSlowdownThresholdOrder},
		{"P bad reports P before I,D", ts1, ts2, 9, 9, 0, 0, writestall.ErrPendingThresholdOrder},
		{"I bad reports I before D", ts1, ts2, tp1, tp2, -1, -1, writestall.ErrImmThreshold},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := writestall.NewController(tc.s1, tc.s2, tc.p1, tc.p2, tc.imm, tc.d)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got err=%v, want %v", err, tc.want)
			}
			if c != nil {
				t.Fatalf("expected nil controller on error, got %v", c)
			}
		})
	}
	t.Run("valid", func(t *testing.T) {
		c, err := writestall.NewController(ts1, ts2, tp1, tp2, tImm, tDelay)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.State() != writestall.StateNormal || c.Delay() != 0 {
			t.Fatalf("initial state=%v delay=%d, want Normal/0", c.State(), c.Delay())
		}
	})
}

func TestObserveNegativeArgs(t *testing.T) {
	c := newTestController(t)
	driveTo(t, c, [3]int64{4, 0, 0}) // enter Slowdown, delay=1
	beforeState, beforeDelay := c.State(), c.Delay()

	cases := []struct {
		name          string
		n0, pend, imm int64
		want          error
	}{
		{"n0 negative", -1, 0, 0, writestall.ErrNegativeL0},
		{"pend negative", 0, -1, 0, writestall.ErrNegativePending},
		{"imm negative", 0, 0, -1, writestall.ErrNegativeImm},
		{"all negative reports n0 first", -1, -1, -1, writestall.ErrNegativeL0},
		{"pend,imm negative reports pend", 0, -1, -1, writestall.ErrNegativePending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, d, err := c.Observe(tc.n0, tc.pend, tc.imm)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got err=%v, want %v", err, tc.want)
			}
			if st != beforeState || d != beforeDelay {
				t.Fatalf("rejected Observe returned (%v,%d), want unchanged (%v,%d)", st, d, beforeState, beforeDelay)
			}
			if c.State() != beforeState || c.Delay() != beforeDelay {
				t.Fatalf("rejected Observe changed state to (%v,%d)", c.State(), c.Delay())
			}
		})
	}
}

func TestStopThresholds(t *testing.T) {
	cases := []struct {
		name          string
		n0, pend, imm int64
		want          writestall.State
	}{
		{"n0=S2-1 not stopped", ts2 - 1, 0, 0, writestall.StateSlowdown},
		{"n0=S2 stopped", ts2, 0, 0, writestall.StateStopped},
		{"pend=P2-1 not stopped", 0, tp2 - 1, 0, writestall.StateSlowdown},
		{"pend=P2 stopped", 0, tp2, 0, writestall.StateStopped},
		{"imm=I-1 not stopped", 0, 0, tImm - 1, writestall.StateNormal},
		{"imm=I stopped", 0, 0, tImm, writestall.StateStopped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestController(t)
			st, _ := observe(t, c, tc.n0, tc.pend, tc.imm)
			if st != tc.want {
				t.Fatalf("state=%v, want %v", st, tc.want)
			}
		})
	}
}

func TestSlowdownThresholds(t *testing.T) {
	cases := []struct {
		name          string
		n0, pend, imm int64
		want          writestall.State
	}{
		{"n0=S1-1 normal", ts1 - 1, 0, 0, writestall.StateNormal},
		{"n0=S1 slowdown", ts1, 0, 0, writestall.StateSlowdown},
		{"pend=P1-1 normal", 0, tp1 - 1, 0, writestall.StateNormal},
		{"pend=P1 slowdown", 0, tp1, 0, writestall.StateSlowdown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestController(t)
			st, _ := observe(t, c, tc.n0, tc.pend, tc.imm)
			if st != tc.want {
				t.Fatalf("state=%v, want %v", st, tc.want)
			}
		})
	}
}

func TestStopRecoveryLine(t *testing.T) {
	stop := [3]int64{ts2, 0, 0}
	cases := []struct {
		name          string
		n0, pend, imm int64
		want          writestall.State
	}{
		// rec(S2)=12, rec(P2)=30, rec(I)=6; "below" is strict.
		{"n0==rec(S2) stays stopped", 12, 0, 0, writestall.StateStopped},
		{"n0==rec(S2)-1 exits", 11, 0, 0, writestall.StateSlowdown},
		{"pend==rec(P2) stays stopped", 0, 30, 0, writestall.StateStopped},
		{"pend==rec(P2)-1 exits", 0, 29, 0, writestall.StateSlowdown},
		{"imm==rec(I) stays stopped", 0, 0, 6, writestall.StateStopped},
		{"imm==rec(I)-1 exits", 0, 0, 5, writestall.StateNormal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestController(t)
			driveTo(t, c, stop)
			st, _ := observe(t, c, tc.n0, tc.pend, tc.imm)
			if st != tc.want {
				t.Fatalf("state=%v, want %v", st, tc.want)
			}
		})
	}
}

func TestSlowdownRecoveryLine(t *testing.T) {
	slow := [3]int64{ts1, 0, 0}
	cases := []struct {
		name          string
		n0, pend, imm int64
		want          writestall.State
	}{
		// rec(S1)=3, rec(P1)=7.
		{"n0==rec(S1) stays slowdown", 3, 0, 0, writestall.StateSlowdown},
		{"n0==rec(S1)-1 exits", 2, 0, 0, writestall.StateNormal},
		{"pend==rec(P1) stays slowdown", 0, 7, 0, writestall.StateSlowdown},
		{"pend==rec(P1)-1 exits", 0, 6, 0, writestall.StateNormal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestController(t)
			driveTo(t, c, slow)
			st, _ := observe(t, c, tc.n0, tc.pend, tc.imm)
			if st != tc.want {
				t.Fatalf("state=%v, want %v", st, tc.want)
			}
		})
	}
}

func TestStopExitPaths(t *testing.T) {
	stop := [3]int64{ts2, 0, 0}
	t.Run("exit directly to normal", func(t *testing.T) {
		c := newTestController(t)
		driveTo(t, c, stop)
		st, d := observe(t, c, 2, 5, 5) // below all recovery lines and below S1/P1
		if st != writestall.StateNormal || d != 0 {
			t.Fatalf("got (%v,%d), want (Normal,0)", st, d)
		}
	})
	t.Run("exit to slowdown", func(t *testing.T) {
		c := newTestController(t)
		driveTo(t, c, stop)
		st, d := observe(t, c, 5, 0, 0) // below recovery lines but n0>=S1
		if st != writestall.StateSlowdown || d == 0 {
			t.Fatalf("got (%v,%d), want (Slowdown,>0)", st, d)
		}
	})
}

func TestSlowdownDelay(t *testing.T) {
	cases := []struct {
		name          string
		n0, pend, imm int64
		want          int64
	}{
		// D=1000, S denominators 12 and 30.
		{"rho=0 lower bound 1", ts1, 0, 0, 1},
		{"ceil of 1000/12", 5, 0, 0, 84},
		{"n0 ratio wins", 15, tp1, 0, 917},            // 11/12 -> 916.67 -> 917 vs 0
		{"pend ratio wins", 4, 39, 0, 967},            // 0 vs 29/30 -> 966.67 -> 967
		{"rho is max of both", 8, 25, 0, 500},         // 4/12 -> 334 vs 15/30 -> 500
		{"exact division no ceil bump", 7, 0, 0, 250}, // 3/12 -> exactly 250
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestController(t)
			st, d := observe(t, c, tc.n0, tc.pend, tc.imm)
			if st != writestall.StateSlowdown {
				t.Fatalf("state=%v, want Slowdown", st)
			}
			if d != tc.want {
				t.Fatalf("delay=%d, want %d", d, tc.want)
			}
		})
	}
	t.Run("delay persists inside hysteresis band", func(t *testing.T) {
		c := newTestController(t)
		driveTo(t, c, [3]int64{ts1, 0, 0})
		st, d := observe(t, c, 3, 0, 0) // n0==rec(S1): stays Slowdown, rho<=0
		if st != writestall.StateSlowdown || d != 1 {
			t.Fatalf("got (%v,%d), want (Slowdown,1)", st, d)
		}
	})
	t.Run("delay is zero outside slowdown", func(t *testing.T) {
		c := newTestController(t)
		if _, d := observe(t, c, 0, 0, 0); d != 0 {
			t.Fatalf("normal delay=%d, want 0", d)
		}
		if _, d := observe(t, c, ts2, 0, 0); d != 0 {
			t.Fatalf("stopped delay=%d, want 0", d)
		}
	})
}

func TestAllTransitions(t *testing.T) {
	cases := []struct {
		name          string
		setup         [][3]int64
		n0, pend, imm int64
		want          writestall.State
	}{
		{"Normal->Normal", nil, 0, 0, 0, writestall.StateNormal},
		{"Normal->Slowdown", nil, ts1, 0, 0, writestall.StateSlowdown},
		{"Normal->Stopped", nil, ts2, 0, 0, writestall.StateStopped},
		{"Slowdown->Slowdown", [][3]int64{{ts1, 0, 0}}, 5, 0, 0, writestall.StateSlowdown},
		{"Slowdown->Normal", [][3]int64{{ts1, 0, 0}}, 2, 0, 0, writestall.StateNormal},
		{"Slowdown->Stopped", [][3]int64{{ts1, 0, 0}}, 0, tp2, 0, writestall.StateStopped},
		{"Stopped->Stopped", [][3]int64{{ts2, 0, 0}}, 12, 0, 0, writestall.StateStopped},
		{"Stopped->Slowdown", [][3]int64{{ts2, 0, 0}}, 5, 0, 0, writestall.StateSlowdown},
		{"Stopped->Normal", [][3]int64{{ts2, 0, 0}}, 2, 5, 5, writestall.StateNormal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestController(t)
			driveTo(t, c, tc.setup...)
			st, _ := observe(t, c, tc.n0, tc.pend, tc.imm)
			if st != tc.want {
				t.Fatalf("state=%v, want %v", st, tc.want)
			}
		})
	}
}

func TestAdmit(t *testing.T) {
	c := newTestController(t)
	if d, err := c.Admit(); err != nil || d != 0 {
		t.Fatalf("normal Admit=(%d,%v), want (0,nil)", d, err)
	}
	driveTo(t, c, [3]int64{5, 0, 0}) // Slowdown, delay 84
	if d, err := c.Admit(); err != nil || d != 84 {
		t.Fatalf("slowdown Admit=(%d,%v), want (84,nil)", d, err)
	}
	driveTo(t, c, [3]int64{ts2, 0, 0})
	if _, err := c.Admit(); !errors.Is(err, writestall.ErrWriteStopped) {
		t.Fatalf("stopped Admit err=%v, want ErrWriteStopped", err)
	}
	// The stop rejection must be distinguishable from parameter errors.
	for _, perr := range []error{
		writestall.ErrSlowdownThresholdOrder, writestall.ErrPendingThresholdOrder,
		writestall.ErrImmThreshold, writestall.ErrMaxDelay,
		writestall.ErrNegativeL0, writestall.ErrNegativePending, writestall.ErrNegativeImm,
	} {
		if errors.Is(writestall.ErrWriteStopped, perr) {
			t.Fatalf("ErrWriteStopped must differ from %v", perr)
		}
	}
}

// naiveController is a deliberately straightforward state machine written
// directly from the specification, used as the differential-test oracle.
type naiveController struct {
	s1, s2, p1, p2, imm, d int64
	state                  writestall.State
}

func (n *naiveController) observe(n0, pend, imm int64) (writestall.State, int64, string) {
	rec := func(t int64) int64 { return 3 * t / 4 }
	stop := n0 >= n.s2 || pend >= n.p2 || imm >= n.imm
	slow := n0 >= n.s1 || pend >= n.p1
	var reason string
	switch {
	case stop:
		n.state = writestall.StateStopped
		reason = "R1: stop condition holds"
	case n.state == writestall.StateStopped:
		if n0 < rec(n.s2) && pend < rec(n.p2) && imm < rec(n.imm) {
			if slow {
				n.state = writestall.StateSlowdown
				reason = "R2: exit stop -> slowdown"
			} else {
				n.state = writestall.StateNormal
				reason = "R2: exit stop -> normal"
			}
		} else {
			reason = "R2: stay stopped (above recovery line)"
		}
	case slow:
		n.state = writestall.StateSlowdown
		reason = "R3: slowdown condition holds"
	case n.state == writestall.StateSlowdown:
		if n0 < rec(n.s1) && pend < rec(n.p1) {
			n.state = writestall.StateNormal
			reason = "R4: exit slowdown -> normal"
		} else {
			reason = "R4: stay slowdown (hysteresis)"
		}
	default:
		n.state = writestall.StateNormal
		reason = "R5: normal"
	}
	delay := int64(0)
	if n.state == writestall.StateSlowdown {
		delay = naiveDelay(n.d, n0-n.s1, n.s2-n.s1, pend-n.p1, n.p2-n.p1)
	}
	return n.state, delay, reason
}

// naiveDelay computes max(1, ceil(D*rho)) independently via big.Rat.
func naiveDelay(D, aNum, aDen, bNum, bDen int64) int64 {
	rho := big.NewRat(aNum, aDen)
	if rb := big.NewRat(bNum, bDen); rb.Cmp(rho) > 0 {
		rho = rb
	}
	if rho.Sign() < 0 {
		rho = big.NewRat(0, 1)
	}
	if rho.Cmp(big.NewRat(1, 1)) > 0 {
		rho = big.NewRat(1, 1)
	}
	v := new(big.Rat).Mul(big.NewRat(D, 1), rho)
	ceil := new(big.Int).Set(v.Num())
	ceil.Add(ceil, v.Denom())
	ceil.Sub(ceil, big.NewInt(1))
	ceil.Quo(ceil, v.Denom())
	if ceil.Cmp(big.NewInt(1)) < 0 {
		ceil.SetInt64(1)
	}
	return ceil.Int64()
}

func TestFuzzAgainstNaive(t *testing.T) {
	const steps = 3000
	const s1, s2, p1, p2, imm, d = 3, 12, 100, 400, 5, 500
	c, err := writestall.NewController(s1, s2, p1, p2, imm, d)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	naive := &naiveController{s1: s1, s2: s2, p1: p1, p2: p2, imm: imm, d: d}
	rng := rand.New(rand.NewSource(20261001))
	for i := 0; i < steps; i++ {
		n0 := int64(rng.Intn(20))
		pend := int64(rng.Intn(600))
		im := int64(rng.Intn(8))
		gotState, gotDelay, gerr := c.Observe(n0, pend, im)
		if gerr != nil {
			t.Fatalf("step %d: unexpected error: %v", i, gerr)
		}
		wantState, wantDelay, reason := naive.observe(n0, pend, im)
		t.Logf("step=%d in=(n0=%d pend=%d imm=%d) out=(state=%s delay=%d) reason=%s",
			i, n0, pend, im, gotState, gotDelay, reason)
		if gotState != wantState || gotDelay != wantDelay {
			t.Fatalf("step %d in=(%d,%d,%d): got (%v,%d), naive wants (%v,%d) [%s]",
				i, n0, pend, im, gotState, gotDelay, wantState, wantDelay, reason)
		}
	}
}

func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	seq := make([][3]int64, 500)
	for i := range seq {
		seq[i] = [3]int64{int64(rng.Intn(20)), int64(rng.Intn(600)), int64(rng.Intn(8))}
	}
	run := func() [][2]int64 {
		c, err := writestall.NewController(3, 12, 100, 400, 5, 500)
		if err != nil {
			t.Fatalf("NewController: %v", err)
		}
		out := make([][2]int64, len(seq))
		for i, o := range seq {
			st, d, err := c.Observe(o[0], o[1], o[2])
			if err != nil {
				t.Fatalf("Observe: %v", err)
			}
			out[i] = [2]int64{int64(st), d}
		}
		return out
	}
	first, second := run(), run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay diverged at step %d: %v vs %v", i, first[i], second[i])
		}
	}
}

func TestConcurrentEquivalentToSerial(t *testing.T) {
	const goroutines = 16
	const perGoroutine = 200
	// Identical observations make the serial result order-independent.
	const n0, pend, imm = 6, 200, 2
	c, err := writestall.NewController(3, 12, 100, 400, 5, 500)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	naive := &naiveController{s1: 3, s2: 12, p1: 100, p2: 400, imm: 5, d: 500}
	wantState, wantDelay, _ := naive.observe(n0, pend, imm)

	var wg sync.WaitGroup
	errs := make(chan string, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < perGoroutine; k++ {
				st, d, err := c.Observe(n0, pend, imm)
				if err != nil {
					errs <- fmt.Sprintf("Observe err: %v", err)
					return
				}
				if st != wantState || d != wantDelay {
					errs <- fmt.Sprintf("got (%v,%d), want (%v,%d)", st, d, wantState, wantDelay)
					return
				}
				admitDelay, err := c.Admit()
				if err != nil || admitDelay != wantDelay {
					errs <- fmt.Sprintf("Admit=(%d,%v), want (%d,nil)", admitDelay, err, wantDelay)
					return
				}
				_ = c.State()
				_ = c.Delay()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if c.State() != wantState || c.Delay() != wantDelay {
		t.Fatalf("final (%v,%d), want (%v,%d)", c.State(), c.Delay(), wantState, wantDelay)
	}
}
