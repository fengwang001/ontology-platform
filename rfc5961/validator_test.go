package rfc5961

import (
	"errors"
	"fmt"
	"testing"
)

func mustNew(t *testing.T, c Config) *Validator {
	t.Helper()
	v, err := New(c)
	if err != nil {
		t.Fatalf("New(%+v): %v", c, err)
	}
	return v
}

func baseCfg() Config {
	return Config{
		RcvNxt:    1000,
		RcvWnd:    100,
		SndUna:    5000,
		SndNxt:    5200,
		MaxSndWnd: 300,
		QuotaC:    2,
		WindowP:   1000,
	}
}

func rst(seq uint32) Segment { return Segment{Seq: seq, RST: true} }

// RST three-way check: out of window -> Drop, exact rcvNxt -> Reset,
// elsewhere in window -> challenge ACK.
func TestRSTThreeWay(t *testing.T) {
	cases := []struct {
		name string
		seq  uint32
		want Action
	}{
		{"exact rcvNxt", 1000, Reset},
		{"inside window", 1050, AckChallenge},
		{"last in window", 1099, AckChallenge},
		{"one past window", 1100, Drop},
		{"one before window", 999, Drop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := mustNew(t, baseCfg())
			got, err := v.Process(0, rst(tc.seq))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if got != tc.want {
				t.Fatalf("RST seq=%d: got %v want %v", tc.seq, got, tc.want)
			}
			if tc.want == Reset && !v.Closed() {
				t.Fatalf("Reset did not close the connection")
			}
		})
	}
}

// A closed window accepts RST only at the exact next sequence number.
func TestRSTZeroWindow(t *testing.T) {
	cfg := baseCfg()
	cfg.RcvWnd = 0
	v := mustNew(t, cfg)

	if got, _ := v.Process(0, rst(1000)); got != Reset {
		t.Fatalf("RST seq=1000 with zero window: got %v want Reset", got)
	}

	v = mustNew(t, cfg)
	if got, _ := v.Process(0, rst(1001)); got != Drop {
		t.Fatalf("RST seq=1001 with zero window: got %v want Drop", got)
	}
}

// Sequence-number wrap-around: rcvNxt near 2^32, window crosses zero.
func TestRSTWraparound(t *testing.T) {
	cfg := baseCfg()
	cfg.RcvNxt = 0xFFFFFFF0
	cfg.RcvWnd = 0x20
	v := mustNew(t, cfg)

	got, err := v.Process(0, rst(0x0000000F))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got != AckChallenge {
		t.Fatalf("wrapped RST: got %v want AckChallenge (offset 0x1F in window)", got)
	}
}

// Acceptance of non-RST data segments: first or last byte in window.
func TestAcceptableData(t *testing.T) {
	// Seq=1090, Len=20: first byte in window.
	v := mustNew(t, baseCfg())
	seg := Segment{Seq: 1090, Ack: 5100, Len: 20, ACK: true}
	if got, _ := v.Process(0, seg); got != Accepted {
		t.Fatalf("first byte in window: got %v want Accepted", got)
	}

	// Seq=990, Len=20: last byte 1009 in window; the accepted overlap
	// starts left of rcvNxt and advances rcvNxt to 1010.
	v = mustNew(t, baseCfg())
	seg = Segment{Seq: 990, Ack: 5100, Len: 20, ACK: true}
	if got, _ := v.Process(0, seg); got != Accepted {
		t.Fatalf("last byte in window: got %v want Accepted", got)
	}
	if v.RcvNxt() != 1010 {
		t.Fatalf("rcvNxt: got %d want 1010", v.RcvNxt())
	}

	// Seq=900, Len=20: nowhere near the window -> plain ACK.
	v = mustNew(t, baseCfg())
	seg = Segment{Seq: 900, Ack: 5100, Len: 20, ACK: true}
	if got, _ := v.Process(0, seg); got != AckPlain {
		t.Fatalf("out of window data: got %v want AckPlain", got)
	}

	// Zero window: any data segment is unacceptable -> plain ACK.
	cfg := baseCfg()
	cfg.RcvWnd = 0
	v = mustNew(t, cfg)
	seg = Segment{Seq: 1000, Ack: 5100, Len: 1, ACK: true}
	if got, _ := v.Process(0, seg); got != AckPlain {
		t.Fatalf("data on zero window: got %v want AckPlain", got)
	}
}

// SYN: challenge when in window, plain ACK when outside.
func TestSYN(t *testing.T) {
	v := mustNew(t, baseCfg())
	syn := Segment{Seq: 1050, SYN: true}
	if got, _ := v.Process(0, syn); got != AckChallenge {
		t.Fatalf("in-window SYN: got %v want AckChallenge", got)
	}

	v = mustNew(t, baseCfg())
	syn = Segment{Seq: 1200, SYN: true}
	if got, _ := v.Process(1, syn); got != AckPlain {
		t.Fatalf("out-of-window SYN: got %v want AckPlain", got)
	}
}

// A non-SYN acceptable segment without ACK is dropped.
func TestMissingACK(t *testing.T) {
	v := mustNew(t, baseCfg())
	seg := Segment{Seq: 1000, Len: 1}
	if got, _ := v.Process(0, seg); got != Drop {
		t.Fatalf("acceptable segment without ACK: got %v want Drop", got)
	}
}

// ACK range is the closed interval [sndUna-maxSndWnd, sndNxt].
func TestACKRange(t *testing.T) {
	cases := []struct {
		name string
		ack  uint32
		want Action
		sndU uint32 // expected sndUna afterwards; 0 means unchanged (5000)
	}{
		{"lower boundary", 4700, Accepted, 0},
		{"upper boundary", 5200, Accepted, 5200},
		{"below lower", 4699, AckChallenge, 0},
		{"above upper", 5201, AckChallenge, 0},
		{"advances sndUna", 5100, Accepted, 5100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := mustNew(t, baseCfg())
			seg := Segment{Seq: 1000, Ack: tc.ack, ACK: true}
			got, err := v.Process(0, seg)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Ack=%d: got %v want %v", tc.ack, got, tc.want)
			}
			wantUna := tc.sndU
			if wantUna == 0 {
				wantUna = 5000
			}
			if v.SndUna() != wantUna {
				t.Fatalf("sndUna after Ack=%d: got %d want %d", tc.ack, v.SndUna(), wantUna)
			}
			// Failed ACK checks never move rcvNxt.
			if tc.want == AckChallenge && v.RcvNxt() != 1000 {
				t.Fatalf("challenge moved rcvNxt to %d", v.RcvNxt())
			}
		})
	}
}

// Plain ACKs and drops never touch the rate limiter.
func TestNonChallengesDoNotConsumeQuota(t *testing.T) {
	v := mustNew(t, baseCfg())

	for i := 0; i < 5; i++ {
		if got, _ := v.Process(int64(i), rst(1200)); got != Drop {
			t.Fatalf("out-of-window RST #%d: got %v want Drop", i, got)
		}
		if got, _ := v.Process(int64(i+100), Segment{Seq: 1200, Len: 1, ACK: true}); got != AckPlain {
			t.Fatalf("out-of-window data #%d: got %v want AckPlain", i, got)
		}
	}
	if ws, ok := v.WindowStart(); ok {
		t.Fatalf("limiter window was set to %d despite no challenges", ws)
	}
	if cnt, cntR := v.Count(); cnt != 0 || cntR != 0 {
		t.Fatalf("limiter counters changed: cnt=%d cntR=%d", cnt, cntR)
	}
}

// Window roll-over for SYN-triggered challenges with C=2, P=1000.
func TestChallengeWindowRolls(t *testing.T) {
	v := mustNew(t, baseCfg())
	syn := func() Segment { return Segment{Seq: 1050, SYN: true} }

	steps := []struct {
		now  int64
		want Action
		cnt  int
		cntR int
	}{
		{0, AckChallenge, 1, 0},
		{10, AckChallenge, 2, 0},
		{20, Suppressed, 2, 0},
		{999, Suppressed, 2, 0},
		{1000, AckChallenge, 1, 0}, // new window: 1000-0 >= 1000
	}
	for _, st := range steps {
		got, err := v.Process(st.now, syn())
		if err != nil {
			t.Fatalf("now=%d: %v", st.now, err)
		}
		if got != st.want {
			t.Fatalf("now=%d: got %v want %v", st.now, got, st.want)
		}
		cnt, cntR := v.Count()
		if cnt != st.cnt || cntR != st.cntR {
			t.Fatalf("now=%d counters: got (%d,%d) want (%d,%d)", st.now, cnt, cntR, st.cnt, st.cntR)
		}
	}
}

// RST-triggered challenges are capped at ceil(C/2).
func TestRSTChallengeQuota(t *testing.T) {
	v := mustNew(t, baseCfg()) // C=2 -> ceil(C/2)=1

	if got, _ := v.Process(0, rst(1050)); got != AckChallenge {
		t.Fatalf("first RST challenge: got %v", got)
	}
	// cnt=1 < C=2, but the RST sub-quota is exhausted.
	if got, _ := v.Process(5, rst(1060)); got != Suppressed {
		t.Fatalf("second RST: got %v want Suppressed", got)
	}
	if got, _ := v.Process(10, Segment{Seq: 1050, SYN: true}); got != AckChallenge {
		t.Fatalf("SYN after RST: got %v want AckChallenge", got)
	}
	if got, _ := v.Process(20, Segment{Seq: 1050, SYN: true}); got != Suppressed {
		t.Fatalf("second SYN: got %v want Suppressed", got)
	}
	cnt, cntR := v.Count()
	if cnt != 2 || cntR != 1 {
		t.Fatalf("counters: cnt=%d cntR=%d want (2,1)", cnt, cntR)
	}

	// C=3 -> ceil(C/2)=2: two RSTs fit, the third is suppressed while
	// a SYN still occupies the third slot.
	cfg := baseCfg()
	cfg.QuotaC = 3
	v = mustNew(t, cfg)
	for i, want := range []Action{AckChallenge, AckChallenge, Suppressed} {
		if got, _ := v.Process(int64(i), rst(1050)); got != want {
			t.Fatalf("C=3 RST #%d: got %v want %v", i, got, want)
		}
	}
	if got, _ := v.Process(3, Segment{Seq: 1050, SYN: true}); got != AckChallenge {
		t.Fatalf("C=3 SYN after two RSTs: got %v want AckChallenge", got)
	}
}

func TestRejections(t *testing.T) {
	t.Run("constructor bounds", func(t *testing.T) {
		bad := []Config{
			{MaxSndWnd: maxWnd + 1, QuotaC: 1, WindowP: 1},
			{RcvWnd: maxWnd + 1, QuotaC: 1, WindowP: 1},
			{SndUna: 0, SndNxt: 1 << 31, QuotaC: 1, WindowP: 1},
			{QuotaC: 0, WindowP: 1},
			{QuotaC: 1001, WindowP: 1},
			{QuotaC: 1, WindowP: 0},
			{QuotaC: 1, WindowP: 1_000_001},
		}
		wantErr := []error{
			ErrInvalidMaxWnd,
			ErrInvalidRcvWnd,
			ErrFlightSize,
			ErrChallengeQuota,
			ErrChallengeQuota,
			ErrChallengeWindow,
			ErrChallengeWindow,
		}
		for i, c := range bad {
			if _, err := New(c); !errors.Is(err, wantErr[i]) {
				t.Fatalf("case %d: got %v want %v", i, err, wantErr[i])
			}
		}
	})

	t.Run("segment and clock validation order", func(t *testing.T) {
		v := mustNew(t, baseCfg())

		if _, err := v.Process(-1, rst(1000)); !errors.Is(err, ErrInvalidNow) {
			t.Fatalf("now=-1: got %v want ErrInvalidNow", err)
		}
		if _, err := v.Process(maxTime+1, rst(1000)); !errors.Is(err, ErrInvalidNow) {
			t.Fatalf("now too large: got %v want ErrInvalidNow", err)
		}
		if _, err := v.Process(0, Segment{Len: maxWnd + 1}); !errors.Is(err, ErrInvalidLen) {
			t.Fatalf("Len too large: got %v want ErrInvalidLen", err)
		}
		if _, err := v.Process(0, Segment{Wnd: maxWnd + 1}); !errors.Is(err, ErrInvalidWnd) {
			t.Fatalf("Wnd too large: got %v want ErrInvalidWnd", err)
		}
		if _, err := v.Process(0, Segment{SYN: true, RST: true}); !errors.Is(err, ErrSYNAndRST) {
			t.Fatalf("SYN+RST: got %v want ErrSYNAndRST", err)
		}

		// Rejected calls must not advance the clock: a later valid call
		// at now=0 succeeds, and now=1 is still allowed afterwards.
		valid := Segment{Seq: 1000, Ack: 5100, ACK: true}
		if _, err := v.Process(0, valid); err != nil {
			t.Fatalf("accepted call at 0: %v", err)
		}
		if _, err := v.Process(1, valid); err != nil {
			t.Fatalf("accepted call at 1: %v", err)
		}
		if _, err := v.Process(0, valid); !errors.Is(err, ErrClockRegression) {
			t.Fatalf("regression: got %v want ErrClockRegression", err)
		}
		// A regression is itself rejected and must not move the clock.
		if last, ok := v.LastNow(); !ok || last != 1 {
			t.Fatalf("lastNow after regression: got (%d,%v) want (1,true)", last, ok)
		}
	})

	t.Run("closed connection", func(t *testing.T) {
		v := mustNew(t, baseCfg())
		if got, _ := v.Process(0, rst(1000)); got != Reset {
			t.Fatalf("expected Reset")
		}
		// Same timestamp is not a regression. A valid segment then hits
		// the Closed check (parameter errors still take precedence over it).
		if _, err := v.Process(1, Segment{Seq: 1000, Ack: 5100, ACK: true}); !errors.Is(err, ErrClosed) {
			t.Fatalf("after reset: got %v want ErrClosed", err)
		}
		if _, err := v.Process(1, Segment{SYN: true, RST: true}); !errors.Is(err, ErrSYNAndRST) {
			t.Fatalf("after reset with bad segment: got %v want ErrSYNAndRST", err)
		}
	})
}

// Replaying the same segment sequence reproduces actions and state.
func TestReplayDeterminism(t *testing.T) {
	const traceLen = 100
	rng := newRng(20240917)
	trace := make([]traceCall, traceLen)
	for i := range trace {
		trace[i] = traceCall{now: int64(i) * 7, seg: randomSegment(rng)}
	}

	run := func() string {
		v := mustNew(t, baseCfg())
		var log string
		for _, c := range trace {
			a, err := v.Process(c.now, c.seg)
			log += fmt.Sprint(a, err, v.RcvNxt(), v.SndUna())
		}
		return log
	}
	if run() != run() {
		t.Fatalf("replay produced different results")
	}
}
