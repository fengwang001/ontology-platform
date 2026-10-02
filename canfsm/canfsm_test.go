package canfsm

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// driveTEC brings TEC from its current value to target (<= 255) via
// TxErr (+8) overshoot and TxOK (-1) back-off, keeping intermediate
// states below bus-off.
func driveTEC(t *testing.T, c *Controller, target int) {
	t.Helper()
	need := target - c.TEC()
	if need < 0 {
		t.Fatalf("driveTEC: current TEC=%d already above target=%d", c.TEC(), target)
	}
	n := (need + 7) / 8
	for i := 0; i < n; i++ {
		if err := c.Apply(TxErr); err != nil {
			t.Fatalf("driveTEC: Apply(TxErr) rejected: %v", err)
		}
	}
	for i := 0; i < n*8-need; i++ {
		if err := c.Apply(TxOK); err != nil {
			t.Fatalf("driveTEC: Apply(TxOK) rejected: %v", err)
		}
	}
}

// driveREC brings REC to target via RxErrDominant (+8) and RxErr (+1).
func driveREC(t *testing.T, c *Controller, target int) {
	t.Helper()
	for i := 0; i < target/8; i++ {
		if err := c.Apply(RxErrDominant); err != nil {
			t.Fatalf("driveREC: Apply(RxErrDominant) rejected: %v", err)
		}
	}
	for i := 0; i < target%8; i++ {
		if err := c.Apply(RxErr); err != nil {
			t.Fatalf("driveREC: Apply(RxErr) rejected: %v", err)
		}
	}
}

// expect checks the snapshot and logs the observed output with the rule
// that justifies it.
func expect(t *testing.T, c *Controller, tec, rec int, st State, reason string) {
	t.Helper()
	s := c.Snapshot()
	if s.TEC != tec || s.REC != rec || s.State != st {
		t.Fatalf("got TEC=%d REC=%d state=%s, want TEC=%d REC=%d state=%s (%s)",
			s.TEC, s.REC, s.State, tec, rec, st, reason)
	}
	t.Logf("out: TEC=%d REC=%d state=%s | reason: %s", s.TEC, s.REC, s.State, reason)
}

func TestTEC127vs128(t *testing.T) {
	c := New()
	driveTEC(t, c, 128)
	t.Logf("in: 16x TxErr (TEC 0 -> 128, +8 each)")
	expect(t, c, 128, 0, StateErrorPassive, "TEC=128 > 127 => error passive")

	if err := c.Apply(TxOK); err != nil {
		t.Fatalf("Apply(TxOK) rejected: %v", err)
	}
	t.Logf("in: TxOK (TEC 128 -> 127)")
	expect(t, c, 127, 0, StateErrorActive, "TEC=127 <= 127 and REC=0 <= 127 => error active")
}

func TestTEC255StaysPassive(t *testing.T) {
	c := New()
	driveTEC(t, c, 247)
	t.Logf("in: 31x TxErr + 1x TxOK (TEC 0 -> 248 -> 247)")
	expect(t, c, 247, 0, StateErrorPassive, "TEC=247 > 127 => error passive")

	if err := c.Apply(TxErr); err != nil {
		t.Fatalf("Apply(TxErr) rejected: %v", err)
	}
	t.Logf("in: TxErr (TEC 247 -> 255)")
	expect(t, c, 255, 0, StateErrorPassive, "TEC=255 is not > 255 => still error passive, not bus-off")
}

func TestTEC256GoesBusOff(t *testing.T) {
	c := New()
	driveTEC(t, c, 248)
	t.Logf("in: 31x TxErr (TEC 0 -> 248)")
	expect(t, c, 248, 0, StateErrorPassive, "TEC=248 > 127 => error passive")

	if err := c.Apply(TxErr); err != nil {
		t.Fatalf("Apply(TxErr) rejected: %v", err)
	}
	t.Logf("in: TxErr (TEC 248 -> 256)")
	expect(t, c, 256, 0, StateBusOff, "TEC=256 > 255 => bus-off")
}

func TestRxOKClampsAbove127(t *testing.T) {
	c := New()
	driveREC(t, c, 130)
	t.Logf("in: 16x RxErrDominant + 2x RxErr (REC 0 -> 128 -> 130)")
	expect(t, c, 0, 130, StateErrorPassive, "REC=130 > 127 => error passive")

	if err := c.Apply(RxOK); err != nil {
		t.Fatalf("Apply(RxOK) rejected: %v", err)
	}
	t.Logf("in: RxOK (REC 130 -> 127)")
	expect(t, c, 0, 127, StateErrorActive, "RxOK with REC>127 sets REC=127; TEC=0 <= 127 => error active")
}

func TestREC127to128(t *testing.T) {
	c := New()
	driveREC(t, c, 127)
	t.Logf("in: 15x RxErrDominant + 7x RxErr (REC 0 -> 120 -> 127)")
	expect(t, c, 0, 127, StateErrorActive, "REC=127 <= 127 and TEC=0 <= 127 => error active")

	if err := c.Apply(RxErr); err != nil {
		t.Fatalf("Apply(RxErr) rejected: %v", err)
	}
	t.Logf("in: RxErr (REC 127 -> 128)")
	expect(t, c, 0, 128, StateErrorPassive, "REC=128 > 127 => error passive")
}

func TestTxAckErr(t *testing.T) {
	c := New()
	if err := c.Apply(TxAckErr); err != nil {
		t.Fatalf("Apply(TxAckErr) rejected: %v", err)
	}
	t.Logf("in: TxAckErr while error active (TEC 0 -> 8)")
	expect(t, c, 8, 0, StateErrorActive, "TxAckErr in error active: TEC += 8")

	driveTEC(t, c, 128)
	t.Logf("in: 15x TxErr (TEC 8 -> 128)")
	expect(t, c, 128, 0, StateErrorPassive, "TEC=128 > 127 => error passive")

	if err := c.Apply(TxAckErr); err != nil {
		t.Fatalf("Apply(TxAckErr) rejected: %v", err)
	}
	t.Logf("in: TxAckErr while error passive (TEC stays 128)")
	expect(t, c, 128, 0, StateErrorPassive, "TxAckErr in error passive: TEC and REC unchanged")
}

func TestTxOKNoUnderflow(t *testing.T) {
	c := New()
	if err := c.Apply(TxOK); err != nil {
		t.Fatalf("Apply(TxOK) rejected: %v", err)
	}
	t.Logf("in: TxOK with TEC=0")
	expect(t, c, 0, 0, StateErrorActive, "TxOK with TEC=0: TEC stays 0, no underflow")
}

// busOff drives the node into bus-off via TxErr (+8 each).
func busOff(t *testing.T, c *Controller) {
	t.Helper()
	for i := 0; i < 100 && c.State() != StateBusOff; i++ {
		if err := c.Apply(TxErr); err != nil {
			t.Fatalf("busOff: Apply(TxErr) rejected: %v", err)
		}
	}
	if st := c.State(); st != StateBusOff {
		t.Fatalf("busOff: state=%s, want BusOff", st)
	}
}

func TestRecovery128Idle11(t *testing.T) {
	c := New()
	busOff(t, c)
	t.Logf("in: 32x TxErr (TEC 0 -> 256)")
	expect(t, c, 256, 0, StateBusOff, "TEC=256 > 255 => bus-off")

	if err := c.Restart(); err != nil {
		t.Fatalf("Restart rejected: %v", err)
	}
	t.Logf("in: Restart while bus-off => recovery starts, recovery count = 0")
	if !c.Recovering() || c.RecoveryCount() != 0 {
		t.Fatalf("after Restart: recovering=%v count=%d, want true/0", c.Recovering(), c.RecoveryCount())
	}

	for i := 1; i <= 127; i++ {
		if err := c.Idle11(); err != nil {
			t.Fatalf("Idle11 #%d rejected: %v", i, err)
		}
	}
	t.Logf("in: 127x Idle11")
	s := c.Snapshot()
	if !s.Recovering || s.RecoveryCount != 127 || s.State != StateBusOff || s.TEC != 256 {
		t.Fatalf("after 127 Idle11: %+v, want recovering=true count=127 state=BusOff TEC=256", s)
	}
	t.Logf("out: recovering=true count=127 state=%s TEC=%d | reason: 127 < 128, recovery not complete", s.State, s.TEC)

	if err := c.Idle11(); err != nil {
		t.Fatalf("Idle11 #128 rejected: %v", err)
	}
	t.Logf("in: 128th Idle11")
	expect(t, c, 0, 0, StateErrorActive, "128th Idle11: TEC=REC=0, recovery ends, back to error active")
	if c.Recovering() {
		t.Fatal("after 128th Idle11: still recovering")
	}

	tr := c.Transitions()
	last := tr[len(tr)-1]
	if last.From != StateBusOff || last.To != StateErrorActive {
		t.Fatalf("last transition=%+v, want BusOff->ErrorActive", last)
	}
	t.Logf("out: last transition=%s->%s at op #%d | reason: recovery completion recorded", last.From, last.To, last.Op)
}

func TestRestartRejections(t *testing.T) {
	c := New()
	if err := c.Restart(); !errors.Is(err, ErrNotBusOff) {
		t.Fatalf("Restart while active: err=%v, want ErrNotBusOff", err)
	}
	t.Logf("in: Restart while error active => rejected: %v", ErrNotBusOff)

	busOff(t, c)
	if err := c.Restart(); err != nil {
		t.Fatalf("Restart while bus-off rejected: %v", err)
	}
	if err := c.Restart(); !errors.Is(err, ErrAlreadyRecovering) {
		t.Fatalf("Restart during recovery: err=%v, want ErrAlreadyRecovering", err)
	}
	t.Logf("in: Restart during recovery => rejected: %v", ErrAlreadyRecovering)

	for i := 0; i < 128; i++ {
		if err := c.Idle11(); err != nil {
			t.Fatalf("Idle11 #%d rejected: %v", i+1, err)
		}
	}
	if err := c.Restart(); !errors.Is(err, ErrNotBusOff) {
		t.Fatalf("Restart after recovery: err=%v, want ErrNotBusOff", err)
	}
	t.Logf("in: Restart after recovery (error active) => rejected: %v", ErrNotBusOff)
}

func TestApplyAndIdle11Rejections(t *testing.T) {
	c := New()
	if err := c.Apply(Event(99)); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("Apply(invalid): err=%v, want ErrInvalidEvent", err)
	}
	if err := c.Apply(Event(-1)); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("Apply(negative): err=%v, want ErrInvalidEvent", err)
	}
	t.Logf("in: Apply with unknown event => rejected: %v", ErrInvalidEvent)

	if err := c.Idle11(); !errors.Is(err, ErrNotRecovering) {
		t.Fatalf("Idle11 while not recovering: err=%v, want ErrNotRecovering", err)
	}
	t.Logf("in: Idle11 while not recovering => rejected: %v", ErrNotRecovering)

	busOff(t, c)
	if err := c.Apply(Event(99)); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("Apply(invalid) while bus-off: err=%v, want ErrInvalidEvent (checked before bus-off)", err)
	}
	t.Logf("in: Apply with unknown event while bus-off => rejected: %v (event check first)", ErrInvalidEvent)
	if err := c.Apply(TxErr); !errors.Is(err, ErrBusOff) {
		t.Fatalf("Apply(TxErr) while bus-off: err=%v, want ErrBusOff", err)
	}
	t.Logf("in: Apply(TxErr) while bus-off => rejected: %v", ErrBusOff)
}

func TestRejectedOpsDoNotConsumeSeq(t *testing.T) {
	c := New()

	// 1 successful op: TEC 0 -> 8.
	if err := c.Apply(TxErr); err != nil {
		t.Fatal(err)
	}
	// Rejected ops: none of them may touch counters, recovery, log or seq.
	rejections := []error{
		c.Apply(Event(7)), // invalid event
		c.Restart(),       // not bus-off
		c.Idle11(),        // not recovering
	}
	for i, err := range rejections {
		if err == nil {
			t.Fatalf("rejection #%d unexpectedly succeeded", i)
		}
	}
	s := c.Snapshot()
	if s.TEC != 8 || s.OpSeq != 1 || len(c.Transitions()) != 0 {
		t.Fatalf("after rejections: %+v transitions=%v, want TEC=8 OpSeq=1 no transitions", s, c.Transitions())
	}
	t.Logf("out: TEC=%d OpSeq=%d transitions=%d | reason: rejected ops leave state untouched", s.TEC, s.OpSeq, len(c.Transitions()))

	// Drive to bus-off; every Apply here is successful.
	busOff(t, c) // 31 more successful ops, OpSeq = 32
	seqBefore := c.Snapshot().OpSeq
	if err := c.Apply(TxOK); !errors.Is(err, ErrBusOff) {
		t.Fatalf("Apply while bus-off: err=%v, want ErrBusOff", err)
	}
	if err := c.Restart(); err != nil {
		t.Fatal(err)
	}
	if err := c.Restart(); !errors.Is(err, ErrAlreadyRecovering) {
		t.Fatalf("Restart during recovery: err=%v, want ErrAlreadyRecovering", err)
	}
	for i := 0; i < 128; i++ {
		if err := c.Idle11(); err != nil {
			t.Fatal(err)
		}
	}

	// Successful ops: 32 Applies to bus-off + 1 Restart + 128 Idle11 = 161.
	s = c.Snapshot()
	if s.OpSeq != seqBefore+129 {
		t.Fatalf("OpSeq=%d, want %d (rejected ops must not count)", s.OpSeq, seqBefore+129)
	}
	tr := c.Transitions()
	prevTo := StateErrorActive
	prevOp := 0
	for _, x := range tr {
		if x.From != prevTo {
			t.Fatalf("transition %+v does not chain from %s", x, prevTo)
		}
		if x.Op <= prevOp {
			t.Fatalf("transition ops not increasing: %d after %d", x.Op, prevOp)
		}
		prevTo, prevOp = x.To, x.Op
	}
	t.Logf("out: OpSeq=%d transitions=%v | reason: only successful ops get a sequence number", s.OpSeq, tr)
}

// naive is a straightforward step-by-step simulation of the rules, used
// as an independent reference for replay comparison.
type naive struct {
	tec, rec      int
	recovering    bool
	recoveryCount int
	opSeq         int
	transitions   []Transition
}

func (n *naive) state() State {
	if n.tec > 255 {
		return StateBusOff
	}
	if n.tec > 127 || n.rec > 127 {
		return StateErrorPassive
	}
	return StateErrorActive
}

func (n *naive) record(from State) {
	if to := n.state(); to != from {
		n.transitions = append(n.transitions, Transition{Op: n.opSeq, From: from, To: to})
	}
}

func (n *naive) apply(ev Event) error {
	if ev < TxOK || ev > RxErrDominant {
		return ErrInvalidEvent
	}
	before := n.state()
	if before == StateBusOff {
		return ErrBusOff
	}
	switch ev {
	case TxOK:
		if n.tec > 0 {
			n.tec--
		}
	case TxErr:
		n.tec += 8
	case TxAckErr:
		if before == StateErrorActive {
			n.tec += 8
		}
	case RxOK:
		if n.rec > 127 {
			n.rec = 127
		} else if n.rec > 0 {
			n.rec--
		}
	case RxErr:
		n.rec++
	case RxErrDominant:
		n.rec += 8
	}
	n.opSeq++
	n.record(before)
	return nil
}

func (n *naive) restart() error {
	if n.state() != StateBusOff {
		return ErrNotBusOff
	}
	if n.recovering {
		return ErrAlreadyRecovering
	}
	n.recovering = true
	n.recoveryCount = 0
	n.opSeq++
	return nil
}

func (n *naive) idle11() error {
	if !n.recovering {
		return ErrNotRecovering
	}
	n.recoveryCount++
	n.opSeq++
	if n.recoveryCount == 128 {
		before := n.state()
		n.tec, n.rec = 0, 0
		n.recovering = false
		n.record(before)
	}
	return nil
}

// opKind is one operation in a replay script.
type opKind struct {
	name string
	ev   Event // valid when name == "Apply"
}

func runOp(c *Controller, n *naive, op opKind) (errC, errN error) {
	switch op.name {
	case "Apply":
		errC, errN = c.Apply(op.ev), n.apply(op.ev)
	case "Restart":
		errC, errN = c.Restart(), n.restart()
	case "Idle11":
		errC, errN = c.Idle11(), n.idle11()
	}
	return errC, errN
}

func checkAgainstNaive(t *testing.T, c *Controller, n *naive, step int, op opKind) {
	t.Helper()
	s := c.Snapshot()
	if s.TEC != n.tec || s.REC != n.rec || s.State != n.state() ||
		s.Recovering != n.recovering || s.RecoveryCount != n.recoveryCount || s.OpSeq != n.opSeq {
		t.Fatalf("step %d (%v): controller %+v != naive %+v", step, op, s, n)
	}
	// Invariants.
	if s.State != StateBusOff && s.TEC > 255 {
		t.Fatalf("step %d: TEC=%d > 255 while not bus-off", step, s.TEC)
	}
	if s.State == StateBusOff && s.TEC <= 255 {
		t.Fatalf("step %d: TEC=%d <= 255 while bus-off", step, s.TEC)
	}
	if !s.Recovering && s.State == StateErrorActive && s.RecoveryCount != 0 && s.RecoveryCount != 128 {
		t.Fatalf("step %d: stale recovery count %d", step, s.RecoveryCount)
	}
}

func checkTransitions(t *testing.T, got, want []Transition) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("transitions len=%d, want %d (%v)", len(got), len(want), want)
	}
	prevTo := StateErrorActive
	for i, x := range got {
		if x != want[i] {
			t.Fatalf("transition[%d]=%+v, want %+v", i, x, want[i])
		}
		if x.From != prevTo {
			t.Fatalf("transition[%d]=%+v does not chain from %s", i, x, prevTo)
		}
		prevTo = x.To
	}
}

func TestReplayMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	events := []Event{TxOK, TxErr, TxAckErr, RxOK, RxErr, RxErrDominant}

	var script []opKind
	for i := 0; i < 4000; i++ {
		switch r := rng.Intn(100); {
		case r < 80:
			script = append(script, opKind{"Apply", events[rng.Intn(len(events))]})
		case r < 85:
			script = append(script, opKind{"Apply", Event(-1 - rng.Intn(3))}) // invalid event
		case r < 93:
			script = append(script, opKind{"Restart", 0})
		default:
			script = append(script, opKind{"Idle11", 0})
		}
	}

	c := New()
	n := &naive{}
	for i, op := range script {
		errC, errN := runOp(c, n, op)
		if !errors.Is(errC, errN) {
			t.Fatalf("step %d (%v): controller err=%v, naive err=%v", i, op, errC, errN)
		}
		checkAgainstNaive(t, c, n, i, op)
		if i%500 == 0 || errC != nil {
			s := c.Snapshot()
			t.Logf("step %d in=%v out: TEC=%d REC=%d state=%s recovering=%v count=%d opSeq=%d err=%v",
				i, op, s.TEC, s.REC, s.State, s.Recovering, s.RecoveryCount, s.OpSeq, errC)
		}
	}
	checkTransitions(t, c.Transitions(), n.transitions)
	t.Logf("out: %d transitions replayed identically | reason: same op sequence => same counters, state and log",
		len(n.transitions))

	// Determinism: replaying the same script on a fresh controller must
	// reproduce counters, state and the transition log exactly.
	c2 := New()
	for _, op := range script {
		switch op.name {
		case "Apply":
			_ = c2.Apply(op.ev)
		case "Restart":
			_ = c2.Restart()
		case "Idle11":
			_ = c2.Idle11()
		}
	}
	s1, s2 := c.Snapshot(), c2.Snapshot()
	if s1 != s2 {
		t.Fatalf("replay diverged: %+v != %+v", s1, s2)
	}
	tr1, tr2 := c.Transitions(), c2.Transitions()
	if len(tr1) != len(tr2) {
		t.Fatalf("replay transition logs differ: %d vs %d", len(tr1), len(tr2))
	}
	for i := range tr1 {
		if tr1[i] != tr2[i] {
			t.Fatalf("replay transition[%d]: %+v != %+v", i, tr1[i], tr2[i])
		}
	}
	t.Logf("out: second replay identical (snapshot=%+v) | reason: deterministic rules", s2)
}

func TestConcurrentLinearizable(t *testing.T) {
	c := New()
	var success atomic.Int64
	var wg sync.WaitGroup

	worker := func(seed int64) {
		defer wg.Done()
		rng := rand.New(rand.NewSource(seed))
		events := []Event{TxOK, TxErr, TxAckErr, RxOK, RxErr, RxErrDominant}
		for i := 0; i < 2000; i++ {
			var err error
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4, 5, 6:
				err = c.Apply(events[rng.Intn(len(events))])
			case 7:
				err = c.Restart()
			case 8:
				err = c.Idle11()
			default:
				_ = c.Snapshot()
				_ = c.Transitions()
				continue
			}
			if err == nil {
				success.Add(1)
			}
		}
	}

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go worker(int64(w) + 1)
	}
	wg.Wait()

	s := c.Snapshot()
	if s.OpSeq != int(success.Load()) {
		t.Fatalf("OpSeq=%d, want %d successful ops", s.OpSeq, success.Load())
	}
	// The transition log must chain, whatever serial order was taken.
	prevTo := StateErrorActive
	for i, x := range c.Transitions() {
		if x.From != prevTo {
			t.Fatalf("transition[%d]=%+v does not chain from %s", i, x, prevTo)
		}
		prevTo = x.To
	}
	if s.State != StateBusOff && s.TEC > 255 {
		t.Fatalf("TEC=%d > 255 while not bus-off", s.TEC)
	}
	t.Logf("out: OpSeq=%d matches successful op count; %d chained transitions | reason: mutex serializes all ops",
		s.OpSeq, len(c.Transitions()))
}
