package coalesce

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func mustNew(t *testing.T, gap, thr int64) *Coalescer {
	t.Helper()
	c, err := New(gap, thr)
	if err != nil {
		t.Fatalf("New(%d,%d) unexpected error: %v", gap, thr, err)
	}
	return c
}

func do(t *testing.T, c *Coalescer, label string, f func() error) error {
	t.Helper()
	err := f()
	snap := c.Snapshot()
	t.Logf("%-26s -> err=%v records=%v pending=%d waitAck=%v masked=%v",
		label, err, c.Records(), snap.Pending, snap.WaitAck, snap.Masked)
	return err
}

func wantRecords(t *testing.T, c *Coalescer, why string, want []Record) {
	t.Helper()
	got := c.Records()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("records mismatch (%s)\n got: %v\nwant: %v", why, got, want)
	}
	t.Logf("OK [%s]: records=%v", why, got)
}

func wantErr(t *testing.T, err error, sentinel error, why string) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("%s: want error %v, got %v", why, sentinel, err)
	}
	t.Logf("OK [%s]: rejected with %v", why, err)
}

func TestConstructorValidation(t *testing.T) {
	if _, err := New(-1, 5); !errors.Is(err, ErrNegativeGap) {
		t.Fatalf("gap<0: want ErrNegativeGap, got %v", err)
	}
	if _, err := New(0, 0); !errors.Is(err, ErrBadThreshold) {
		t.Fatalf("thr<1: want ErrBadThreshold, got %v", err)
	}
	if _, err := New(-2, 0); !errors.Is(err, ErrNegativeGap) {
		t.Fatalf("both bad: want ErrNegativeGap reported first, got %v", err)
	}
	if _, err := New(0, 1); err != nil {
		t.Fatalf("gap=0, thr=1 must be accepted, got %v", err)
	}
	t.Log("constructor: gap<0 and thr<1 rejected with distinguishable reasons")
}

func TestFireExactlyAtAllow(t *testing.T) {
	c := mustNew(t, 5, 10)
	do(t, c, "Event(0,1)", func() error { return c.Event(0, 1) })
	do(t, c, "Ack(1)", func() error { return c.Ack(1) })
	do(t, c, "Event(2,1)", func() error { return c.Event(2, 1) })
	do(t, c, "Event(5,1) t==allow", func() error { return c.Event(5, 1) })
	do(t, c, "Ack(6)", func() error { return c.Ack(6) })
	do(t, c, "Tick(10) t==allow", func() error { return c.Tick(10) })
	do(t, c, "Ack(11)", func() error { return c.Ack(11) })
	do(t, c, "Event(15,2) t==allow", func() error { return c.Event(15, 2) })
	wantRecords(t, c, "fires exactly at allow=last+gap, both catch-up and at-t paths",
		[]Record{{0, 1}, {5, 1}, {10, 1}, {15, 2}})
}

func TestThresholdOverridesGap(t *testing.T) {
	c := mustNew(t, 100, 3)
	do(t, c, "Event(0,1)", func() error { return c.Event(0, 1) })
	do(t, c, "Ack(1)", func() error { return c.Ack(1) })
	do(t, c, "Event(2,2)", func() error { return c.Event(2, 2) })
	do(t, c, "Event(3,1) p==thr", func() error { return c.Event(3, 1) })
	wantRecords(t, c, "p reaching thr fires immediately at t=3 despite allow=100",
		[]Record{{0, 1}, {3, 3}})
}

func TestOneBelowThresholdNoFire(t *testing.T) {
	c := mustNew(t, 100, 3)
	do(t, c, "Event(0,1)", func() error { return c.Event(0, 1) })
	do(t, c, "Ack(1)", func() error { return c.Ack(1) })
	do(t, c, "Event(2,2) p==thr-1", func() error { return c.Event(2, 2) })
	do(t, c, "Tick(50) before allow", func() error { return c.Tick(50) })
	wantRecords(t, c, "p=thr-1 never fires before allow", []Record{{0, 1}})
	if got := c.Snapshot().Pending; got != 2 {
		t.Fatalf("pending: want 2, got %d", got)
	}
	t.Log("OK [p=thr-1 stays pending]: pending=2")
}

func TestTickCatchUpFiresAtAllowNotT(t *testing.T) {
	c := mustNew(t, 10, 100)
	do(t, c, "Event(0,1)", func() error { return c.Event(0, 1) })
	do(t, c, "Ack(1)", func() error { return c.Ack(1) })
	do(t, c, "Event(2,5)", func() error { return c.Event(2, 5) })
	do(t, c, "Tick(15) skips allow", func() error { return c.Tick(15) })
	do(t, c, "Ack(16)", func() error { return c.Ack(16) })
	do(t, c, "Event(17,3)", func() error { return c.Event(17, 3) })
	do(t, c, "Tick(19) before allow=20", func() error { return c.Tick(19) })
	do(t, c, "Tick(1000) big skip", func() error { return c.Tick(1000) })
	wantRecords(t, c, "catch-up fires at allow (10, then 20), not at t; next allow counts from catch-up time",
		[]Record{{0, 1}, {10, 5}, {20, 3}})
}

func TestAckFiresImmediatelyAtAckTime(t *testing.T) {
	c := mustNew(t, 1000, 100)
	do(t, c, "Event(0,1)", func() error { return c.Event(0, 1) })
	do(t, c, "Event(5,4) blocked by waitAck", func() error { return c.Event(5, 4) })
	do(t, c, "Tick(2000) still blocked", func() error { return c.Tick(2000) })
	do(t, c, "Ack(2000)", func() error { return c.Ack(2000) })
	wantRecords(t, c, "events accumulated while awaiting ack fire at the Ack's own t",
		[]Record{{0, 1}, {2000, 4}})
}

func TestUnmaskFiresImmediately(t *testing.T) {
	c := mustNew(t, 0, 5)
	do(t, c, "Event(0,1)", func() error { return c.Event(0, 1) })
	do(t, c, "Ack(1)", func() error { return c.Ack(1) })
	do(t, c, "Mask(2)", func() error { return c.Mask(2) })
	do(t, c, "Event(3,4) masked", func() error { return c.Event(3, 4) })
	do(t, c, "Tick(10) masked", func() error { return c.Tick(10) })
	do(t, c, "Unmask(11)", func() error { return c.Unmask(11) })
	wantRecords(t, c, "events accumulated while masked fire at the Unmask's own t",
		[]Record{{0, 1}, {11, 4}})
}

func TestMaskAndCatchUpSameTime(t *testing.T) {
	t.Run("MaskAtAllowCatchUpWins", func(t *testing.T) {
		c := mustNew(t, 10, 100)
		do(t, c, "Event(0,1)", func() error { return c.Event(0, 1) })
		do(t, c, "Ack(1)", func() error { return c.Ack(1) })
		do(t, c, "Event(2,3)", func() error { return c.Event(2, 3) })
		do(t, c, "Mask(10) t==allow", func() error { return c.Mask(10) })
		wantRecords(t, c, "step 1 catch-up runs before Mask is applied, so it fires at allow=10",
			[]Record{{0, 1}, {10, 3}})
	})
	t.Run("MaskBeforeAllowBlocksCatchUp", func(t *testing.T) {
		c := mustNew(t, 10, 100)
		do(t, c, "Event(0,1)", func() error { return c.Event(0, 1) })
		do(t, c, "Ack(1)", func() error { return c.Ack(1) })
		do(t, c, "Event(2,3)", func() error { return c.Event(2, 3) })
		do(t, c, "Mask(9)", func() error { return c.Mask(9) })
		do(t, c, "Tick(10) masked", func() error { return c.Tick(10) })
		wantRecords(t, c, "mask already set blocks the catch-up at allow=10",
			[]Record{{0, 1}})
		do(t, c, "Unmask(10)", func() error { return c.Unmask(10) })
		wantRecords(t, c, "unmask at t=10 fires immediately (t>=allow)",
			[]Record{{0, 1}, {10, 3}})
	})
}

func TestRejectionsDoNotChangeState(t *testing.T) {
	c := mustNew(t, 5, 100)
	do(t, c, "Event(0,2)", func() error { return c.Event(0, 2) })
	do(t, c, "Ack(1)", func() error { return c.Ack(1) })
	do(t, c, "Event(2,1)", func() error { return c.Event(2, 1) })

	err := do(t, c, "Ack(10) not waiting", func() error { return c.Ack(10) })
	wantErr(t, err, ErrNotWaitingAck, "ack while waitAck=false")
	wantRecords(t, c, "rejected Ack must not run the catch-up that allow=5<=10 would allow",
		[]Record{{0, 2}})

	do(t, c, "Tick(10)", func() error { return c.Tick(10) })
	wantRecords(t, c, "catch-up fires at allow=5 once the op is legal",
		[]Record{{0, 2}, {5, 1}})

	do(t, c, "Ack(10)", func() error { return c.Ack(10) })
	do(t, c, "Event(10,1)", func() error { return c.Event(10, 1) })
	do(t, c, "Ack(11)", func() error { return c.Ack(11) })

	err = do(t, c, "Event(20,0) bad n", func() error { return c.Event(20, 0) })
	wantErr(t, err, ErrBadEventCount, "event with n<1")
	do(t, c, "Event(15,1)", func() error { return c.Event(15, 1) })
	wantRecords(t, c, "rejected op at t=20 must not advance lastT, so t=15 is still legal",
		[]Record{{0, 2}, {5, 1}, {10, 1}, {15, 1}})

	do(t, c, "Ack(16)", func() error { return c.Ack(16) })
	err = do(t, c, "Ack(16) again", func() error { return c.Ack(16) })
	wantErr(t, err, ErrNotWaitingAck, "second ack at same t is legal in time but not waiting")

	err = do(t, c, "Event(14,1) regression", func() error { return c.Event(14, 1) })
	wantErr(t, err, ErrTimeRegression, "t before last successful op")
	err = do(t, c, "Event(14,0) regression+badn", func() error { return c.Event(14, 0) })
	wantErr(t, err, ErrTimeRegression, "time regression has priority over bad n")
	err = do(t, c, "Ack(14) regression", func() error { return c.Ack(14) })
	wantErr(t, err, ErrTimeRegression, "time regression has priority over ack-state check")

	snap := c.Snapshot()
	if snap.Pending != 0 || snap.Fires != 4 || snap.Acks != 4 {
		t.Fatalf("final state: want pending=0 fires=4 acks=4, got %+v", snap)
	}
	t.Logf("OK [rejections left no trace]: snapshot=%+v", snap)
}

// naive is a deliberately literal, step-by-step transcription of the spec:
// step 1 catch-up at allow, step 2 apply the op, step 3 fire check at t.
type naive struct {
	gap, thr int64
	p, last  int64
	fired    bool
	waitAck  bool
	masked   bool
	recs     []Record
	lastT    int64
	hasT     bool
}

type testOp struct {
	kind string
	t, n int64
}

func (o testOp) String() string {
	if o.kind == "event" {
		return fmt.Sprintf("Event(t=%d,n=%d)", o.t, o.n)
	}
	return fmt.Sprintf("%s(t=%d)", o.kind, o.t)
}

func (n *naive) allow() int64 { return n.last + n.gap }

func (n *naive) fire(at int64) {
	n.recs = append(n.recs, Record{Time: at, Count: n.p})
	n.p = 0
	n.last = at
	n.fired = true
	n.waitAck = true
}

// apply returns the same sentinel reason the Coalescer would reject with.
func (n *naive) apply(o testOp) error {
	if n.hasT && o.t < n.lastT {
		return ErrTimeRegression
	}
	switch o.kind {
	case "Event":
		if o.n < 1 {
			return ErrBadEventCount
		}
	case "Ack":
		if !n.waitAck {
			return ErrNotWaitingAck
		}
	}
	// Step 1: catch-up fire at allow (never before the first fire).
	if n.fired && n.p > 0 && !n.waitAck && !n.masked && n.p < n.thr && n.allow() <= o.t {
		n.fire(n.allow())
	}
	// Step 2: apply the operation itself.
	switch o.kind {
	case "Event":
		n.p += o.n
	case "Ack":
		n.waitAck = false
	case "Mask":
		n.masked = true
	case "Unmask":
		n.masked = false
	case "Tick":
	}
	// Step 3: fire check at t.
	if n.p > 0 && !n.waitAck && !n.masked && (!n.fired || o.t >= n.allow() || n.p >= n.thr) {
		n.fire(o.t)
	}
	n.lastT = o.t
	n.hasT = true
	return nil
}

func (n *naive) run(ops []testOp) {
	for _, o := range ops {
		n.apply(o)
	}
}

func applyOp(c *Coalescer, o testOp) error {
	switch o.kind {
	case "Event":
		return c.Event(o.t, o.n)
	case "Tick":
		return c.Tick(o.t)
	case "Ack":
		return c.Ack(o.t)
	case "Mask":
		return c.Mask(o.t)
	case "Unmask":
		return c.Unmask(o.t)
	}
	panic("unknown op kind " + o.kind)
}

// reasonOf maps an error to its sentinel reason for comparison.
func reasonOf(err error) error {
	for _, s := range []error{ErrTimeRegression, ErrBadEventCount, ErrNotWaitingAck} {
		if errors.Is(err, s) {
			return s
		}
	}
	return nil
}

func genOps(r *rand.Rand, count int) []testOp {
	kinds := []string{"Event", "Event", "Event", "Tick", "Tick", "Ack", "Mask", "Unmask"}
	ops := make([]testOp, 0, count)
	cur := int64(0)
	for i := 0; i < count; i++ {
		t := cur + r.Int63n(25)
		if r.Intn(10) == 0 && i > 0 {
			t = cur - 1 - r.Int63n(10) // time regression
		} else {
			cur = t
		}
		kind := kinds[r.Intn(len(kinds))]
		n := int64(0)
		if kind == "Event" {
			n = 1 + r.Int63n(8)
			if r.Intn(10) == 0 {
				n = -r.Int63n(2) // invalid n
			}
		}
		ops = append(ops, testOp{kind: kind, t: t, n: n})
	}
	return ops
}

func checkInvariants(t *testing.T, c *Coalescer, eventSum int64) {
	t.Helper()
	snap := c.Snapshot()
	recs := c.Records()
	var carried int64
	prev := int64(math.MinInt64)
	for i, r := range recs {
		if i > 0 && r.Time < prev {
			t.Fatalf("fire times not non-decreasing: records=%v", recs)
		}
		prev = r.Time
		carried += r.Count
	}
	if carried+snap.Pending != eventSum {
		t.Fatalf("accounting broken: carried=%d + pending=%d != eventSum=%d",
			carried, snap.Pending, eventSum)
	}
	if d := snap.Fires - snap.Acks; d != 0 && d != 1 {
		t.Fatalf("fires-acks must be 0 or 1, got %d (fires=%d acks=%d)", d, snap.Fires, snap.Acks)
	}
	t.Logf("invariants hold: fires=%d acks=%d carried=%d pending=%d eventSum=%d",
		snap.Fires, snap.Acks, carried, snap.Pending, eventSum)
}

func TestNaiveSimulationFuzz(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		r := rand.New(rand.NewSource(seed))
		gap := r.Int63n(8)
		thr := 1 + r.Int63n(6)
		ops := genOps(r, 300)

		c := mustNew(t, gap, thr)
		n := &naive{gap: gap, thr: thr}
		var eventSum int64
		for i, o := range ops {
			errC := applyOp(c, o)
			errN := n.apply(o)
			if reasonOf(errC) != reasonOf(errN) {
				t.Fatalf("seed=%d op %d %s: coalescer err=%v, naive err=%v",
					seed, i, o, errC, errN)
			}
			if errC == nil && o.kind == "Event" {
				eventSum += o.n
			}
			if got, want := c.Records(), n.recs; !reflect.DeepEqual(got, want) {
				t.Fatalf("seed=%d op %d %s:\ncoalescer records=%v\nnaive records=%v",
					seed, i, o, got, want)
			}
		}
		checkInvariants(t, c, eventSum)

		// Replay: the same op sequence must reproduce identical records.
		replay := mustNew(t, gap, thr)
		for _, o := range ops {
			applyOp(replay, o)
		}
		if got, want := replay.Records(), c.Records(); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%d replay diverged: %v vs %v", seed, got, want)
		}
		t.Logf("seed=%d gap=%d thr=%d ops=%d -> records=%v (replay identical)",
			seed, gap, thr, len(ops), c.Records())
	}
}

func TestConcurrentLinearizable(t *testing.T) {
	c := mustNew(t, 3, 5)
	var clock int64
	var eventSum int64
	var wg sync.WaitGroup
	kinds := []string{"Event", "Event", "Tick", "Ack", "Mask", "Unmask"}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 200; i++ {
				t := atomic.AddInt64(&clock, 1+r.Int63n(5))
				o := testOp{kind: kinds[r.Intn(len(kinds))], t: t, n: 1 + r.Int63n(4)}
				if err := applyOp(c, o); err == nil && o.kind == "Event" {
					atomic.AddInt64(&eventSum, o.n)
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	checkInvariants(t, c, atomic.LoadInt64(&eventSum))
}
