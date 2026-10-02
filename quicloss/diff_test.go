package quicloss

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// errorReason maps production errors to the reference model's reason tag.
func errorReason(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrInvalidSpace):
		return "invalid space"
	case errors.Is(err, ErrInvalidSize):
		return "invalid size"
	case errors.Is(err, ErrInvalidTime):
		return "invalid time"
	case errors.Is(err, ErrInvalidPN):
		return "negative pn"
	case errors.Is(err, ErrEmptyAck):
		return "empty ack"
	case errors.Is(err, ErrInvalidAckDelay):
		return "invalid ack delay"
	case errors.Is(err, ErrClockBackward):
		return "clock backward"
	case errors.Is(err, ErrSpaceDiscarded):
		return "space discarded"
	case errors.Is(err, ErrPNNotIncreasing):
		return "pn not increasing"
	case errors.Is(err, ErrAckedNeverSent):
		return "acked never sent"
	case errors.Is(err, ErrTimeoutEarly):
		return "timeout early"
	default:
		return ""
	}
}

func refSnapshot(m *refModel) Snapshot {
	conv := func(s *refSpace) SpaceSnapshot {
		out := SpaceSnapshot{
			LargestAcked: cpInt(s.la), LossTime: cpInt(s.lt),
			LargestSent: cpInt(s.maxSent), LastAe: cpInt(s.lastAe),
		}
		for pn, p := range s.pkts {
			out.Packets = append(out.Packets, Packet{
				PN: pn, SendTime: p.send, Size: p.size, AckElic: p.ae,
			})
		}
		sort.Slice(out.Packets, func(i, j int) bool { return out.Packets[i].PN < out.Packets[j].PN })
		return out
	}
	return Snapshot{
		Latest: m.latest, SRTT: m.srtt, RTTVar: m.rttvar,
		MinRTT: cpInt(m.minRtt), HasSample: m.hasSample, PTOCount: m.ptoCount,
		Confirmed: m.confirmed, LastNow: cpInt(m.lastNow),
		HDiscarded: m.sp[SpaceHandshake].dropped,
		H:          conv(m.sp[SpaceHandshake]), A: conv(m.sp[SpaceApp]),
	}
}

type gen struct {
	rng     *rand.Rand
	nextPN  map[Space]int64
	now     int64
	lastLog []string
}

// TestDifferentialRandom replays 2000 random call sequences through both
// the production Controller and the naive reference model, comparing every
// return value, error cause, timer and full internal state. Each sequence
// logs inputs, outputs and the rejection basis (visible with -v).
func TestDifferentialRandom(t *testing.T) {
	for iter := 0; iter < 2000; iter++ {
		runOne(t, iter)
	}
}

func runOne(t *testing.T, iter int) {
	rng := rand.New(rand.NewSource(int64(iter)*1_000_003 + 7))
	mad := []int64{0, 1, 25, 100, 10000}[rng.Intn(5)]
	c, err := New(mad)
	if err != nil {
		t.Fatal(err)
	}
	m := newRefModel(mad)
	g := &gen{rng: rng, nextPN: map[Space]int64{SpaceHandshake: 0, SpaceApp: 0}, now: 0}
	var log []string
	log = append(log, fmt.Sprintf("iter=%d maxAckDelay=%d", iter, mad))

	steps := 30 + rng.Intn(70)
	for step := 0; step < steps; step++ {
		switch rng.Intn(7) {
		case 0:
			g.stepSend(t, c, m, &log)
		case 1:
			g.stepAck(t, c, m, &log)
		case 2:
			g.stepDetect(t, c, m, &log)
		case 3:
			g.stepTimer(t, c, m, &log)
		case 4:
			g.stepTimeout(t, c, m, &log)
		case 5:
			g.stepConfirm(t, c, m, &log)
		default:
			g.stepInvalidSend(t, c, m, &log)
		}
	}
	t.Log(strings.Join(log, "\n"))
}

func (g *gen) pickSpace() Space {
	if g.rng.Intn(2) == 0 {
		return SpaceHandshake
	}
	return SpaceApp
}

// advanceNow returns a non-decreasing now, sometimes jumping far.
func (g *gen) advanceNow() int64 {
	switch g.rng.Intn(5) {
	case 0:
	case 1:
		g.now += int64(g.rng.Intn(10))
	case 2:
		g.now += int64(g.rng.Intn(1000))
	case 3:
		g.now += int64(g.rng.Intn(100000))
	default:
		g.now += int64(g.rng.Intn(5))
	}
	if g.now > 1_000_000_000_000 {
		g.now = 1_000_000_000_000
	}
	return g.now
}

func (g *gen) maybeBadTime() (int64, bool) {
	if g.now > 0 && g.rng.Intn(6) == 0 {
		return g.now - 1 - int64(g.rng.Intn(10)), true
	}
	return g.advanceNow(), false
}

func cmpSnap(t *testing.T, c *Controller, m *refModel, log *[]string) {
	t.Helper()
	c.mu.Lock()
	got := c.snapshotLocked()
	c.mu.Unlock()
	want := refSnapshot(m)
	if !reflect.DeepEqual(got, want) {
		*log = append(*log, fmt.Sprintf("STATE MISMATCH:\n got=%+v\nwant=%+v", formatSnap(got), formatSnap(want)))
		t.Fatalf("state mismatch\n%s", strings.Join(*log, "\n"))
	}
}

func formatSnap(s Snapshot) string {
	return fmt.Sprintf("{latest=%d srtt=%d rttvar=%d min=%v sample=%v count=%d conf=%v H=%s A=%s}",
		s.Latest, s.SRTT, s.RTTVar, s.MinRTT, s.HasSample, s.PTOCount, s.Confirmed,
		formatSpace(s.H), formatSpace(s.A))
}

func formatSpace(s SpaceSnapshot) string {
	pns := make([]int64, 0, len(s.Packets))
	for _, p := range s.Packets {
		pns = append(pns, p.PN)
	}
	return fmt.Sprintf("{pkts=%v la=%v lt=%v max=%v ae=%v}", pns, s.LargestAcked, s.LossTime, s.LargestSent, s.LastAe)
}

func (g *gen) stepSend(t *testing.T, c *Controller, m *refModel, log *[]string) {
	sp := g.pickSpace()
	pn := g.nextPN[sp]
	g.nextPN[sp]++
	size := 1 + g.rng.Intn(65535)
	ae := g.rng.Intn(5) != 0
	now := g.advanceNow()

	err1 := c.Send(now, sp, pn, size, ae)
	o2 := m.send(now, sp, pn, size, ae)
	*log = append(*log, fmt.Sprintf("Send now=%d sp=%s pn=%d size=%d ae=%v -> ok=%v reason=%q",
		now, sp, pn, size, ae, err1 == nil, errorReason(err1)))
	if (err1 == nil) != o2.ok || errorReason(err1) != o2.reason {
		t.Fatalf("Send mismatch: %v vs %+v\n%s", err1, o2, strings.Join(*log, "\n"))
	}
	if err1 == nil {
		g.now = now
	}
	cmpSnap(t, c, m, log)
}

func (g *gen) stepInvalidSend(t *testing.T, c *Controller, m *refModel, log *[]string) {
	// Deliberately illegal: bad size, bad pn, or repeated pn.
	now := g.advanceNow()
	sp := g.pickSpace()
	switch g.rng.Intn(3) {
	case 0:
		size := []int{0, -1, 65536, 1 << 20}[g.rng.Intn(4)]
		pn := g.nextPN[sp]
		g.nextPN[sp]++
		err1 := c.Send(now, sp, pn, size, true)
		o2 := m.send(now, sp, pn, size, true)
		*log = append(*log, fmt.Sprintf("Send(badsize) now=%d sp=%s pn=%d size=%d -> %q", now, sp, pn, size, errorReason(err1)))
		match(t, err1, o2, log)
	case 1:
		pn := int64(-1 - g.rng.Intn(5))
		err1 := c.Send(now, sp, pn, 10, true)
		o2 := m.send(now, sp, pn, 10, true)
		*log = append(*log, fmt.Sprintf("Send(badpn) now=%d sp=%s pn=%d -> %q", now, sp, pn, errorReason(err1)))
		match(t, err1, o2, log)
	default:
		pn := g.nextPN[sp] - 1
		if pn < 0 {
			pn = 0
		}
		err1 := c.Send(now, sp, pn, 10, true)
		o2 := m.send(now, sp, pn, 10, true)
		*log = append(*log, fmt.Sprintf("Send(repeat) now=%d sp=%s pn=%d -> %q", now, sp, pn, errorReason(err1)))
		match(t, err1, o2, log)
	}
	cmpSnap(t, c, m, log)
}

func match(t *testing.T, err1 error, o2 outcome, log *[]string) {
	t.Helper()
	if (err1 == nil) != o2.ok || errorReason(err1) != o2.reason {
		t.Fatalf("mismatch: %v(%q) vs %+v\n%s", err1, errorReason(err1), o2, strings.Join(*log, "\n"))
	}
}

func (g *gen) stepAck(t *testing.T, c *Controller, m *refModel, log *[]string) {
	now := g.advanceNow()
	sp := g.pickSpace()

	var pns []int64
	switch g.rng.Intn(4) {
	case 0:
		// Possibly empty (rejected by both).
		if g.rng.Intn(2) == 0 {
			pns = nil
		}
	case 1:
		// Possibly never-sent number.
		pns = append(pns, int64(g.rng.Intn(20)))
	default:
		maxSent := g.nextPN[sp] - 1
		n := 1 + g.rng.Intn(4)
		for i := 0; i < n; i++ {
			if maxSent >= 0 {
				pns = append(pns, int64(g.rng.Intn(int(maxSent)+1)))
			}
		}
		// Occasionally a never-sent in-range or above-range number.
		if g.rng.Intn(4) == 0 {
			pns = append(pns, maxSent+1+int64(g.rng.Intn(3)))
		}
	}
	if g.rng.Intn(8) == 0 {
		pns = append(pns, int64(-1-g.rng.Intn(3)))
	}
	var delay int64
	switch g.rng.Intn(6) {
	case 0:
		delay = int64(-1)
	case 1:
		delay = 0
	default:
		delay = int64(g.rng.Intn(60))
	}

	lost1, err1 := c.Ack(now, sp, pns, delay)
	o2 := m.ack(now, sp, pns, delay)
	*log = append(*log, fmt.Sprintf("Ack now=%d sp=%s pns=%v delay=%d -> ok=%v reason=%q lost=%v",
		now, sp, pns, delay, err1 == nil, errorReason(err1), normLost(lost1)))
	if (err1 == nil) != o2.ok || errorReason(err1) != o2.reason {
		t.Fatalf("Ack mismatch: %v vs %+v\n%s", err1, o2, strings.Join(*log, "\n"))
	}
	if err1 == nil && !reflect.DeepEqual(normLost(lost1), normLost(o2.lost)) {
		t.Fatalf("Ack lost mismatch: %v vs %v\n%s", lost1, o2.lost, strings.Join(*log, "\n"))
	}
	cmpSnap(t, c, m, log)
}

func normLost(in []int64) []int64 {
	if len(in) == 0 {
		return []int64{}
	}
	return append([]int64{}, in...)
}

func (g *gen) stepDetect(t *testing.T, c *Controller, m *refModel, log *[]string) {
	now := g.advanceNow()
	sp := g.pickSpace()
	lost1, err1 := c.Detect(sp, now)
	o2 := m.detectCall(sp, now)
	*log = append(*log, fmt.Sprintf("Detect now=%d sp=%s -> ok=%v reason=%q lost=%v",
		now, sp, err1 == nil, errorReason(err1), normLost(lost1)))
	if (err1 == nil) != o2.ok || errorReason(err1) != o2.reason {
		t.Fatalf("Detect mismatch: %v vs %+v\n%s", err1, o2, strings.Join(*log, "\n"))
	}
	if err1 == nil && !reflect.DeepEqual(normLost(lost1), normLost(o2.lost)) {
		t.Fatalf("Detect lost mismatch: %v vs %v", lost1, o2.lost)
	}
	cmpSnap(t, c, m, log)
}

func timerEq(a, b *TimerInfo) bool {
	return reflect.DeepEqual(a, b)
}

func (g *gen) stepTimer(t *testing.T, c *Controller, m *refModel, log *[]string) {
	t1 := c.Timer()
	t2 := m.timer()
	if !timerEq(t1, t2) {
		t.Fatalf("Timer mismatch: %+v vs %+v\n%s", t1, t2, strings.Join(*log, "\n"))
	}
	if t1 != nil {
		*log = append(*log, fmt.Sprintf("Timer -> (%d,%s,%s)", t1.Time, t1.Mode, t1.Space))
	} else {
		*log = append(*log, "Timer -> none")
	}
}

func (g *gen) stepTimeout(t *testing.T, c *Controller, m *refModel, log *[]string) {
	t2 := m.timer()
	var now int64
	switch g.rng.Intn(3) {
	case 0:
		now = g.advanceNow() // may be early
	default:
		now = g.advanceNow()
		if t2 != nil && now < t2.Time {
			now = t2.Time + int64(g.rng.Intn(3))
		}
	}
	if now > 1_000_000_000_000 {
		now = 1_000_000_000_000
	}
	r1, err1 := c.OnTimeout(now)
	o2 := m.timeout(now)
	*log = append(*log, fmt.Sprintf("OnTimeout now=%d -> ok=%v reason=%q", now, err1 == nil, errorReason(err1)))
	if (err1 == nil) != o2.ok || errorReason(err1) != o2.reason {
		t.Fatalf("Timeout mismatch: %v vs %+v\n%s", err1, o2, strings.Join(*log, "\n"))
	}
	if err1 == nil {
		if !reflect.DeepEqual(normLost(r1.LostPN), normLost(o2.timeout.LostPN)) ||
			r1.Mode != o2.timeout.Mode || r1.Space != o2.timeout.Space ||
			!reflect.DeepEqual(r1.Probe, o2.timeout.Probe) {
			t.Fatalf("Timeout result mismatch: %+v vs %+v", r1, o2.timeout)
		}
		switch r1.Mode {
		case ModeLoss:
			*log = append(*log, fmt.Sprintf("  basis: Loss detection sp=%s lost=%v", r1.Space, r1.LostPN))
		case ModePTO:
			*log = append(*log, fmt.Sprintf("  basis: PTO probe sp=%s count=%d", r1.Probe.Space, r1.Probe.Count))
		}
	} else {
		*log = append(*log, "  basis: rejected, no state change")
	}
	cmpSnap(t, c, m, log)
}

func (g *gen) stepConfirm(t *testing.T, c *Controller, m *refModel, log *[]string) {
	now := g.advanceNow()
	err1 := c.HandshakeConfirmed(now)
	o2 := m.confirm(now)
	*log = append(*log, fmt.Sprintf("HandshakeConfirmed now=%d -> ok=%v reason=%q", now, err1 == nil, errorReason(err1)))
	if (err1 == nil) != o2.ok || errorReason(err1) != o2.reason {
		t.Fatalf("Confirm mismatch: %v vs %+v", err1, o2)
	}
	cmpSnap(t, c, m, log)
}
