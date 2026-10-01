package modbusrtu

import (
	"errors"
	"log"
	"os"
	"sync"
	"testing"
	"time"
)

var testLog = log.New(os.Stdout, "", log.Ltime|log.Lmicroseconds)

// withCRC appends the two little-endian CRC bytes for payload.
func withCRC(payload ...byte) []byte {
	f := append([]byte(nil), payload...)
	c := crc16Modbus(f)
	return append(f, byte(c), byte(c>>8))
}

// feedFrame pushes a frame with constant intra-frame spacing d starting at t0,
// then Polls at t0 + len*d + t35 to settle it.
func feedFrame(t *testing.T, r *Receiver, frame []byte, t0 time.Time, d time.Duration) []*Event {
	t.Helper()
	var got []*Event
	for i, b := range frame {
		ev, err := r.OnByte(b, t0.Add(time.Duration(i)*d))
		if err != nil {
			t.Fatalf("OnByte(%d): %v", i, err)
		}
		if ev != nil {
			got = append(got, ev)
		}
	}
	ev, _ := r.Poll(t0.Add(time.Duration(len(frame))*d + r.t35))
	if ev != nil {
		got = append(got, ev)
	}
	return got
}

func summarize(evs []*Event) string {
	if len(evs) == 0 {
		return "none"
	}
	s := ""
	for _, ev := range evs {
		if s != "" {
			s += ","
		}
		switch ev.Kind {
		case EventDelivered:
			s += "delivered"
		case EventIgnored:
			s += "ignored"
		case EventDropped:
			s += "dropped(" + errorsString(ev.Reason) + ")"
		default:
			s += "none"
		}
	}
	return s
}

func errorsString(e error) string {
	switch {
	case errors.Is(e, ErrIntraGap):
		return "intra-gap"
	case errors.Is(e, ErrFrameTooLong):
		return "too-long"
	case errors.Is(e, ErrFrameTooShort):
		return "too-short"
	case errors.Is(e, ErrCRC):
		return "crc"
	case e == nil:
		return ""
	default:
		return e.Error()
	}
}

func hasKind(evs []*Event, k EventKind) bool {
	for _, ev := range evs {
		if ev.Kind == k {
			return true
		}
	}
	return false
}

func firstReason(evs []*Event, want error) bool {
	for _, ev := range evs {
		if ev.Kind == EventDropped {
			return errors.Is(ev.Reason, want)
		}
	}
	return false
}

func TestThresholds(t *testing.T) {
	cases := []struct {
		baud   int
		want15 time.Duration
		want35 time.Duration
		note   string
	}{
		{19200, 750_000, 1_750_000, "B>=19200 fixed thresholds"},
		{38400, 750_000, 1_750_000, "B>=19200 fixed thresholds"},
		{19199, (33 * 1_000_000_000) / (2 * 19199), (77 * 1_000_000_000) / (2 * 19199), "B<19200 floor formula"},
		{9600, (33 * 1_000_000_000) / (2 * 9600), (77 * 1_000_000_000) / (2 * 9600), "B<19200 floor formula"},
	}
	for _, tc := range cases {
		r, err := New(tc.baud, 1)
		if err != nil {
			t.Fatalf("New(%d): %v", tc.baud, err)
		}
		g15, g35 := r.Thresholds()
		testLog.Printf("[thresholds] B=%d t15=%dns t35=%dns (%s)", tc.baud, g15.Nanoseconds(), g35.Nanoseconds(), tc.note)
		if g15 != tc.want15 || g35 != tc.want35 {
			t.Fatalf("B=%d got (%d,%d) want (%d,%d)", tc.baud, g15, g35, tc.want15, tc.want35)
		}
	}
}

func TestNewRejectsBadParams(t *testing.T) {
	for _, b := range []int{0, -1, -9600} {
		if _, err := New(b, 1); !errors.Is(err, ErrInvalidBaud) {
			t.Fatalf("New(%d,1) err=%v want ErrInvalidBaud", b, err)
		}
	}
	for _, s := range []byte{0, 248, 255} {
		if _, err := New(9600, s); !errors.Is(err, ErrInvalidSlaveAddr) {
			t.Fatalf("New(9600,%d) err=%v want ErrInvalidSlaveAddr", s, err)
		}
	}
	testLog.Printf("[new] non-positive baud and slave addresses outside 1..247 rejected")
}

func TestCRCCheckValue(t *testing.T) {
	if got := crc16Modbus([]byte("123456789")); got != 0x4B37 {
		t.Fatalf("check value = %#04x want 0x4B37", got)
	}
	testLog.Printf(`[crc] CRC16/MODBUS("123456789")=0x4B37 OK`)
}

func TestGapBoundaries(t *testing.T) {
	// 6-byte good frame addressed to slave 1; the tested gap sits between
	// byte index 1 and 2, so the leading fragment is 3 bytes long.
	frame := withCRC(1, 0x03, 0x00, 0x00)
	cases := []struct {
		name   string
		gapOf  func(t15, t35 time.Duration) time.Duration
		want   error
		reason string
	}{
		{"gap==t15", func(a, _ time.Duration) time.Duration { return a }, nil, "t15 itself is allowed"},
		{"gap==t15+1", func(a, _ time.Duration) time.Duration { return a + 1 }, ErrIntraGap, "strictly above t15 and below t35 => intra-frame violation"},
		{"gap==t35-1", func(_, b time.Duration) time.Duration { return b - 1 }, ErrIntraGap, "t35-1 still violates intra-frame gap"},
		{"gap==t35", func(_, b time.Duration) time.Duration { return b }, ErrFrameTooShort, "t35 is left-closed: 3-byte leading fragment settles first"},
	}
	for _, tc := range cases {
		r, _ := New(19200, 1)
		t15, t35 := r.Thresholds()
		g := tc.gapOf(t15, t35)
		base := time.Unix(0, 0)

		var evs []*Event
		emit := func(ev *Event) {
			if ev != nil {
				evs = append(evs, ev)
			}
		}
		emit(mustByte(t, r, frame[0], base))
		emit(mustByte(t, r, frame[1], base))
		emit(mustByte(t, r, frame[2], base.Add(g)))
		for i := 3; i < len(frame); i++ {
			emit(mustByte(t, r, frame[i], base.Add(g+time.Duration(i-2))))
		}
		emit(mustPoll(t, r, base.Add(g+time.Duration(len(frame))+t35)))

		testLog.Printf("[gap] %-10s gap=%dns events=%s | %s", tc.name, g.Nanoseconds(), summarize(evs), tc.reason)
		if tc.name == "gap==t15" {
			if !hasKind(evs, EventDelivered) {
				t.Fatalf("%s: want delivered, got %s", tc.name, summarize(evs))
			}
			continue
		}
		if !firstReason(evs, tc.want) {
			t.Fatalf("%s: want %v, got %s", tc.name, tc.want, summarize(evs))
		}
	}
}

func TestFrameLength4And3(t *testing.T) {
	r, _ := New(19200, 1)
	good := withCRC(1, 0x06)
	evs := feedFrame(t, r, good, time.Unix(0, 0), 0)
	testLog.Printf("[len4] frame=% x events=%s | exactly 4 bytes with good CRC is delivered", good, summarize(evs))
	if !hasKind(evs, EventDelivered) {
		t.Fatalf("len4: %s", summarize(evs))
	}

	r2, _ := New(19200, 1)
	short := []byte{1, 6, 0}
	evs = feedFrame(t, r2, short, time.Unix(0, 0), 0)
	testLog.Printf("[len3] frame=% x events=%s | 3 bytes settles as too-short", short, summarize(evs))
	if !firstReason(evs, ErrFrameTooShort) {
		t.Fatalf("len3: %s", summarize(evs))
	}
}

func TestFrameLength256And257(t *testing.T) {
	payload := make([]byte, 254)
	payload[0] = 1
	good := withCRC(payload...)
	if len(good) != 256 {
		t.Fatalf("setup len=%d", len(good))
	}
	r, _ := New(19200, 1)
	evs := feedFrame(t, r, good, time.Unix(0, 0), 0)
	testLog.Printf("[len256] %d-byte valid frame events=%s", len(good), summarize(evs))
	if !hasKind(evs, EventDelivered) {
		t.Fatalf("len256: %s", summarize(evs))
	}

	r2, _ := New(19200, 1)
	dropIdx := -1
	for i := 0; i < 257; i++ {
		ev := mustByte(t, r2, byte('A'), time.Unix(0, int64(i)))
		if ev != nil {
			evs = []*Event{ev}
			dropIdx = i
		}
	}
	testLog.Printf("[len257] TooLong at byte index %d events=%s | 257th byte voids immediately", dropIdx, summarize(evs))
	if dropIdx != 256 || !firstReason(evs, ErrFrameTooLong) {
		t.Fatalf("257: idx=%d %s", dropIdx, summarize(evs))
	}
	if ev := mustByte(t, r2, 1, time.Unix(0, 300)); ev != nil {
		t.Fatalf("discard-state byte emitted event %+v", ev)
	}
	if ev := mustByte(t, r2, 1, time.Unix(0, 300+1_750_000)); ev != nil {
		t.Fatalf("fresh byte after t35 quiet gap emitted event %+v", ev)
	}
	if c := r2.Snapshot(); c.TooLong != 1 {
		t.Fatalf("TooLong=%d want 1", c.TooLong)
	}
}

func TestAddressBroadcastVsForeign(t *testing.T) {
	r, _ := New(19200, 5)
	bcast := withCRC(0, 0x03, 0xAA)
	evs := feedFrame(t, r, bcast, time.Unix(0, 0), 0)
	testLog.Printf("[addr] broadcast=% x events=%s | address 0 delivered", bcast, summarize(evs))
	if !hasKind(evs, EventDelivered) {
		t.Fatalf("broadcast: %s", summarize(evs))
	}

	foreign := withCRC(7, 0x03, 0xAA)
	evs = feedFrame(t, r, foreign, time.Unix(10, 0), 0)
	testLog.Printf("[addr] foreign=% x events=%s | address 7 (neither S=5 nor 0) ignored, not delivered", foreign, summarize(evs))
	if !hasKind(evs, EventIgnored) || hasKind(evs, EventDelivered) {
		t.Fatalf("foreign: %s", summarize(evs))
	}
	if c := r.Snapshot(); c.Delivered != 1 || c.Ignored != 1 {
		t.Fatalf("counts=%+v want Delivered=1 Ignored=1", c)
	}
}

func TestCRCMismatchAndOrder(t *testing.T) {
	bad := withCRC(1, 0x03, 0x00, 0x01)
	bad[len(bad)-1] ^= 0xFF
	r, _ := New(19200, 1)
	evs := feedFrame(t, r, bad, time.Unix(0, 0), 0)
	testLog.Printf("[crc] corrupted=% x events=%s | little-endian CRC field mismatch", bad, summarize(evs))
	if !firstReason(evs, ErrCRC) {
		t.Fatalf("crc: %s", summarize(evs))
	}

	// Short beats CRC and address even with foreign address.
	r2, _ := New(19200, 1)
	evs = feedFrame(t, r2, []byte{9, 9, 9}, time.Unix(0, 0), 0)
	testLog.Printf("[order] 3-byte foreign-addr frame events=%s | short reported before CRC and address", summarize(evs))
	if !firstReason(evs, ErrFrameTooShort) {
		t.Fatalf("order: %s", summarize(evs))
	}
}

func TestPollSettles(t *testing.T) {
	frame := withCRC(1, 0x03, 0x11)
	r, _ := New(19200, 1)
	_, t35 := r.Thresholds()
	var evs []*Event
	for i, b := range frame {
		ev := mustByte(t, r, b, time.Unix(0, int64(i)))
		if ev != nil {
			t.Fatalf("unexpected mid-frame event: %+v", ev)
		}
	}
	// Poll before t35 settles nothing.
	last := int64(len(frame) - 1)
	if ev := mustPoll(t, r, time.Unix(0, last).Add(t35-1)); ev != nil {
		t.Fatalf("early Poll settled: %+v", ev)
	}
	ev := mustPoll(t, r, time.Unix(0, last).Add(t35))
	if ev != nil {
		evs = append(evs, ev)
	}
	testLog.Printf("[poll] settle via Poll at exactly lastByte+t35: frame=% x events=%s", frame, summarize(evs))
	if !hasKind(evs, EventDelivered) {
		t.Fatalf("poll: %s", summarize(evs))
	}
	// Polling again settles nothing and does not double count.
	if ev := mustPoll(t, r, time.Unix(0, last).Add(2*t35)); ev != nil {
		t.Fatalf("second Poll settled: %+v", ev)
	}
	if c := r.Snapshot(); c.Delivered != 1 {
		t.Fatalf("Delivered=%d want 1", c.Delivered)
	}
}

func TestClockBackwardRejected(t *testing.T) {
	r, _ := New(19200, 1)
	short := []byte{1, 2}
	mustByte(t, r, short[0], time.Unix(0, 100))
	if _, err := r.OnByte(2, time.Unix(0, 99)); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("backward OnByte err=%v", err)
	}
	mustPoll(t, r, time.Unix(0, 100))
	if _, err := r.Poll(time.Unix(0, 98)); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("backward Poll err=%v", err)
	}
	// Equal time is fine, and rejected calls changed nothing.
	ev := mustByte(t, r, short[1], time.Unix(0, 100))
	if ev != nil {
		t.Fatalf("equal-time byte produced event %+v", ev)
	}
	ev = mustPoll(t, r, time.Unix(0, 100).Add(r.t35))
	var evs []*Event
	if ev != nil {
		evs = append(evs, ev)
	}
	testLog.Printf("[clock] backward calls rejected; state unchanged, settled frame=% x events=%s", short, summarize(evs))
	if !firstReason(evs, ErrFrameTooShort) {
		t.Fatalf("post-reject settlement: %s", summarize(evs))
	}
	if c := r.Snapshot(); c.TooShort != 1 {
		t.Fatalf("TooShort=%d want 1 (rejected calls must not mutate state)", c.TooShort)
	}
}

func TestConcurrentDeterminism(t *testing.T) {
	frame := withCRC(1, 0x03, 0x22)
	run := func() Counters {
		r, _ := New(19200, 1)
		_, t35 := r.Thresholds()
		var wg sync.WaitGroup
		for i, b := range frame {
			wg.Add(1)
			go func(i int, b byte) {
				defer wg.Done()
				_, _ = r.OnByte(b, time.Unix(0, int64(i)))
			}(i, b)
		}
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = r.Snapshot()
			}()
		}
		wg.Wait()
		_, _ = r.Poll(time.Unix(0, int64(len(frame))).Add(t35))
		return r.Snapshot()
	}
	// Concurrent interleave may legally corrupt frame content (a serial order
	// of same-timestamp byte deliveries exists), but counters must never race.
	for i := 0; i < 20; i++ {
		_ = run()
	}
	testLog.Printf("[concurrency] 20 parallel feeds completed under -race without data races")
}

func mustByte(t *testing.T, r *Receiver, b byte, at time.Time) *Event {
	t.Helper()
	ev, err := r.OnByte(b, at)
	if err != nil {
		t.Fatalf("OnByte(%#x,%v): %v", b, at.UnixNano(), err)
	}
	return ev
}

func mustPoll(t *testing.T, r *Receiver, at time.Time) *Event {
	t.Helper()
	ev, err := r.Poll(at)
	if err != nil {
		t.Fatalf("Poll(%v): %v", at.UnixNano(), err)
	}
	return ev
}
