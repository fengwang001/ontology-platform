package modbusrtu

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// simEvent mirrors Event for the naive simulator.
type simEvent struct {
	frame     []byte
	delivered bool
	ignored   bool
	reason    error
}

func (e simEvent) present() bool {
	return e.reason != nil || e.delivered || e.ignored
}

// naiveSim is a literal, step-by-step reimplementation of the specification
// used as an independent oracle in differential tests.
type naiveSim struct {
	t15, t35   time.Duration
	addr       int
	started    bool
	lastCall   time.Time
	inFrame    bool
	discarding bool
	lastByte   time.Time
	buf        []byte

	delivered, ignored, intraGap, tooLong, tooShort, crcErrs int

	log *bytes.Buffer
}

func newNaiveSim(baud, addr int, log *bytes.Buffer) *naiveSim {
	s := &naiveSim{addr: addr, log: log, buf: make([]byte, 0, 257)}
	if baud >= 19200 {
		s.t15 = 750 * time.Microsecond
		s.t35 = 1750 * time.Microsecond
	} else {
		s.t15 = time.Duration(33_000_000_000/int64(2*baud)) * time.Nanosecond
		s.t35 = time.Duration(77_000_000_000/int64(2*baud)) * time.Nanosecond
	}
	return s
}

func (s *naiveSim) checkClock(t time.Time) bool {
	if s.started && t.Before(s.lastCall) {
		fmt.Fprintf(s.log, "input t=%d -> ErrClockBackward (lastCall=%d), no state change\n",
			t.UnixNano(), s.lastCall.UnixNano())
		return false
	}
	s.started = true
	s.lastCall = t
	return true
}

func (s *naiveSim) settle(frame []byte) simEvent {
	ev := simEvent{frame: append([]byte(nil), frame...)}
	if len(frame) < 4 {
		s.tooShort++
		ev.reason = ErrFrameTooShort
		fmt.Fprintf(s.log, "settle len=%d -> ErrFrameTooShort\n", len(frame))
		return ev
	}
	got := uint16(frame[len(frame)-2]) | uint16(frame[len(frame)-1])<<8
	if crc16(frame[:len(frame)-2]) != got {
		s.crcErrs++
		ev.reason = ErrCRC
		fmt.Fprintf(s.log, "settle len=%d addr=%d -> ErrCRC\n", len(frame), frame[0])
		return ev
	}
	if frame[0] != byte(s.addr) && frame[0] != 0 {
		s.ignored++
		ev.ignored = true
		fmt.Fprintf(s.log, "settle len=%d addr=%d -> Ignored\n", len(frame), frame[0])
		return ev
	}
	s.delivered++
	ev.delivered = true
	fmt.Fprintf(s.log, "settle len=%d addr=%d -> Delivered\n", len(frame), frame[0])
	return ev
}

func (s *naiveSim) onByte(b byte, t time.Time) (simEvent, bool) {
	if !s.checkClock(t) {
		return simEvent{}, false
	}
	if s.inFrame {
		g := t.Sub(s.lastByte)
		if g >= s.t35 {
			var out simEvent
			if !s.discarding {
				out = s.settle(s.buf)
			}
			s.inFrame = true
			s.discarding = false
			s.buf = append(s.buf[:0], b)
			s.lastByte = t
			fmt.Fprintf(s.log, "byte %#02x t=%d gap=%d >=t35=%d: boundary, new frame\n",
				b, t.UnixNano(), g, s.t35)
			return out, true
		}
		if s.discarding {
			s.lastByte = t
			fmt.Fprintf(s.log, "byte %#02x t=%d gap=%d: discarded, ignored\n",
				b, t.UnixNano(), g)
			return simEvent{}, true
		}
		if g > s.t15 {
			s.discarding = true
			s.lastByte = t
			s.intraGap++
			fmt.Fprintf(s.log, "byte %#02x t=%d gap=%d in (t15=%d,t35=%d): ErrIntraFrameGap\n",
				b, t.UnixNano(), g, s.t15, s.t35)
			return simEvent{reason: ErrIntraFrameGap}, true
		}
	}

	s.lastByte = t
	if !s.inFrame {
		s.inFrame = true
		s.buf = append(s.buf[:0], b)
		fmt.Fprintf(s.log, "byte %#02x t=%d: start frame\n", b, t.UnixNano())
		return simEvent{}, true
	}

	if len(s.buf) >= 256 {
		s.discarding = true
		s.tooLong++
		fmt.Fprintf(s.log, "byte %#02x t=%d: 257th byte -> ErrFrameTooLong\n", b, t.UnixNano())
		return simEvent{reason: ErrFrameTooLong}, true
	}
	s.buf = append(s.buf, b)
	fmt.Fprintf(s.log, "byte %#02x t=%d: append len=%d\n", b, t.UnixNano(), len(s.buf))
	return simEvent{}, true
}

func (s *naiveSim) poll(t time.Time) (simEvent, bool) {
	if !s.checkClock(t) {
		return simEvent{}, false
	}
	if !s.inFrame || t.Sub(s.lastByte) < s.t35 {
		fmt.Fprintf(s.log, "poll t=%d: nothing\n", t.UnixNano())
		return simEvent{}, true
	}
	if s.discarding {
		s.inFrame = false
		s.discarding = false
		s.buf = s.buf[:0]
		fmt.Fprintf(s.log, "poll t=%d: silence exits discard state\n", t.UnixNano())
		return simEvent{}, true
	}
	frame := s.buf
	s.inFrame = false
	s.buf = s.buf[:0]
	return s.settle(frame), true
}

// op is one input in a replayed scenario.
type op struct {
	byteOp bool
	b      byte
	t      time.Time
}

func compareEvent(t *testing.T, iter int, label string, got *Event, want simEvent) {
	t.Helper()
	if got == nil {
		t.Fatalf("iter %d %s: receiver produced no event, want %+v", iter, label, want)
	}
	if want.reason != nil {
		if !errors.Is(got.Reason, want.reason) {
			t.Fatalf("iter %d %s loss mismatch: got=%v want=%v",
				iter, label, got.Reason, want.reason)
		}
		return
	}
	if got.Reason != nil || want.delivered != got.Delivered ||
		want.ignored != got.Ignored || !bytes.Equal(want.frame, got.Frame) {
		t.Fatalf("iter %d %s event mismatch:\n got  reason=%v delivered=%v ignored=%v frame=% x\n want reason=%v delivered=%v ignored=%v frame=% x",
			iter, label, got.Reason, got.Delivered, got.Ignored, got.Frame,
			want.reason, want.delivered, want.ignored, want.frame)
	}
}

func TestDifferentialAgainstNaiveSimulator(t *testing.T) {
	const iterations = 300
	rng := rand.New(rand.NewSource(20261001))

	for iter := 0; iter < iterations; iter++ {
		var log bytes.Buffer
		baud := []int{1200, 4800, 9600, 19199, 19200, 38400, 115200}[rng.Intn(7)]
		addr := 1 + rng.Intn(247)

		r, err := New(baud, addr)
		if err != nil {
			t.Fatal(err)
		}
		sim := newNaiveSim(baud, addr, &log)

		var ops []op
		now := time.Unix(0, 0)
		count := 20 + rng.Intn(400)
		for i := 0; i < count; i++ {
			var gap time.Duration
			switch rng.Intn(10) {
			case 0:
				gap = sim.t15 // exactly t15
			case 1:
				gap = sim.t15 + time.Nanosecond
			case 2:
				gap = sim.t35 - time.Nanosecond
			case 3:
				gap = sim.t35
			case 4:
				gap = sim.t35 + time.Duration(rng.Int63n(int64(5*time.Millisecond)))
			case 5:
				gap = time.Duration(1 + rng.Int63n(int64(sim.t15)))
			case 6:
				gap = -time.Microsecond // backward call
			default:
				gap = time.Duration(1 + rng.Int63n(int64(time.Millisecond)))
			}
			now = now.Add(gap)
			if rng.Intn(8) == 0 {
				ops = append(ops, op{t: now}) // Poll
			} else {
				ops = append(ops, op{byteOp: true, b: byte(rng.Intn(256)), t: now})
			}
		}

		// Inject a CRC-valid frame (local / broadcast / foreign) sometimes.
		if rng.Intn(2) == 0 {
			now = now.Add(sim.t35 + time.Millisecond)
			start := now
			n := 4 + rng.Intn(20)
			frAddr := []byte{byte(addr), 0, byte(1 + (addr % 246))}[rng.Intn(3)]
			frame := validFrame(t, frAddr, n)
			for i, b := range frame {
				ops = append(ops, op{byteOp: true, b: b, t: start.Add(time.Duration(i) * time.Millisecond)})
			}
			ops = append(ops, op{t: start.Add(time.Duration(n+5) * time.Millisecond)})
		}

		for _, o := range ops {
			var (
				ev  *Event
				err error
				sEv simEvent
				ok  bool
			)
			if o.byteOp {
				ev, err = r.OnByte(o.b, o.t)
				sEv, ok = sim.onByte(o.b, o.t)
			} else {
				ev, err = r.Poll(o.t)
				sEv, ok = sim.poll(o.t)
			}
			if err != nil {
				if !errors.Is(err, ErrClockBackward) || ok {
					t.Fatalf("iter %d: clock handling mismatch at t=%d: err=%v simOk=%v\n%s",
						iter, o.t.UnixNano(), err, ok, log.String())
				}
				continue
			}
			if sEv.present() {
				compareEvent(t, iter, fmt.Sprintf("t=%d byteOp=%v", o.t.UnixNano(), o.byteOp), ev, sEv)
			} else if ev != nil {
				t.Fatalf("iter %d at t=%d: unexpected event %+v\n%s",
					iter, o.t.UnixNano(), ev, log.String())
			}
		}

		// Final Poll well past t35 flushes whatever remains.
		flushAt := now.Add(10 * time.Second)
		ev, err := r.Poll(flushAt)
		if err != nil {
			t.Fatal(err)
		}
		sEv, ok := sim.poll(flushAt)
		if !ok {
			t.Fatal("sim flush rejected")
		}
		if sEv.present() {
			compareEvent(t, iter, "flush", ev, sEv)
		} else if ev != nil {
			t.Fatalf("iter %d flush: unexpected event %+v", iter, ev)
		}

		gc := r.Counters()
		if gc.Delivered != sim.delivered || gc.Ignored != sim.ignored ||
			gc.IntraGap != sim.intraGap || gc.TooLong != sim.tooLong ||
			gc.TooShort != sim.tooShort || gc.CRCErrors != sim.crcErrs {
			t.Fatalf("iter %d counters mismatch:\n got  %+v\n want delivered=%d ignored=%d intraGap=%d tooLong=%d tooShort=%d crc=%d\n%s",
				iter, gc, sim.delivered, sim.ignored, sim.intraGap, sim.tooLong,
				sim.tooShort, sim.crcErrs, log.String())
		}
		if iter < 3 {
			t.Logf("iter %d: input baud=%d addr=%d ops=%d; output counters=%+v\n判定依据:\n%s",
				iter, baud, addr, len(ops), gc, log.String())
		}
	}
}

func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	type call struct {
		byteOp bool
		b      byte
		t      time.Time
	}
	var calls []call
	now := time.Unix(0, 0)
	for i := 0; i < 200; i++ {
		now = now.Add(time.Duration(1 + rng.Int63n(int64(3*time.Millisecond))))
		if rng.Intn(6) == 0 {
			calls = append(calls, call{t: now})
		} else {
			calls = append(calls, call{byteOp: true, b: byte(rng.Intn(256)), t: now})
		}
	}

	run := func() (string, Counters) {
		r, _ := New(19199, 33)
		var sb bytes.Buffer
		for _, c := range calls {
			var ev *Event
			var err error
			if c.byteOp {
				ev, err = r.OnByte(c.b, c.t)
			} else {
				ev, err = r.Poll(c.t)
			}
			if err != nil {
				fmt.Fprintf(&sb, "t=%d err=%v\n", c.t.UnixNano(), err)
				continue
			}
			if ev != nil {
				fmt.Fprintf(&sb, "t=%d reason=%v delivered=%v ignored=%v frame=% x\n",
					c.t.UnixNano(), ev.Reason, ev.Delivered, ev.Ignored, ev.Frame)
			}
		}
		ev, _ := r.Poll(now.Add(time.Hour))
		if ev != nil {
			fmt.Fprintf(&sb, "flush reason=%v delivered=%v ignored=%v frame=% x\n",
				ev.Reason, ev.Delivered, ev.Ignored, ev.Frame)
		}
		return sb.String(), r.Counters()
	}

	out1, c1 := run()
	out2, c2 := run()
	if out1 != out2 || c1 != c2 {
		t.Fatalf("replay mismatch:\n--- run1\n%s%+v\n--- run2\n%s%+v", out1, c1, out2, c2)
	}
	t.Logf("input: fixed 200-call sequence; output: deterministic, counters=%+v", c1)
}

// TestConcurrentAccess drives OnByte and Poll from many goroutines whose
// execution order is forced by a turn gate to match the monotonically
// assigned timestamps. Counter readers run without the gate the whole time,
// overlapping every receiver call.
func TestConcurrentAccess(t *testing.T) {
	// 1200 baud: t15 = 13.75ms, t35 = 32.083...ms.
	r, _ := New(1200, 11)

	const (
		byteGap = 5 * time.Millisecond  // < t15: bytes stay in one frame
		pollGap = 50 * time.Millisecond // > t35: first poll settles
		rounds  = 50
		workers = 4
	)
	frame := validFrame(t, 11, 8)

	var (
		clk    int64
		workWG sync.WaitGroup

		turnMu   sync.Mutex
		turnCond = sync.NewCond(&turnMu)
		nextSeq  int
	)
	runInTurn := func(seq int, fn func()) {
		turnMu.Lock()
		for nextSeq != seq {
			turnCond.Wait()
		}
		turnMu.Unlock()

		fn()

		turnMu.Lock()
		nextSeq++
		turnCond.Broadcast()
		turnMu.Unlock()
	}

	stop := make(chan struct{})
	rdone := make(chan struct{})
	go func() {
		defer close(rdone)
		for {
			select {
			case <-stop:
				return
			default:
				_ = r.Counters()
			}
		}
	}()

	jobs := make(chan func(), len(frame)+workers)
	for w := 0; w < workers; w++ {
		go func() {
			for fn := range jobs {
				fn()
			}
		}()
	}

	for round := 0; round < rounds; round++ {
		clk2 := clk
		for i, b := range frame {
			b := b
			clk2 += int64(byteGap)
			ts := time.Unix(0, clk2)
			seq := round*(len(frame)+workers) + i
			workWG.Add(1)
			jobs <- func() {
				defer workWG.Done()
				runInTurn(seq, func() {
					if _, err := r.OnByte(b, ts); err != nil {
						t.Errorf("feed: %v", err)
					}
				})
			}
		}
		workWG.Wait()

		clk = clk2 + int64(pollGap)
		settleAt := time.Unix(0, clk)
		for w := 0; w < workers; w++ {
			w := w
			ts := settleAt.Add(time.Duration(w) * time.Nanosecond)
			seq := round*(len(frame)+workers) + len(frame) + w
			workWG.Add(1)
			jobs <- func() {
				defer workWG.Done()
				runInTurn(seq, func() {
					if _, err := r.Poll(ts); err != nil {
						t.Errorf("poll: %v", err)
					}
				})
			}
		}
		workWG.Wait()
	}
	close(jobs)
	close(stop)
	<-rdone

	c := r.Counters()
	if c.Delivered != rounds || c.TooShort != 0 || c.CRCErrors != 0 ||
		c.IntraGap != 0 || c.TooLong != 0 || c.Ignored != 0 {
		t.Fatalf("unexpected concurrent counters: %+v", c)
	}
	t.Logf("output under concurrent OnByte/Poll workers + counter readers: counters=%+v", c)
}
