package rfc5961

import (
	"fmt"
	"sync"
	"testing"
)

// Minimal deterministic xorshift generator so traces are reproducible
// without pulling in math/rand global seeding.
type rng struct {
	state uint64
}

func newRng(seed uint64) *rng {
	if seed == 0 {
		seed = 1
	}
	return &rng{state: seed}
}

func (r *rng) u64() uint64 {
	x := r.state
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	r.state = x
	return x
}

func (r *rng) intn(n int) int { return int(r.u64() % uint64(n)) }

type traceCall struct {
	now int64
	seg Segment
}

func randomSegment(r *rng) Segment {
	seg := Segment{
		Seq: uint32(r.u64()),
		Ack: uint32(r.u64() >> 16),
		Len: uint64(r.intn(1 << 14)),
		Wnd: uint64(r.intn(1 << 14)),
	}
	switch r.intn(10) {
	case 0, 1, 2:
		seg.RST = true
	case 3, 4:
		seg.SYN = true
	}
	if r.intn(2) == 0 {
		seg.FIN = true
	}
	if !seg.SYN && r.intn(4) != 0 {
		seg.ACK = true
	}
	return seg
}

// modelState is a naive, step-by-step transcription of the specification
// used as an independent oracle for the implementation.
type modelState struct {
	rcvNxt    uint32
	rcvWnd    uint64
	sndUna    uint32
	sndNxt    uint32
	maxSndWnd uint64
	quotaC    int
	windowP   int64
	rstQuota  int

	closed      bool
	haveLastNow bool
	lastNow     int64
	wsValid     bool
	ws          int64
	cnt         int
	cntR        int
}

func modelFromConfig(c Config) modelState {
	return modelState{
		rcvNxt:    c.RcvNxt,
		rcvWnd:    c.RcvWnd,
		sndUna:    c.SndUna,
		sndNxt:    c.SndNxt,
		maxSndWnd: c.MaxSndWnd,
		quotaC:    c.QuotaC,
		windowP:   c.WindowP,
		rstQuota:  (c.QuotaC + 1) / 2,
	}
}

func (m *modelState) acceptable(seq uint32, length uint64) bool {
	if m.rcvWnd == 0 {
		return length == 0 && seq == m.rcvNxt
	}
	if uint64(seq-m.rcvNxt) < m.rcvWnd {
		return true
	}
	if length > 0 {
		last := seq + uint32(length-1)
		if uint64(last-m.rcvNxt) < m.rcvWnd {
			return true
		}
	}
	return false
}

func (m *modelState) challenge(now int64, fromRST bool) Action {
	if !m.wsValid || now-m.ws >= m.windowP {
		m.wsValid = true
		m.ws = now
		m.cnt = 0
		m.cntR = 0
	}
	if m.cnt >= m.quotaC {
		return Suppressed
	}
	if fromRST && m.cntR >= m.rstQuota {
		return Suppressed
	}
	m.cnt++
	if fromRST {
		m.cntR++
	}
	return AckChallenge
}

func (m *modelState) process(now int64, seg Segment) (Action, string) {
	length := seg.Len
	if seg.SYN {
		length++
	}
	if seg.FIN {
		length++
	}

	if seg.RST {
		if !m.acceptable(seg.Seq, 0) {
			return Drop, "RST seq outside receive window (L=0)"
		}
		if seg.Seq == m.rcvNxt {
			m.closed = true
			return Reset, "RST seq == rcvNxt"
		}
		a := m.challenge(now, true)
		return a, "RST in window but seq != rcvNxt (RST challenge)"
	}

	if !m.acceptable(seg.Seq, length) {
		return AckPlain, "seq range outside receive window (plain ACK)"
	}
	if seg.SYN {
		a := m.challenge(now, false)
		return a, "acceptable SYN (challenge)"
	}
	if !seg.ACK {
		return Drop, "acceptable non-SYN segment without ACK"
	}

	lo := m.sndUna - uint32(m.maxSndWnd)
	if uint64(seg.Ack-lo) > uint64(m.sndNxt-lo) {
		a := m.challenge(now, false)
		return a, fmt.Sprintf("Ack %d outside closed range [%d,%d] (challenge)", seg.Ack, lo, m.sndNxt)
	}

	if off := uint64(seg.Ack - m.sndUna); off > 0 && off <= uint64(m.sndNxt-m.sndUna) {
		m.sndUna = seg.Ack
	}
	if seg.Wnd > m.maxSndWnd {
		m.maxSndWnd = seg.Wnd
	}
	startOff := uint64(seg.Seq - m.rcvNxt)
	if startOff == 0 || startOff >= 1<<31 {
		end := seg.Seq + uint32(length)
		endOff := uint64(end - m.rcvNxt)
		if endOff > 0 && endOff < 1<<31 {
			m.rcvNxt = end
		}
	}
	return Accepted, "all checks passed"
}

func randomConfig(r *rng) Config {
	sndUna := uint32(r.u64())
	flight := uint64(r.intn(1 << 16))
	return Config{
		RcvNxt:    uint32(r.u64() >> 11),
		RcvWnd:    uint64(r.intn(1 << 14)),
		SndUna:    sndUna,
		SndNxt:    sndUna + uint32(flight),
		MaxSndWnd: uint64(r.intn(1 << 14)),
		QuotaC:    1 + r.intn(6),
		WindowP:   int64(1 + r.intn(2000)),
	}
}

// TestNaiveModelDifferential replays 2000 random segment sequences
// through both the implementation and the independent naive model and
// requires the action and every piece of observable state to agree.
func TestNaiveModelDifferential(t *testing.T) {
	const sequences = 2000
	const traceLen = 10

	for s := 0; s < sequences; s++ {
		r := newRng(uint64(s*7919 + 42))
		cfg := randomConfig(r)
		v, err := New(cfg)
		if err != nil {
			t.Fatalf("sequence %d: New: %v", s, err)
		}
		m := modelFromConfig(cfg)

		var now int64
		for i := 0; i < traceLen; i++ {
			now += int64(r.intn(int(cfg.WindowP) + 1500))
			seg := randomSegment(r)

			got, gerr := v.Process(now, seg)
			want, reason := m.process(now, seg)
			m.haveLastNow = true
			m.lastNow = now

			flags := flagString(seg)
			t.Logf("seq=%d step=%d now=%d seg={Seq=%#x Ack=%#x Len=%d Wnd=%d %s L=%d} -> %v | model=%v | %s",
				s, i, now, seg.Seq, seg.Ack, seg.Len, seg.Wnd, flags, seg.seqSpace(), got, want, reason)

			if gerr != nil {
				t.Fatalf("sequence %d step %d: unexpected error %v", s, i, gerr)
			}
			if got != want {
				t.Fatalf("sequence %d step %d: action %v != model %v (%s)", s, i, got, want, reason)
			}
			if v.RcvNxt() != m.rcvNxt {
				t.Fatalf("sequence %d step %d: rcvNxt %#x != model %#x", s, i, v.RcvNxt(), m.rcvNxt)
			}
			if v.SndUna() != m.sndUna {
				t.Fatalf("sequence %d step %d: sndUna %#x != model %#x", s, i, v.SndUna(), m.sndUna)
			}
			if v.MaxSndWnd() != m.maxSndWnd {
				t.Fatalf("sequence %d step %d: maxSndWnd %d != model %d", s, i, v.MaxSndWnd(), m.maxSndWnd)
			}
			if v.Closed() != m.closed {
				t.Fatalf("sequence %d step %d: closed %v != model %v", s, i, v.Closed(), m.closed)
			}
			cnt, cntR := v.Count()
			if cnt != m.cnt || cntR != m.cntR {
				t.Fatalf("sequence %d step %d: counters (%d,%d) != model (%d,%d)", s, i, cnt, cntR, m.cnt, m.cntR)
			}
			ws, ok := v.WindowStart()
			if ok != m.wsValid || (ok && ws != m.ws) {
				t.Fatalf("sequence %d step %d: window (%d,%v) != model (%d,%v)", s, i, ws, ok, m.ws, m.wsValid)
			}
			if got == Reset || m.closed {
				break // the remainder of the trace would be rejected as Closed
			}
		}
	}
}

func flagString(seg Segment) string {
	var flags string
	if seg.SYN {
		flags += "SYN "
	}
	if seg.ACK {
		flags += "ACK "
	}
	if seg.RST {
		flags += "RST "
	}
	if seg.FIN {
		flags += "FIN "
	}
	if flags == "" {
		flags = "-"
	}
	return flags
}

// Concurrent calls must be race-free and preserve limiter invariants;
// exactly one Reset can win on a fresh connection.
func TestConcurrent(t *testing.T) {
	t.Run("challenges", func(t *testing.T) {
		v := mustNew(t, Config{
			RcvNxt: 1000, RcvWnd: 100,
			SndUna: 5000, SndNxt: 5200, MaxSndWnd: 300,
			QuotaC: 4, WindowP: 1_000_000,
		})
		var wg sync.WaitGroup
		for i := 0; i < 64; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = v.Process(0, Segment{Seq: 1050, SYN: true})
			}()
		}
		wg.Wait()
		cnt, cntR := v.Count()
		if cnt > 4 || cntR > 2 {
			t.Fatalf("invariants violated: cnt=%d cntR=%d", cnt, cntR)
		}
		if ws, ok := v.WindowStart(); !ok || ws != 0 {
			t.Fatalf("window start: got (%d,%v)", ws, ok)
		}
	})

	t.Run("reset race", func(t *testing.T) {
		v := mustNew(t, baseCfg())
		var wg sync.WaitGroup
		var resets, closedErrs int
		var mu sync.Mutex
		for i := 0; i < 64; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				a, err := v.Process(0, rst(1000))
				mu.Lock()
				switch {
				case a == Reset:
					resets++
				case err != nil && err == ErrClosed:
					closedErrs++
				}
				mu.Unlock()
			}()
		}
		wg.Wait()
		if resets != 1 || resets+closedErrs != 64 {
			t.Fatalf("resets=%d closedErrs=%d", resets, closedErrs)
		}
	})
}
