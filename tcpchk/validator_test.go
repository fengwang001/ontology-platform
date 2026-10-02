package tcpchk

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func mustNew(t *testing.T, p Params) *Validator {
	t.Helper()
	v, err := NewValidator(p)
	if err != nil {
		t.Fatalf("NewValidator(%+v): %v", p, err)
	}
	return v
}

func mustProcess(t *testing.T, v *Validator, now uint64, seg Segment) Action {
	t.Helper()
	a, err := v.Process(now, seg)
	if err != nil {
		t.Fatalf("Process(now=%d, seg=%+v): %v", now, seg, err)
	}
	return a
}

func baseParams() Params {
	return Params{
		RcvNxt:    1000,
		RcvWnd:    100,
		SndUna:    0,
		SndNxt:    0,
		MaxSndWnd: 0,
		Limit:     1000,
		Period:    1000,
	}
}

// rcvNxt=1000, rcvWnd=100: RST three-way decision.
func TestRSTThreeWay(t *testing.T) {
	rst := func(seq uint32) Segment { return Segment{Seq: seq, RST: true} }

	v := mustNew(t, baseParams())
	if a := mustProcess(t, v, 0, rst(1000)); a != Reset {
		t.Fatalf("RST Seq=1000: got %s, want Reset", a)
	}
	if s := v.Snapshot(); s.State != Closed {
		t.Fatalf("after Reset: state=%s, want Closed", s.State)
	}

	for _, seq := range []uint32{1050, 1099} {
		v := mustNew(t, baseParams())
		if a := mustProcess(t, v, 0, rst(seq)); a != AckChallenge {
			t.Fatalf("RST Seq=%d: got %s, want AckChallenge", seq, a)
		}
	}
	for _, seq := range []uint32{1100, 999} {
		v := mustNew(t, baseParams())
		if a := mustProcess(t, v, 0, rst(seq)); a != Drop {
			t.Fatalf("RST Seq=%d: got %s, want Drop", seq, a)
		}
	}
}

// rcvWnd=0: RST must match rcvNxt exactly.
func TestRSTZeroWindow(t *testing.T) {
	p := baseParams()
	p.RcvWnd = 0

	v := mustNew(t, p)
	if a := mustProcess(t, v, 0, Segment{Seq: 1000, RST: true}); a != Reset {
		t.Fatalf("RST Seq=1000 (wnd 0): got %s, want Reset", a)
	}
	v = mustNew(t, p)
	if a := mustProcess(t, v, 0, Segment{Seq: 1001, RST: true}); a != Drop {
		t.Fatalf("RST Seq=1001 (wnd 0): got %s, want Drop", a)
	}
}

// Wraparound: rcvNxt=0xFFFFFFF0, rcvWnd=0x20, RST Seq=0x0000000F is at
// offset 0x1F, inside the window.
func TestRSTWraparound(t *testing.T) {
	p := baseParams()
	p.RcvNxt = 0xFFFFFFF0
	p.RcvWnd = 0x20
	v := mustNew(t, p)
	if a := mustProcess(t, v, 0, Segment{Seq: 0x0000000F, RST: true}); a != AckChallenge {
		t.Fatalf("wraparound RST: got %s, want AckChallenge", a)
	}
}

// Non-RST acceptability and rcvNxt advancement.
func TestNonRSTWindow(t *testing.T) {
	ack := func(seq, ln uint32) Segment { return Segment{Seq: seq, Len: ln, ACK: true} }

	// First byte inside the window: acceptable.
	v := mustNew(t, baseParams())
	if a := mustProcess(t, v, 0, ack(1090, 20)); a != Accepted {
		t.Fatalf("Seq=1090 Len=20: got %s, want Accepted", a)
	}
	if s := v.Snapshot(); s.RcvNxt != 1000 {
		t.Fatalf("out-of-order segment advanced rcvNxt to %d", s.RcvNxt)
	}

	// Last byte 1009 inside the window; left overlap is clipped and
	// rcvNxt advances to 1010.
	v = mustNew(t, baseParams())
	if a := mustProcess(t, v, 0, ack(990, 20)); a != Accepted {
		t.Fatalf("Seq=990 Len=20: got %s, want Accepted", a)
	}
	if s := v.Snapshot(); s.RcvNxt != 1010 {
		t.Fatalf("left-overlap clip: rcvNxt=%d, want 1010", s.RcvNxt)
	}
}

// Unacceptable non-RST segments get a plain ACK without the limiter.
func TestAckPlainCases(t *testing.T) {
	// Seq=900 Len=20: last byte 919 still left of rcvNxt=1000.
	v := mustNew(t, baseParams())
	if a := mustProcess(t, v, 0, Segment{Seq: 900, Len: 20, ACK: true}); a != AckPlain {
		t.Fatalf("Seq=900 Len=20: got %s, want AckPlain", a)
	}

	// rcvWnd=0 and Len>0: never acceptable.
	p := baseParams()
	p.RcvWnd = 0
	v = mustNew(t, p)
	if a := mustProcess(t, v, 0, Segment{Seq: 1000, Len: 10, ACK: true}); a != AckPlain {
		t.Fatalf("rcvWnd=0 Len=10: got %s, want AckPlain", a)
	}

	// SYN inside the window challenges; outside it gets a plain ACK.
	v = mustNew(t, baseParams())
	if a := mustProcess(t, v, 0, Segment{Seq: 1050, SYN: true, ACK: true}); a != AckChallenge {
		t.Fatalf("SYN Seq=1050: got %s, want AckChallenge", a)
	}
	v = mustNew(t, baseParams())
	if a := mustProcess(t, v, 0, Segment{Seq: 1200, SYN: true, ACK: true}); a != AckPlain {
		t.Fatalf("SYN Seq=1200: got %s, want AckPlain", a)
	}
}

// ACK range: sndUna=5000, sndNxt=5200, maxSndWnd=300 gives the closed
// interval [4700, 5200].
func TestACKRange(t *testing.T) {
	p := baseParams()
	p.SndUna = 5000
	p.SndNxt = 5200
	p.MaxSndWnd = 300
	seg := func(ack uint32) Segment { return Segment{Seq: 1000, Ack: ack, ACK: true} }

	for _, ack := range []uint32{4700, 5000, 5200} {
		v := mustNew(t, p)
		if a := mustProcess(t, v, 0, seg(ack)); a != Accepted {
			t.Fatalf("Ack=%d: got %s, want Accepted (boundary-inclusive)", ack, a)
		}
	}
	for _, ack := range []uint32{4699, 5201} {
		v := mustNew(t, p)
		if a := mustProcess(t, v, 0, seg(ack)); a != AckChallenge {
			t.Fatalf("Ack=%d: got %s, want AckChallenge", ack, a)
		}
		if s := v.Snapshot(); s.RcvNxt != 1000 || s.SndUna != 5000 {
			t.Fatalf("Ack=%d challenge changed state: rcvNxt=%d sndUna=%d", ack, s.RcvNxt, s.SndUna)
		}
	}

	// Ack=5100 lies in (sndUna, sndNxt] and advances sndUna.
	v := mustNew(t, p)
	if a := mustProcess(t, v, 0, seg(5100)); a != Accepted {
		t.Fatalf("Ack=5100: got %s, want Accepted", a)
	}
	if s := v.Snapshot(); s.SndUna != 5100 {
		t.Fatalf("Ack=5100: sndUna=%d, want 5100", s.SndUna)
	}

	// Ack == sndUna is acceptable but does not move sndUna; Ack left of
	// sndUna but inside [lo, sndNxt] is accepted without moving it either.
	v = mustNew(t, p)
	if a := mustProcess(t, v, 0, seg(4800)); a != Accepted {
		t.Fatalf("Ack=4800: got %s, want Accepted", a)
	}
	if s := v.Snapshot(); s.SndUna != 5000 {
		t.Fatalf("Ack=4800: sndUna=%d, want 5000 (old ACK must not rewind)", s.SndUna)
	}
}

// maxSndWnd tracks the maximum advertised window on Accepted segments.
func TestMaxSndWndGrowth(t *testing.T) {
	p := baseParams()
	p.SndUna = 5000
	p.SndNxt = 5200
	p.MaxSndWnd = 300
	v := mustNew(t, p)
	a := mustProcess(t, v, 0, Segment{Seq: 1000, Ack: 5000, Wnd: 400, ACK: true})
	if a != Accepted {
		t.Fatalf("got %s, want Accepted", a)
	}
	if s := v.Snapshot(); s.MaxSndWnd != 400 {
		t.Fatalf("maxSndWnd=%d, want 400", s.MaxSndWnd)
	}
	// A smaller advertised window never shrinks the maximum, and the
	// grown maximum widens the acceptable ACK interval on the left.
	a = mustProcess(t, v, 1, Segment{Seq: 1000, Ack: 4600, Wnd: 10, ACK: true})
	if a != Accepted {
		t.Fatalf("Ack=4600 with grown maxSndWnd: got %s, want Accepted", a)
	}
	if s := v.Snapshot(); s.MaxSndWnd != 400 {
		t.Fatalf("maxSndWnd shrank to %d", s.MaxSndWnd)
	}
}

// C=2, P=1000: two challenges per window, window resets when
// now-ws >= P.
func TestChallengeRateLimit(t *testing.T) {
	p := baseParams()
	p.Limit = 2
	p.Period = 1000
	v := mustNew(t, p)
	syn := Segment{Seq: 1050, SYN: true, ACK: true}

	cases := []struct {
		now  uint64
		want Action
	}{
		{0, AckChallenge},
		{10, AckChallenge},
		{20, Suppressed},
		{999, Suppressed},
		{1000, AckChallenge}, // 1000-0 >= 1000 opens a new window
	}
	for _, c := range cases {
		if a := mustProcess(t, v, c.now, syn); a != c.want {
			t.Fatalf("now=%d: got %s, want %s", c.now, a, c.want)
		}
	}
	if s := v.Snapshot(); s.WindowStart != 1000 || s.Count != 1 {
		t.Fatalf("after window reset: ws=%d cnt=%d, want ws=1000 cnt=1", s.WindowStart, s.Count)
	}
}

// C=2 gives an RST quota of ceil(2/2)=1: RST-triggered challenges take
// at most half the window quota.
func TestRSTQuotaC2(t *testing.T) {
	p := baseParams()
	p.Limit = 2
	p.Period = 100000
	v := mustNew(t, p)

	if a := mustProcess(t, v, 0, Segment{Seq: 1050, RST: true}); a != AckChallenge {
		t.Fatalf("RST#1: got %s, want AckChallenge", a)
	}
	if s := v.Snapshot(); s.Count != 1 || s.CountRST != 1 {
		t.Fatalf("after RST#1: cnt=%d cntR=%d, want 1/1", s.Count, s.CountRST)
	}
	// cnt=1 < C=2 but the RST quota is exhausted.
	if a := mustProcess(t, v, 5, Segment{Seq: 1060, RST: true}); a != Suppressed {
		t.Fatalf("RST#2: got %s, want Suppressed", a)
	}
	if s := v.Snapshot(); s.Count != 1 || s.CountRST != 1 {
		t.Fatalf("suppressed RST changed limiter: cnt=%d cntR=%d", s.Count, s.CountRST)
	}
	// A SYN challenge can still use the remaining slot.
	if a := mustProcess(t, v, 10, Segment{Seq: 1050, SYN: true, ACK: true}); a != AckChallenge {
		t.Fatalf("SYN#1: got %s, want AckChallenge", a)
	}
	if s := v.Snapshot(); s.Count != 2 || s.CountRST != 1 {
		t.Fatalf("after SYN#1: cnt=%d cntR=%d, want 2/1", s.Count, s.CountRST)
	}
	if a := mustProcess(t, v, 20, Segment{Seq: 1050, SYN: true, ACK: true}); a != Suppressed {
		t.Fatalf("SYN#2: got %s, want Suppressed", a)
	}
}

// C=3 gives an RST quota of ceil(3/2)=2: two RST challenges pass, the
// third is suppressed while a SYN can still use the third slot.
func TestRSTQuotaC3(t *testing.T) {
	p := baseParams()
	p.Limit = 3
	p.Period = 100000
	v := mustNew(t, p)
	rst := func(seq uint32) Segment { return Segment{Seq: seq, RST: true} }

	if a := mustProcess(t, v, 0, rst(1050)); a != AckChallenge {
		t.Fatalf("RST#1: got %s, want AckChallenge", a)
	}
	if a := mustProcess(t, v, 1, rst(1060)); a != AckChallenge {
		t.Fatalf("RST#2: got %s, want AckChallenge", a)
	}
	if a := mustProcess(t, v, 2, rst(1070)); a != Suppressed {
		t.Fatalf("RST#3: got %s, want Suppressed", a)
	}
	if a := mustProcess(t, v, 3, Segment{Seq: 1050, SYN: true, ACK: true}); a != AckChallenge {
		t.Fatalf("SYN: got %s, want AckChallenge (third slot)", a)
	}
	if s := v.Snapshot(); s.Count != 3 || s.CountRST != 2 {
		t.Fatalf("final: cnt=%d cntR=%d, want 3/2", s.Count, s.CountRST)
	}
}

// Drop and AckPlain never touch the rate limiter.
func TestDropAndAckPlainSpareLimiter(t *testing.T) {
	p := baseParams()
	p.Limit = 1
	p.Period = 100000
	v := mustNew(t, p)

	mustProcess(t, v, 0, Segment{Seq: 5000, RST: true})         // Drop
	mustProcess(t, v, 1, Segment{Seq: 5000, Len: 5, ACK: true}) // AckPlain
	mustProcess(t, v, 2, Segment{Seq: 1050})                    // Drop (no ACK flag)
	if s := v.Snapshot(); s.HasWindow || s.Count != 0 || s.CountRST != 0 {
		t.Fatalf("limiter touched: hasWs=%v cnt=%d cntR=%d", s.HasWindow, s.Count, s.CountRST)
	}
	// The full quota is still available afterwards.
	if a := mustProcess(t, v, 3, Segment{Seq: 1050, SYN: true, ACK: true}); a != AckChallenge {
		t.Fatalf("SYN after Drop/AckPlain: got %s, want AckChallenge", a)
	}
}

// Constructor parameter validation.
func TestParamsValidation(t *testing.T) {
	ok := baseParams()
	if _, err := NewValidator(ok); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	bad := []Params{
		func() Params { p := ok; p.RcvWnd = MaxWindow + 1; return p }(),
		func() Params { p := ok; p.MaxSndWnd = MaxWindow + 1; return p }(),
		func() Params { p := ok; p.SndUna = 0; p.SndNxt = MaxWindow + 1; return p }(),
		func() Params { p := ok; p.Limit = 0; return p }(),
		func() Params { p := ok; p.Limit = 1001; return p }(),
		func() Params { p := ok; p.Period = 0; return p }(),
		func() Params { p := ok; p.Period = MaxPeriod + 1; return p }(),
	}
	for i, p := range bad {
		if _, err := NewValidator(p); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("bad params #%d: err=%v, want ErrInvalidParam", i, err)
		}
	}
	// Boundary values are legal.
	for _, p := range []Params{
		func() Params { p := ok; p.RcvWnd = MaxWindow; return p }(),
		func() Params { p := ok; p.SndUna = 0; p.SndNxt = MaxWindow; return p }(),
		func() Params { p := ok; p.Limit = 1; p.Period = 1; return p }(),
	} {
		if _, err := NewValidator(p); err != nil {
			t.Fatalf("boundary params rejected: %v", err)
		}
	}
}

// Rejection reasons are distinguishable, reported in a fixed order, and
// a rejected call changes no state (including the clock).
func TestRejections(t *testing.T) {
	v := mustNew(t, baseParams())
	mustProcess(t, v, 100, Segment{Seq: 1000, ACK: true}) // accepted, lastNow=100

	// Invalid segment field.
	if _, err := v.Process(101, Segment{Seq: 1000, Len: MaxWindow + 1, ACK: true}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Len overflow: %v", err)
	}
	if _, err := v.Process(101, Segment{Seq: 1000, Wnd: MaxWindow + 1, ACK: true}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Wnd overflow: %v", err)
	}
	if _, err := v.Process(101, Segment{Seq: 1000, SYN: true, RST: true}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("SYN+RST: %v", err)
	}
	if _, err := v.Process(MaxClock+1, Segment{Seq: 1000, ACK: true}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("now overflow: %v", err)
	}
	// Invalid param wins over backwards clock.
	if _, err := v.Process(50, Segment{Seq: 1000, SYN: true, RST: true}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("invalid+backwards: %v, want ErrInvalidParam first", err)
	}
	// Backwards clock.
	if _, err := v.Process(99, Segment{Seq: 1000, ACK: true}); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("backwards clock: %v", err)
	}
	// Rejected calls did not move the clock: now=100 is still accepted.
	if a := mustProcess(t, v, 100, Segment{Seq: 1000, ACK: true}); a != Accepted {
		t.Fatalf("after rejections: got %s, want Accepted (clock untouched)", a)
	}

	// Reset closes the connection; backwards clock still wins over Closed.
	if a := mustProcess(t, v, 200, Segment{Seq: 1000, RST: true}); a != Reset {
		t.Fatalf("RST: got %s, want Reset", a)
	}
	if _, err := v.Process(150, Segment{Seq: 1000, ACK: true}); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("closed+backwards: %v, want ErrClockBackwards first", err)
	}
	if _, err := v.Process(200, Segment{Seq: 1000, ACK: true}); !errors.Is(err, ErrAlreadyClosed) {
		t.Fatalf("closed: %v, want ErrAlreadyClosed", err)
	}
}

// Replaying the same segment sequence yields identical actions and state.
func TestReplayDeterminism(t *testing.T) {
	p := baseParams()
	p.Limit = 3
	segs := []Segment{
		{Seq: 1050, RST: true},
		{Seq: 1000, Len: 10, ACK: true},
		{Seq: 1050, SYN: true, ACK: true},
		{Seq: 990, Len: 20, ACK: true},
		{Seq: 1060, RST: true},
		{Seq: 700, Len: 5, ACK: true},
	}
	run := func() ([]Action, Snapshot) {
		v := mustNew(t, p)
		acts := make([]Action, len(segs))
		for i, s := range segs {
			acts[i] = mustProcess(t, v, uint64(i), s)
		}
		return acts, v.Snapshot()
	}
	a1, s1 := run()
	a2, s2 := run()
	for i := range a1 {
		if a1[i] != a2[i] {
			t.Fatalf("replay diverged at step %d: %s vs %s", i, a1[i], a2[i])
		}
	}
	if s1 != s2 {
		t.Fatalf("replay state diverged: %+v vs %+v", s1, s2)
	}
}

// Concurrent Process and Snapshot calls are race-free and keep the
// limiter invariants.
func TestConcurrency(t *testing.T) {
	p := baseParams()
	p.Limit = 10
	p.Period = 100
	v := mustNew(t, p)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 500; i++ {
				now := uint64(rng.Intn(100000))
				seg := Segment{Seq: 1000 + uint32(rng.Intn(200)), SYN: true, ACK: true}
				_, _ = v.Process(now, seg)
				s := v.Snapshot()
				if s.Count > p.Limit {
					t.Errorf("cnt=%d exceeds C=%d", s.Count, p.Limit)
				}
				if s.CountRST > (p.Limit+1)/2 {
					t.Errorf("cntR=%d exceeds ceil(C/2)", s.CountRST)
				}
			}
		}(g)
	}
	wg.Wait()
}
