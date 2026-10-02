package tricolor

import (
	"errors"
	"sync"
	"testing"
)

func baseParams() Params {
	return Params{CIR: 10, CBS: 100, EBS: 50, W: 100, K: 3, Pn: 50}
}

func mustNew(t *testing.T, p Params) *Marker {
	t.Helper()
	m, err := New(p)
	if err != nil {
		t.Fatalf("New(%+v): %v", p, err)
	}
	return m
}

func mustMark(t *testing.T, m *Marker, now int64, in Input, b int64) (Color, bool) {
	t.Helper()
	c, pen, err := m.Mark(now, in, b)
	if err != nil {
		t.Fatalf("Mark(%d, %v, %d): %v", now, in, b, err)
	}
	return c, pen
}

func checkBuckets(t *testing.T, m *Marker, tc, te int64) {
	t.Helper()
	s := m.Snapshot()
	if s.Tc != tc || s.Te != te {
		t.Fatalf("buckets = (Tc %d, Te %d), want (Tc %d, Te %d)", s.Tc, s.Te, tc, te)
	}
}

// The worked example from the specification.
func TestSpecBasicExample(t *testing.T) {
	m := mustNew(t, baseParams())
	checkBuckets(t, m, 100, 50)

	if c, _ := mustMark(t, m, 0, InGreen, 100); c != Green {
		t.Fatalf("Mark(0,Green,100) = %v, want Green", c)
	}
	checkBuckets(t, m, 0, 50)

	if c, _ := mustMark(t, m, 0, InGreen, 60); c != Red {
		t.Fatalf("Mark(0,Green,60) = %v, want Red", c)
	}
	if q := m.Snapshot().RedQueue; len(q) != 1 || q[0] != 0 {
		t.Fatalf("red queue = %v, want [0]", q)
	}

	if c, _ := mustMark(t, m, 0, InGreen, 50); c != Yellow {
		t.Fatalf("Mark(0,Green,50) = %v, want Yellow", c)
	}
	checkBuckets(t, m, 0, 0)

	if c, _ := mustMark(t, m, 10, InGreen, 80); c != Green {
		t.Fatalf("Mark(10,Green,80) = %v, want Green", c)
	}
	checkBuckets(t, m, 20, 0)

	if c, _ := mustMark(t, m, 30, InYellow, 30); c != Yellow {
		t.Fatalf("Mark(30,Yellow,30) = %v, want Yellow", c)
	}
	checkBuckets(t, m, 100, 20)

	// Yellow input never touches the committed bucket.
	if c, _ := mustMark(t, m, 30, InYellow, 25); c != Red {
		t.Fatalf("Mark(30,Yellow,25) = %v, want Red", c)
	}
	checkBuckets(t, m, 100, 20)

	if c, _ := mustMark(t, m, 30, InGreen, 25); c != Green {
		t.Fatalf("Mark(30,Green,25) = %v, want Green", c)
	}
	checkBuckets(t, m, 75, 20)
}

// K=2: rate-reds at t=0 and t=99 trigger a penalty with until=149.
func TestPenaltyTrigger(t *testing.T) {
	p := baseParams()
	p.K = 2
	m := mustNew(t, p)

	// Drain both buckets so Green packets are rate-red.
	mustMark(t, m, 0, InGreen, 100) // Green, Tc=0
	mustMark(t, m, 0, InGreen, 50)  // Yellow, Te=0

	// b=151 exceeds both bucket depths, so it is Red at any time.
	if c, _ := mustMark(t, m, 0, InGreen, 151); c != Red {
		t.Fatalf("first rate-red = %v, want Red", c)
	}
	if s := m.Snapshot(); s.Until != 0 {
		t.Fatalf("until = %d after one red, want 0 (K=2 not reached)", s.Until)
	}

	// t=99: 0+W=100 > 99, record at 0 survives; queue {0,99} reaches K.
	if c, _ := mustMark(t, m, 99, InGreen, 151); c != Red {
		t.Fatalf("second rate-red = %v, want Red", c)
	}
	s := m.Snapshot()
	if s.Until != 149 || s.LastUntil != 149 || s.S != 0 {
		t.Fatalf("until=%d lastUntil=%d s=%d, want 149/149/0", s.Until, s.LastUntil, s.S)
	}
	if len(s.RedQueue) != 0 {
		t.Fatalf("red queue = %v after trigger, want empty", s.RedQueue)
	}

	// Inside the penalty every input color is Red, tokens untouched,
	// no red is recorded.
	for _, in := range []Input{InGreen, InYellow, InRed, InBlind} {
		c, pen := mustMark(t, m, 100, in, 1)
		if c != Red || !pen {
			t.Fatalf("Mark(100,%v,1) = (%v,%v), want (Red,true)", in, c, pen)
		}
	}
	after := m.Snapshot()
	if after.Tc != after.CBS || after.Te != after.EBS {
		t.Fatalf("penalty consumed tokens: Tc=%d Te=%d, want %d/%d",
			after.Tc, after.Te, after.CBS, after.EBS)
	}
	if len(after.RedQueue) != 0 {
		t.Fatalf("red queue = %v during penalty, want empty", after.RedQueue)
	}

	// now=149 is no longer penalized.
	if _, pen := mustMark(t, m, 149, InRed, 1); pen {
		t.Fatalf("still penalized at now=149")
	}
}

// A second rate-red at t=100 evicts the t=0 record (0+W <= 100), so the
// queue holds a single entry and no penalty triggers.
func TestPenaltyWindowEvictionBoundary(t *testing.T) {
	p := baseParams()
	p.K = 2
	m := mustNew(t, p)

	mustMark(t, m, 0, InGreen, 100)
	mustMark(t, m, 0, InGreen, 50)
	mustMark(t, m, 0, InGreen, 151) // red at 0

	if c, _ := mustMark(t, m, 100, InGreen, 151); c != Red {
		t.Fatalf("rate-red at 100 = %v, want Red", c)
	}
	s := m.Snapshot()
	if s.Until != 0 {
		t.Fatalf("until = %d, want 0 (record at 0 evicted at t=100)", s.Until)
	}
	if q := s.RedQueue; len(q) != 1 || q[0] != 100 {
		t.Fatalf("red queue = %v, want [100]", q)
	}
}

// forceRed issues a packet that is always rate-red (b > CBS+EBS).
func forceRed(t *testing.T, m *Marker, now int64) {
	t.Helper()
	c, pen := mustMark(t, m, now, InGreen, 151)
	if c != Red || pen {
		t.Fatalf("forceRed(%d) = (%v,%v), want (Red,false)", now, c, pen)
	}
}

func triggerPair(t *testing.T, m *Marker, t1, t2 int64) {
	t.Helper()
	forceRed(t, m, t1)
	forceRed(t, m, t2)
}

// Escalation example from the specification: s grows while consecutive
// penalties start within one window of the previous penalty end, and
// resets otherwise.
func TestPenaltyEscalation(t *testing.T) {
	p := baseParams()
	p.K = 2
	m := mustNew(t, p)

	triggerPair(t, m, 0, 99) // until=149, lastUntil=149, s=0
	if s := m.Snapshot(); s.Until != 149 || s.S != 0 {
		t.Fatalf("after first trigger: until=%d s=%d, want 149/0", s.Until, s.S)
	}

	triggerPair(t, m, 150, 160) // 160-149=11 < W=100 -> s=1, dur=100
	if s := m.Snapshot(); s.Until != 260 || s.LastUntil != 260 || s.S != 1 {
		t.Fatalf("after second trigger: until=%d lastUntil=%d s=%d, want 260/260/1",
			s.Until, s.LastUntil, s.S)
	}

	triggerPair(t, m, 400, 410) // 410-260=150 >= W -> s resets to 0, dur=50
	if s := m.Snapshot(); s.Until != 460 || s.S != 0 {
		t.Fatalf("after third trigger: until=%d s=%d, want 460/0", s.Until, s.S)
	}
}

// now == lastUntil+W resets the streak (equality does not count);
// now == lastUntil+W-1 increments it.
func TestPenaltyEscalationBoundary(t *testing.T) {
	setup := func(t *testing.T) *Marker {
		p := baseParams()
		p.K = 2
		m := mustNew(t, p)
		triggerPair(t, m, 0, 99)    // lastUntil=149
		triggerPair(t, m, 150, 160) // lastUntil=260, s=1
		return m
	}

	m := setup(t)
	triggerPair(t, m, 350, 360) // trigger at 360 = 260+100 -> s=0, dur=50
	if s := m.Snapshot(); s.Until != 410 || s.S != 0 {
		t.Fatalf("trigger at lastUntil+W: until=%d s=%d, want 410/0", s.Until, s.S)
	}

	m = setup(t)
	triggerPair(t, m, 358, 359) // trigger at 359 = 260+99 -> s=2, dur=200
	if s := m.Snapshot(); s.Until != 559 || s.S != 2 {
		t.Fatalf("trigger at lastUntil+W-1: until=%d s=%d, want 559/2", s.Until, s.S)
	}
}

// The streak counter is capped at 3, so the longest penalty is 8*Pn.
func TestPenaltyEscalationCap(t *testing.T) {
	p := baseParams()
	p.K = 2
	m := mustNew(t, p)

	triggerPair(t, m, 0, 99)    // s=0, until=149
	triggerPair(t, m, 150, 160) // s=1, until=260
	triggerPair(t, m, 270, 280) // s=2, until=480
	triggerPair(t, m, 490, 500) // s=3, until=900
	triggerPair(t, m, 910, 920) // s stays 3, dur=400, until=1320
	if s := m.Snapshot(); s.Until != 1320 || s.S != 3 {
		t.Fatalf("capped streak: until=%d s=%d, want 1320/3", s.Until, s.S)
	}
}

// Red input never consumes tokens and never records a red.
func TestRedInputNoRecord(t *testing.T) {
	p := baseParams()
	p.K = 1 // a single recorded red would trigger
	m := mustNew(t, p)

	for i := 0; i < 5; i++ {
		c, pen := mustMark(t, m, 0, InRed, 10)
		if c != Red || pen {
			t.Fatalf("Mark(0,Red,10) = (%v,%v), want (Red,false)", c, pen)
		}
	}
	s := m.Snapshot()
	if len(s.RedQueue) != 0 || s.Until != 0 {
		t.Fatalf("red input recorded: queue=%v until=%d", s.RedQueue, s.Until)
	}
	checkBuckets(t, m, 100, 50)
}

// Blind is treated exactly like Green: identical sequences of Green and
// Blind inputs produce identical outputs and bucket states.
func TestBlindEqualsGreen(t *testing.T) {
	mg := mustNew(t, baseParams())
	mb := mustNew(t, baseParams())

	ops := []struct {
		now int64
		b   int64
	}{
		{0, 100}, {0, 60}, {0, 50}, {10, 80}, {30, 30}, {30, 25}, {50, 200},
	}
	for _, op := range ops {
		cg, pg := mustMark(t, mg, op.now, InGreen, op.b)
		cb, pb := mustMark(t, mb, op.now, InBlind, op.b)
		if cg != cb || pg != pb {
			t.Fatalf("op %+v: Green=(%v,%v) Blind=(%v,%v)", op, cg, pg, cb, pb)
		}
	}
	sg, sb := mg.Snapshot(), mb.Snapshot()
	if sg.Tc != sb.Tc || sg.Te != sb.Te || sg.Until != sb.Until {
		t.Fatalf("states diverge: Green %+v vs Blind %+v", sg, sb)
	}
}

// A packet larger than both buckets is always Red and consumes nothing.
func TestOversizePacketAlwaysRed(t *testing.T) {
	m := mustNew(t, baseParams())
	for _, now := range []int64{0, 5, 1000} {
		c, _ := mustMark(t, m, now, InGreen, 151)
		if c != Red {
			t.Fatalf("Mark(%d,Green,151) = %v, want Red", now, c)
		}
		checkBuckets(t, m, 100, 50)
	}
}

// Reconfigure refills with the old parameters, then truncates both
// buckets; truncated committed tokens are discarded, not spilled into
// the excess bucket.
func TestReconfigureTruncates(t *testing.T) {
	m := mustNew(t, baseParams())
	mustMark(t, m, 0, InGreen, 50) // Tc=50

	// Refill at t=10 with old params: Tc=50+100=150 -> capped 100,
	// overflow 50 -> Te=min(50,50+50)=50. Then clamp: Tc=40, Te=20.
	if err := m.Reconfigure(10, 10, 40, 20); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	checkBuckets(t, m, 40, 20)

	s := m.Snapshot()
	if s.CIR != 10 || s.CBS != 40 || s.EBS != 20 {
		t.Fatalf("params = %+v, want CIR=10 CBS=40 EBS=20", s)
	}

	// New depths apply to subsequent refills.
	mustMark(t, m, 20, InRed, 1)
	checkBuckets(t, m, 40, 20) // refill capped at the new depths
}

// Reconfigure keeps penalty state and the red queue intact.
func TestReconfigureKeepsPenaltyState(t *testing.T) {
	p := baseParams()
	p.K = 2
	m := mustNew(t, p)
	triggerPair(t, m, 0, 99) // until=149

	if err := m.Reconfigure(100, 5, 200, 300); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	s := m.Snapshot()
	if s.Until != 149 || s.LastUntil != 149 {
		t.Fatalf("penalty state lost: until=%d lastUntil=%d", s.Until, s.LastUntil)
	}
	if _, pen := mustMark(t, m, 100, InGreen, 1); !pen {
		t.Fatalf("not penalized at 100 after Reconfigure")
	}
}

// When both buckets are full the overflow is discarded.
func TestOverflowDroppedWhenBothFull(t *testing.T) {
	m := mustNew(t, baseParams())
	// Refill of 100*10=1000 tokens: Tc stays 100, Te stays 50.
	mustMark(t, m, 100, InRed, 1)
	checkBuckets(t, m, 100, 50)
}

// Invalid constructor parameters are rejected with ErrInvalidParam.
func TestInvalidParams(t *testing.T) {
	good := baseParams()
	bads := []Params{}
	for _, mutate := range []func(*Params){
		func(p *Params) { p.CIR = 0 },
		func(p *Params) { p.CIR = 1_000_001 },
		func(p *Params) { p.CBS = 0 },
		func(p *Params) { p.CBS = 1_000_000_001 },
		func(p *Params) { p.EBS = -1 },
		func(p *Params) { p.EBS = 1_000_000_001 },
		func(p *Params) { p.W = 0 },
		func(p *Params) { p.W = 1_000_001 },
		func(p *Params) { p.K = 0 },
		func(p *Params) { p.K = 1001 },
		func(p *Params) { p.Pn = 0 },
		func(p *Params) { p.Pn = 1_000_001 },
	} {
		p := good
		mutate(&p)
		bads = append(bads, p)
	}
	for _, p := range bads {
		if _, err := New(p); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New(%+v) err = %v, want ErrInvalidParam", p, err)
		}
	}
	if _, err := New(good); err != nil {
		t.Fatalf("New(valid) err = %v", err)
	}
}

// Rejected calls are distinguishable, parameter errors take precedence
// over clock regression, and rejections leave the state untouched.
func TestRejectedCallsAreSideEffectFree(t *testing.T) {
	m := mustNew(t, baseParams())
	mustMark(t, m, 10, InGreen, 10)
	before := m.Snapshot()

	assertInvalid := func(err error, what string) {
		t.Helper()
		if !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("%s err = %v, want ErrInvalidParam", what, err)
		}
	}
	assertBackward := func(err error, what string) {
		t.Helper()
		if !errors.Is(err, ErrClockBackward) {
			t.Fatalf("%s err = %v, want ErrClockBackward", what, err)
		}
	}

	_, _, err := m.Mark(1_000_000_000_001, InGreen, 1)
	assertInvalid(err, "Mark now out of range")
	_, _, err = m.Mark(20, Input(9), 1)
	assertInvalid(err, "Mark bad color")
	_, _, err = m.Mark(20, InGreen, 0)
	assertInvalid(err, "Mark b=0")
	_, _, err = m.Mark(20, InGreen, 1_000_001)
	assertInvalid(err, "Mark b too large")
	assertBackward(func() error { _, _, e := m.Mark(5, InGreen, 1); return e }(), "Mark clock backward")

	// Parameter errors win over clock regression.
	_, _, err = m.Mark(5, InGreen, 0)
	assertInvalid(err, "Mark bad b and backward clock")
	assertInvalid(m.Reconfigure(5, 0, 100, 50), "Reconfigure bad CIR and backward clock")

	assertInvalid(m.Reconfigure(20, 0, 100, 50), "Reconfigure CIR=0")
	assertInvalid(m.Reconfigure(20, 10, 0, 50), "Reconfigure CBS=0")
	assertInvalid(m.Reconfigure(20, 10, 100, -1), "Reconfigure EBS=-1")
	assertBackward(m.Reconfigure(5, 10, 100, 50), "Reconfigure clock backward")

	after := m.Snapshot()
	if after.Tc != before.Tc || after.Te != before.Te || after.Last != before.Last ||
		after.Until != before.Until || len(after.RedQueue) != len(before.RedQueue) {
		t.Fatalf("rejected calls mutated state: %+v -> %+v", before, after)
	}
}

// Every packet's bytes are accounted to exactly one output color.
func TestStatsConservation(t *testing.T) {
	m := mustNew(t, baseParams())
	var totalPackets, totalBytes int64
	ops := []struct {
		now int64
		in  Input
		b   int64
	}{
		{0, InGreen, 100}, {0, InGreen, 60}, {0, InGreen, 50},
		{10, InYellow, 80}, {30, InRed, 30}, {30, InBlind, 25},
		{100, InGreen, 151}, {200, InYellow, 10},
	}
	for _, op := range ops {
		mustMark(t, m, op.now, op.in, op.b)
		totalPackets++
		totalBytes += op.b
	}
	st := m.Stats()
	var packets, bytes int64
	for c := Green; c <= Red; c++ {
		packets += st.Packets[c]
		bytes += st.Bytes[c]
	}
	if packets != totalPackets || bytes != totalBytes {
		t.Fatalf("stats = %d packets/%d bytes, want %d/%d",
			packets, bytes, totalPackets, totalBytes)
	}
}

// Concurrent callers behave as some serial order: bucket invariants and
// byte conservation hold under -race.
func TestConcurrentAccess(t *testing.T) {
	m := mustNew(t, Params{CIR: 100, CBS: 1000, EBS: 500, W: 50, K: 3, Pn: 20})

	var wg sync.WaitGroup
	var byteMu sync.Mutex
	var markedPackets, markedBytes int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				now := int64(i)
				in := Input((g + i) % 4)
				b := int64(1 + (g*7+i)%50)
				if _, _, err := m.Mark(now, in, b); err == nil {
					byteMu.Lock()
					markedPackets++
					markedBytes += b
					byteMu.Unlock()
				}
				if i%97 == 0 {
					_ = m.Reconfigure(now, 100, 1000, 500)
				}
				_ = m.Snapshot()
				_ = m.Stats()
			}
		}(g)
	}
	wg.Wait()

	s := m.Snapshot()
	if s.Tc < 0 || s.Tc > s.CBS || s.Te < 0 || s.Te > s.EBS {
		t.Fatalf("bucket invariant violated: %+v", s)
	}
	st := m.Stats()
	var packets, bytes int64
	for c := Green; c <= Red; c++ {
		packets += st.Packets[c]
		bytes += st.Bytes[c]
	}
	if bytes != markedBytes {
		t.Fatalf("byte conservation: stats=%d marked=%d", bytes, markedBytes)
	}
	if packets != markedPackets {
		t.Fatalf("packets = %d, want %d", packets, markedPackets)
	}
}
