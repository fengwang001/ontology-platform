package modbusrtu

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"
)

// simEvent is the outcome recorded by the naive, per-event reference model.
type simEvent struct {
	kind   EventKind
	frame  []byte
	reason error
	op     string // "B" byte or "P" poll
}

func (e simEvent) String() string {
	if e.kind == EventNone {
		return "none"
	}
	return fmt.Sprintf("%s/%s frame=% x", opName(e.op), summarize([]*Event{{Kind: e.kind, Frame: e.frame, Reason: e.reason}}), e.frame)
}

func opName(op string) string {
	if op == "P" {
		return "Poll"
	}
	return "Byte"
}

// naiveSimulator is a literal transcription of the specification, written
// independently of Receiver, used to cross-check randomized replays.
type naiveSimulator struct {
	t15, t35   int64
	slave      byte
	lastCall   int64
	haveCall   bool
	lastByte   int64
	haveByte   bool
	discarding bool
	buf        []byte
	c          Counters
}

func (s *naiveSimulator) checkClock(ns int64) bool {
	if s.haveCall && ns < s.lastCall {
		return false
	}
	s.lastCall, s.haveCall = ns, true
	return true
}

func (s *naiveSimulator) settle(ns int64) simEvent {
	frame := append([]byte(nil), s.buf...)
	s.buf = s.buf[:0]
	s.haveByte = false
	if len(frame) < 4 {
		s.c.TooShort++
		return simEvent{EventDropped, frame, ErrFrameTooShort, ""}
	}
	got := uint16(frame[len(frame)-2]) | uint16(frame[len(frame)-1])<<8
	if got != crc16Modbus(frame[:len(frame)-2]) {
		s.c.CRCErrors++
		return simEvent{EventDropped, frame, ErrCRC, ""}
	}
	if a := frame[0]; a != s.slave && a != 0 {
		s.c.Ignored++
		return simEvent{EventIgnored, frame, nil, ""}
	}
	s.c.Delivered++
	return simEvent{EventDelivered, frame, nil, ""}
}

func (s *naiveSimulator) byte(b byte, ns int64) (simEvent, error) {
	if !s.checkClock(ns) {
		return simEvent{}, ErrClockBackward
	}
	var ev simEvent
	gap := int64(0)
	if s.haveByte {
		gap = ns - s.lastByte
	}
	switch {
	case s.haveByte && gap >= s.t35:
		if !s.discarding {
			e := s.settle(ns)
			e.op = "B"
			ev = e
		}
		s.discarding = false
		s.buf = append(s.buf[:0], b)
	case s.discarding:
	case s.haveByte && gap > s.t15:
		s.discarding = true
		s.buf = s.buf[:0]
		s.c.IntraGap++
		ev = simEvent{EventDropped, nil, ErrIntraGap, "B"}
	default:
		if len(s.buf) >= maxFrameLen {
			s.discarding = true
			s.buf = s.buf[:0]
			s.c.TooLong++
			ev = simEvent{EventDropped, nil, ErrFrameTooLong, "B"}
		} else {
			s.buf = append(s.buf, b)
		}
	}
	s.lastByte, s.haveByte = ns, true
	return ev, nil
}

func (s *naiveSimulator) poll(ns int64) (simEvent, error) {
	if !s.checkClock(ns) {
		return simEvent{}, ErrClockBackward
	}
	if !s.haveByte || ns-s.lastByte < s.t35 {
		return simEvent{}, nil
	}
	if s.discarding {
		s.discarding = false
		s.buf = s.buf[:0]
		return simEvent{}, nil
	}
	e := s.settle(ns)
	e.op = "P"
	return e, nil
}

type op struct {
	isPoll bool
	b      byte
	t      int64
}

// TestNaiveSimulationReplay generates random op sequences covering every
// threshold neighborhood and replays them through both implementations.
func TestNaiveSimulationReplay(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261001, 42))
	bauds := []int{19200, 19199, 9600, 38400}

	for iter := 0; iter < 400; iter++ {
		baud := bauds[iter%len(bauds)]
		slave := byte(1 + rng.IntN(5))
		r, err := New(baud, slave)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t15, t35 := r.Thresholds()
		sim := &naiveSimulator{t15: t15.Nanoseconds(), t35: t35.Nanoseconds(), slave: slave, buf: make([]byte, 0, 300)}

		ops := generateOps(rng, t15.Nanoseconds(), t35.Nanoseconds(), slave)
		var simEvents []simEvent
		var gotEvents []*Event

		for _, o := range ops {
			at := time.Unix(0, o.t)
			if o.isPoll {
				se, serr := sim.poll(o.t)
				ge, gerr := r.Poll(at)
				if (serr != nil) != (gerr != nil) || serr != nil && !errorIs(serr, gerr) {
					t.Fatalf("iter=%d Poll t=%d errors sim=%v r=%v", iter, o.t, serr, gerr)
				}
				if se.kind != EventNone {
					simEvents = append(simEvents, se)
				}
				if ge != nil {
					gotEvents = append(gotEvents, ge)
				}
			} else {
				se, serr := sim.byte(o.b, o.t)
				ge, gerr := r.OnByte(o.b, at)
				if (serr != nil) != (gerr != nil) || serr != nil && !errorIs(serr, gerr) {
					t.Fatalf("iter=%d Byte t=%d errors sim=%v r=%v", iter, o.t, serr, gerr)
				}
				if se.kind != EventNone {
					simEvents = append(simEvents, se)
				}
				if ge != nil {
					gotEvents = append(gotEvents, ge)
				}
			}
		}

		if len(simEvents) != len(gotEvents) {
			t.Fatalf("iter=%d baud=%d event count sim=%d r=%d\nops=%s\nsim=%v\nr=%s",
				iter, baud, len(simEvents), len(gotEvents), dumpOps(ops), simEvents, summarize(gotEvents))
		}
		for i := range simEvents {
			se, ge := simEvents[i], gotEvents[i]
			if se.kind != ge.Kind || !bytes.Equal(se.frame, ge.Frame) || !reasonEqual(se.reason, ge.Reason) {
				t.Fatalf("iter=%d event[%d] mismatch:\nsim=%s\nr=%s\nops=%s",
					iter, i, se, summarize([]*Event{ge}), dumpOps(ops))
			}
		}
		if sim.c != r.Snapshot() {
			t.Fatalf("iter=%d counters mismatch sim=%+v r=%+v", iter, sim.c, r.Snapshot())
		}

		if iter < 5 {
			testLog.Printf("[sim] iter=%d baud=%d S=%d ops=%d events=%d", iter, baud, slave, len(ops), len(simEvents))
			for i := range simEvents {
				testLog.Printf("  out[%d] %s", i, simEvents[i])
			}
		}
	}
	testLog.Printf("[sim] 400 randomized replays (B in 19200/19199/9600/38400) match naive model: events and counters identical")
}

// TestReplayDeterminism replays an identical recorded byte/poll sequence twice
// and requires byte-identical event kinds, frames, reasons and counters.
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewPCG(77, 99))
	ops := generateOps(rng, 750_000, 1_750_000, 3)

	play := func() ([]*Event, Counters) {
		r, _ := New(19200, 3)
		var evs []*Event
		for _, o := range ops {
			at := time.Unix(0, o.t)
			var ev *Event
			var err error
			if o.isPoll {
				ev, err = r.Poll(at)
			} else {
				ev, err = r.OnByte(o.b, at)
			}
			if err != nil {
				// Backward ops are rejected identically both runs; skip.
				continue
			}
			if ev != nil {
				evs = append(evs, ev)
			}
		}
		return evs, r.Snapshot()
	}

	e1, c1 := play()
	e2, c2 := play()
	if c1 != c2 {
		t.Fatalf("counter drift: %+v vs %+v", c1, c2)
	}
	if len(e1) != len(e2) {
		t.Fatalf("event count drift %d vs %d", len(e1), len(e2))
	}
	for i := range e1 {
		if e1[i].Kind != e2[i].Kind || !bytes.Equal(e1[i].Frame, e2[i].Frame) ||
			!reasonEqual(e1[i].Reason, e2[i].Reason) {
			t.Fatalf("event[%d] drift:\n%+v\n%+v", i, e1[i], e2[i])
		}
	}
	testLog.Printf("[replay] identical %d-op sequence replayed twice -> %d identical events, counters %+v", len(ops), len(e1), c1)
}

func errorIs(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a == b
}

func reasonEqual(a, b error) bool {
	return (a == nil) == (b == nil) && (a == nil || a == b)
}

func dumpOps(ops []op) string {
	var buf bytes.Buffer
	for i, o := range ops {
		if i > 0 && i%12 == 0 {
			buf.WriteByte('\n')
		}
		if o.isPoll {
			fmt.Fprintf(&buf, "P@%d ", o.t)
		} else {
			fmt.Fprintf(&buf, "%02x@%d ", o.b, o.t)
		}
	}
	return buf.String()
}

// generateOps builds a non-decreasing timestamp stream. Gaps are drawn from
// the exact boundary neighborhoods t15-1, t15, t15+1, t35-1, t35, t35+1 plus
// tiny intra-frame steps; bursts may exceed 256 bytes and Polls are inserted.
func generateOps(rng *rand.Rand, t15, t35 int64, slave byte) []op {
	var ops []op
	now := int64(0)
	n := 3 + rng.IntN(20)

	// gapPool focuses on boundary decisions and discard-state recovery.
	gapPool := []int64{
		0, 1, t15 - 1, t15, t15 + 1,
		t35 - 1, t35, t35 + 1,
		t15 + (t35-t15)/2,
	}

	mkGoodFrame := func(addr byte, bodyLen int) []byte {
		p := make([]byte, bodyLen)
		for i := range p {
			p[i] = byte(rng.IntN(256))
		}
		p[0] = addr
		return withCRC(p...)
	}

	for i := 0; i < n; i++ {
		// Occasionally inject a backward call (always rejected, no mutation).
		if rng.IntN(10) == 0 && len(ops) > 0 {
			bt := now - 1 - int64(rng.IntN(5))
			if rng.IntN(2) == 0 {
				ops = append(ops, op{isPoll: true, t: bt})
			} else {
				ops = append(ops, op{b: byte(rng.IntN(256)), t: bt})
			}
		}

		var frame []byte
		switch rng.IntN(5) {
		case 0:
			frame = mkGoodFrame(slave, 1+rng.IntN(30)) // deliverable
		case 1:
			frame = mkGoodFrame(0, 1+rng.IntN(10)) // broadcast delivered
		case 2:
			frame = mkGoodFrame(byte(10+rng.IntN(200)), 1+rng.IntN(10)) // foreign ignored
		case 3:
			l := 1 + rng.IntN(3)
			frame = make([]byte, l) // too short
			for j := range frame {
				frame[j] = byte(rng.IntN(256))
			}
		default:
			frame = make([]byte, 1+rng.IntN(260)) // may hit 257 / bad CRC
			for j := range frame {
				frame[j] = byte(rng.IntN(256))
			}
			if rng.IntN(2) == 0 && len(frame) >= 4 {
				frame = withCRC(frame[:len(frame)-2]...) // good CRC, maybe >256
			}
		}

		for j, b := range frame {
			var g int64
			if j == 0 {
				if rng.IntN(3) == 0 {
					g = gapPool[rng.IntN(len(gapPool))]
				} else {
					g = gapPool[rng.IntN(3)] // tight intra spacing
				}
			} else {
				// Boundary gaps inside a frame trigger intra-gap violations.
				g = gapPool[rng.IntN(len(gapPool))]
			}
			now += g
			ops = append(ops, op{b: b, t: now})
		}

		if rng.IntN(2) == 0 {
			now += t35 + int64(rng.IntN(3)) - 1 // t35-1, t35, t35+1
			ops = append(ops, op{isPoll: true, t: now})
		} else {
			now += gapPool[rng.IntN(3)] // leave frame open into next burst
		}
	}
	// Final Poll always beyond t35 to flush anything pending.
	now += t35 + 1
	ops = append(ops, op{isPoll: true, t: now})
	return ops
}
