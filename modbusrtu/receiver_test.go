package modbusrtu

import (
	"errors"
	"testing"
	"time"
)

// appendCRC appends the low and high CRC bytes, in that order.
func appendCRC(t *testing.T, b []byte) []byte {
	t.Helper()
	c := crc16(b)
	return append(b, byte(c), byte(c>>8))
}

// validFrame builds a CRC-valid frame whose first byte is addr.
func validFrame(t *testing.T, addr byte, n int) []byte {
	t.Helper()
	b := make([]byte, n-2)
	for i := range b {
		if i == 0 {
			b[i] = addr
			continue
		}
		b[i] = byte(i*7 + 3)
	}
	return appendCRC(t, b)
}

// feed pushes every byte of frame with the given intra-frame spacing.
func feed(r *Receiver, frame []byte, start time.Time, spacing time.Duration) error {
	for i, b := range frame {
		ev, err := r.OnByte(b, start.Add(time.Duration(i)*spacing))
		if err != nil {
			return err
		}
		if ev != nil {
			return errors.New("unexpected early event")
		}
	}
	return nil
}

func TestCRCCheckValue(t *testing.T) {
	if got := crc16([]byte("123456789")); got != 0x4B37 {
		t.Fatalf("crc16(123456789) = %#04x, want 0x4B37", got)
	}
}

func TestNewValidation(t *testing.T) {
	for _, baud := range []int{0, -1, -9600} {
		if _, err := New(baud, 1); err == nil {
			t.Fatalf("New(%d, 1) expected error", baud)
		}
	}
	for _, addr := range []int{0, -1, 248, 1000} {
		if _, err := New(9600, addr); err == nil {
			t.Fatalf("New(9600, %d) expected error", addr)
		}
	}
	if _, err := New(19200, 247); err != nil {
		t.Fatalf("valid construction failed: %v", err)
	}
}

func TestThresholdFormulas(t *testing.T) {
	cases := []struct {
		baud    int
		wantT15 time.Duration
		wantT35 time.Duration
	}{
		{19200, 750 * time.Microsecond, 1750 * time.Microsecond},
		{38400, 750 * time.Microsecond, 1750 * time.Microsecond},
		{19199, time.Duration(33_000_000_000/(2*19199)) * time.Nanosecond,
			time.Duration(77_000_000_000/(2*19199)) * time.Nanosecond},
		{9600, time.Duration(33_000_000_000/(2*9600)) * time.Nanosecond,
			time.Duration(77_000_000_000/(2*9600)) * time.Nanosecond},
	}
	for _, tc := range cases {
		r, err := New(tc.baud, 1)
		if err != nil {
			t.Fatalf("New(%d,1): %v", tc.baud, err)
		}
		if r.t15 != tc.wantT15 || r.t35 != tc.wantT35 {
			t.Fatalf("baud %d: thresholds = %v,%v want %v,%v",
				tc.baud, r.t15, r.t35, tc.wantT15, tc.wantT35)
		}
		t.Logf("input: baud=%d -> t15=%v t35=%v", tc.baud, r.t15, r.t35)
	}
}

// TestGapBoundaries checks gaps of exactly t15, t15+1ns, t35-1ns and
// exactly t35 between the first and second byte of a frame.
func TestGapBoundaries(t *testing.T) {
	frame := validFrame(t, 5, 8)

	type gapCase struct {
		name     string
		gap      time.Duration
		wantLoss bool
	}
	for _, baud := range []int{19200, 19199} {
		r0, _ := New(baud, 5)
		t15, t35 := r0.t15, r0.t35
		gaps := []gapCase{
			{"exact-t15", t15, false},
			{"t15-plus-1ns", t15 + time.Nanosecond, true},
			{"t35-minus-1ns", t35 - time.Nanosecond, true},
		}
		for _, gc := range gaps {
			r, _ := New(baud, 5)
			base := time.Unix(0, 0)
			if _, err := r.OnByte(frame[0], base); err != nil {
				t.Fatal(err)
			}
			ev, err := r.OnByte(frame[1], base.Add(gc.gap))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("input: baud=%d gap=%s (%v); output: ev=%+v; basis: t15=%v t35=%v",
				baud, gc.name, gc.gap, ev, t15, t35)
			if gc.wantLoss {
				if ev == nil || !errors.Is(ev.Reason, ErrIntraFrameGap) {
					t.Fatalf("baud=%d gap=%s: want ErrIntraFrameGap, got %+v",
						baud, gc.name, ev)
				}
			} else if ev != nil {
				t.Fatalf("baud=%d gap=%s: expected no event, got %+v",
					baud, gc.name, ev)
			}
		}

		// Exactly t35: the first byte settles as a too-short frame, then
		// the second byte opens a fresh frame.
		r, _ := New(baud, 5)
		base := time.Unix(0, 0)
		if _, err := r.OnByte(frame[0], base); err != nil {
			t.Fatal(err)
		}
		ev, err := r.OnByte(frame[1], base.Add(t35))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("input: baud=%d gap=exact-t35 (%v); output: ev=%+v; basis: g>=t35 closes prior frame",
			baud, t35, ev)
		if ev == nil || !errors.Is(ev.Reason, ErrFrameTooShort) {
			t.Fatalf("exact t35: want ErrFrameTooShort settlement, got %+v", ev)
		}
	}
}

// TestDiscardStateLocksOutUntilT35 verifies that after an intra-frame gap
// all following bytes are ignored until a >= t35 gap, even if another
// offending gap occurs in between.
func TestDiscardStateLocksOutUntilT35(t *testing.T) {
	r, _ := New(19200, 5)
	t0 := time.Unix(0, 0)
	frame := validFrame(t, 5, 8)

	must := func(ev *Event, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(r.OnByte(frame[0], t0))
	secondAt := t0.Add(100 * time.Microsecond)
	must(r.OnByte(frame[1], secondAt))
	// Gap from the previous byte is exactly t15+1us (751us at 19200).
	thirdAt := secondAt.Add(751 * time.Microsecond)
	ev, err := r.OnByte(frame[2], thirdAt)
	must(ev, err)
	if ev == nil || !errors.Is(ev.Reason, ErrIntraFrameGap) {
		t.Fatalf("want gap violation, got %+v", ev)
	}
	// A byte 100us later: ignored, no second event.
	ignored1 := thirdAt.Add(100 * time.Microsecond)
	ev, err = r.OnByte(frame[3], ignored1)
	must(ev, err)
	if ev != nil {
		t.Fatalf("discard-state byte must be ignored, got %+v", ev)
	}
	// Another offending gap mid-discard: still ignored and not recounted.
	ev, err = r.OnByte(frame[4], ignored1.Add(time.Millisecond/2))
	must(ev, err)
	if ev != nil {
		t.Fatalf("byte during discard before t35 must be ignored, got %+v", ev)
	}
	// Gap >= t35 from the last byte: starts a new, deliverable frame.
	lastIgnored := ignored1.Add(time.Millisecond / 2)
	newStart := lastIgnored.Add(2 * time.Millisecond)
	must(r.OnByte(frame[0], newStart))
	for i := 1; i < len(frame); i++ {
		must(r.OnByte(frame[i], newStart.Add(time.Duration(i)*100*time.Microsecond)))
	}
	ev, err = r.Poll(newStart.Add(time.Duration(len(frame))*100*time.Microsecond + 2*time.Millisecond))
	must(ev, err)
	if ev == nil || !ev.Delivered {
		t.Fatalf("post-discard frame should be delivered, got %+v", ev)
	}
	if c := r.Counters(); c.IntraGap != 1 || c.Delivered != 1 {
		t.Fatalf("counters = %+v, want IntraGap=1 Delivered=1", c)
	}
	t.Logf("output: counters=%+v; basis: discard bytes ignored, gaps still measured from them", r.Counters())
}

func TestLengthBoundaries(t *testing.T) {
	for _, n := range []int{3, 4} {
		r, _ := New(9600, 5)
		var b []byte
		if n == 4 {
			b = validFrame(t, 5, 4)
		} else {
			b = []byte{5, 3, 1, 0}[:3]
		}
		t0 := time.Unix(0, 0)
		if err := feed(r, b, t0, time.Millisecond); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		ev, err := r.Poll(t0.Add(time.Duration(n)*time.Millisecond + 10*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("input: frame length=%d; output: %+v; basis: settlement order length<4 first", n, ev)
		if n == 3 && (ev == nil || !errors.Is(ev.Reason, ErrFrameTooShort)) {
			t.Fatalf("len 3: want ErrFrameTooShort, got %+v", ev)
		}
		if n == 4 && (ev == nil || !ev.Delivered) {
			t.Fatalf("len 4: want delivery, got %+v", ev)
		}
	}
}

func TestFrameTooLong256And257(t *testing.T) {
	for _, n := range []int{256, 257} {
		r, _ := New(9600, 5)
		t0 := time.Unix(0, 0)
		var got *Event
		for i := 0; i < n; i++ {
			ev, err := r.OnByte(byte(i), t0.Add(time.Duration(i)*time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			if ev != nil {
				got = ev
			}
		}
		t.Logf("input: %d bytes spaced 1ms; output at 257th byte: %+v; basis: >256 voids immediately", n, got)
		if n == 256 && got != nil {
			t.Fatalf("256th byte must be accepted, got %+v", got)
		}
		if n == 257 {
			if got == nil || !errors.Is(got.Reason, ErrFrameTooLong) {
				t.Fatalf("257th byte: want ErrFrameTooLong, got %+v", got)
			}
			// Subsequent bytes ignored until a >= t35 gap; Poll yields
			// nothing for the discarded frame.
			// One more byte 1ms later: gap < t15, ignored while discarding.
			ev, err := r.OnByte(0xAA, t0.Add(257*time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			if ev != nil {
				t.Fatalf("post-too-long byte ignored until t35, got %+v", ev)
			}
			// Silence >= t35 releases the discard state silently.
			ev, _ = r.Poll(t0.Add(257*time.Millisecond + 10*time.Millisecond))
			if ev != nil {
				t.Fatalf("discarded frame must not settle again, got %+v", ev)
			}
		}
	}
}

func TestAddressesBroadcastForeignLocal(t *testing.T) {
	for _, tc := range []struct {
		name string
		addr byte
		kind string
	}{
		{"local", 7, "delivered"},
		{"broadcast", 0, "delivered"},
		{"foreign", 8, "ignored"},
	} {
		r, _ := New(9600, 7)
		frame := validFrame(t, tc.addr, 6)
		t0 := time.Unix(0, 0)
		if err := feed(r, frame, t0, time.Millisecond); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		ev, err := r.Poll(t0.Add(time.Duration(len(frame)+5) * time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("input: frame addr=%d local=7; output: %+v; basis: addr 0 or S delivered else Ignored",
			tc.addr, ev)
		if ev == nil {
			t.Fatalf("%s: expected event", tc.name)
		}
		switch tc.kind {
		case "delivered":
			if !ev.Delivered || ev.Ignored || ev.Reason != nil {
				t.Fatalf("%s: want delivery, got %+v", tc.name, ev)
			}
		case "ignored":
			if ev.Delivered || !ev.Ignored || ev.Reason != nil {
				t.Fatalf("%s: want ignored valid frame, got %+v", tc.name, ev)
			}
		}
	}
}

func TestCRCMismatchAndSettlementOrder(t *testing.T) {
	r, _ := New(9600, 5)
	frame := validFrame(t, 5, 6)
	frame[len(frame)-1] ^= 0xFF // corrupt CRC
	t0 := time.Unix(0, 0)
	if err := feed(r, frame, t0, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ev, err := r.Poll(t0.Add(20 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input: CRC-corrupted frame; output: %+v; basis: CRC checked before address", ev)
	if ev == nil || !errors.Is(ev.Reason, ErrCRC) {
		t.Fatalf("want ErrCRC, got %+v", ev)
	}

	// Too short beats CRC even though the two trailing bytes cannot match.
	r2, _ := New(9600, 5)
	if ev, err := r2.OnByte(0x09, t0); err != nil {
		t.Fatal(err)
	} else if ev != nil {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if ev, err := r2.OnByte(0x03, t0.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	} else if ev != nil {
		t.Fatalf("unexpected event: %+v", ev)
	}
	ev, err = r2.Poll(t0.Add(20 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if ev == nil || !errors.Is(ev.Reason, ErrFrameTooShort) {
		t.Fatalf("short foreign frame: want ErrFrameTooShort first, got %+v", ev)
	}
}

func TestPollSettlesFrameAlone(t *testing.T) {
	r, _ := New(9600, 5)
	frame := validFrame(t, 5, 6)
	t0 := time.Unix(0, 0)
	if err := feed(r, frame, t0, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	lastByteAt := t0.Add(time.Duration(len(frame)-1) * time.Millisecond)
	// Poll 1ms before t35: nothing yet.
	ev, err := r.Poll(lastByteAt.Add(r.t35 - time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if ev != nil {
		t.Fatalf("Poll before t35 must not settle, got %+v", ev)
	}
	// Exactly t35 after the last byte: settles.
	settleAt := lastByteAt.Add(r.t35)
	ev, err = r.Poll(settleAt)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input: Poll at exactly t35 after last byte; output: %+v; basis: Poll uses same boundary rule", ev)
	if ev == nil || !ev.Delivered {
		t.Fatalf("Poll at t35: want delivery, got %+v", ev)
	}
	// Repeated Polls with equal or later timestamps produce nothing.
	ev, err = r.Poll(settleAt)
	if err != nil {
		t.Fatal(err)
	}
	if ev != nil {
		t.Fatalf("second Poll must be a no-op, got %+v", ev)
	}
}

func TestClockBackwardRejectedAndStateless(t *testing.T) {
	r, _ := New(9600, 5)
	frame := validFrame(t, 5, 6)
	t0 := time.Unix(0, 0)
	if err := feed(r, frame[:2], t0, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	before := r.Counters()

	ev, err := r.OnByte(0x01, t0.Add(time.Microsecond))
	if !errors.Is(err, ErrClockBackward) || ev != nil {
		t.Fatalf("backward OnByte: want ErrClockBackward, got ev=%+v err=%v", ev, err)
	}
	ev, err = r.Poll(t0.Add(time.Microsecond))
	if !errors.Is(err, ErrClockBackward) || ev != nil {
		t.Fatalf("backward Poll: want ErrClockBackward, got ev=%+v err=%v", ev, err)
	}
	// Equal timestamp is accepted; frame still intact and settles normally.
	if err := feed(r, frame[2:], t0.Add(2*time.Millisecond), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ev, err = r.Poll(t0.Add(20 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if ev == nil || !ev.Delivered {
		t.Fatalf("frame after rejected backward calls should still settle, got %+v", ev)
	}
	after := r.Counters()
	_ = before
	t.Logf("input: two backward calls mid-frame; output: counters=%+v; basis: rejected calls change no state", after)
	if after.Delivered != 1 || after.TooShort != 0 || after.CRCErrors != 0 {
		t.Fatalf("unexpected counters after backward calls: %+v", after)
	}
}
