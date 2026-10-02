package tcpchk

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// This file holds a naive, step-by-step simulation written directly
// from the specification, plus a randomized differential test that
// replays 2000 random segment sequences against the Validator and
// compares actions, sequence numbers and rate limiter counters.

type reason int

const (
	rOK reason = iota
	rInvalid
	rClock
	rClosed
)

func (r reason) String() string {
	switch r {
	case rInvalid:
		return "invalid-param"
	case rClock:
		return "clock-backwards"
	case rClosed:
		return "closed"
	}
	return "ok"
}

// off computes (x - base) mod 2^32 as a plain integer offset.
func off(x, base uint32) uint64 { return uint64(x - base) }

type model struct {
	rcvNxt, rcvWnd uint32
	sndUna, sndNxt uint32
	maxSndWnd      uint32
	limit, period  uint64
	closed         bool
	hasWs          bool
	ws, cnt, cntR  uint64
	hasLast        bool
	lastNow        uint64
}

func newModel(p Params) *model {
	return &model{
		rcvNxt: p.RcvNxt, rcvWnd: p.RcvWnd,
		sndUna: p.SndUna, sndNxt: p.SndNxt,
		maxSndWnd: p.MaxSndWnd,
		limit:     p.Limit, period: p.Period,
	}
}

func (m *model) acc(seq, l uint32) bool {
	if l == 0 {
		if m.rcvWnd == 0 {
			return seq == m.rcvNxt
		}
		return off(seq, m.rcvNxt) < uint64(m.rcvWnd)
	}
	if m.rcvWnd == 0 {
		return false
	}
	return off(seq, m.rcvNxt) < uint64(m.rcvWnd) ||
		off(seq+l-1, m.rcvNxt) < uint64(m.rcvWnd)
}

func (m *model) challenge(now uint64, fromRst bool) Action {
	if !m.hasWs || now-m.ws >= m.period {
		m.ws, m.hasWs, m.cnt, m.cntR = now, true, 0, 0
	}
	if m.cnt < m.limit && (!fromRst || m.cntR < (m.limit+1)/2) {
		m.cnt++
		if fromRst {
			m.cntR++
		}
		return AckChallenge
	}
	return Suppressed
}

func (m *model) step(now uint64, seg Segment) (Action, reason) {
	if now > MaxClock || seg.Len > MaxWindow || seg.Wnd > MaxWindow || (seg.SYN && seg.RST) {
		return Drop, rInvalid
	}
	if m.hasLast && now < m.lastNow {
		return Drop, rClock
	}
	if m.closed {
		return Drop, rClosed
	}
	act := m.decide(now, seg)
	m.lastNow, m.hasLast = now, true
	return act, rOK
}

func (m *model) decide(now uint64, seg Segment) Action {
	// (1) RST: judged with L=0, payload ignored.
	if seg.RST {
		if !m.acc(seg.Seq, 0) {
			return Drop
		}
		if seg.Seq == m.rcvNxt {
			m.closed = true
			return Reset
		}
		return m.challenge(now, true)
	}
	// L = Len + SYN + FIN.
	l := seg.Len
	if seg.SYN {
		l++
	}
	if seg.FIN {
		l++
	}
	// (2) sequence window.
	if !m.acc(seg.Seq, l) {
		return AckPlain
	}
	// (3) SYN.
	if seg.SYN {
		return m.challenge(now, false)
	}
	// (4) ACK flag.
	if !seg.ACK {
		return Drop
	}
	// (5) ACK number in the closed interval [sndUna-maxSndWnd, sndNxt].
	lo := m.sndUna - m.maxSndWnd
	if off(seg.Ack, lo) > off(m.sndNxt, lo) {
		return m.challenge(now, false)
	}
	// (6) pass: advance state.
	if d := off(seg.Ack, m.sndUna); d != 0 && d <= off(m.sndNxt, m.sndUna) {
		m.sndUna = seg.Ack
	}
	if seg.Wnd > m.maxSndWnd {
		m.maxSndWnd = seg.Wnd
	}
	seqOff := off(seg.Seq, m.rcvNxt)
	endOff := off(seg.Seq+l, m.rcvNxt)
	if (seqOff == 0 || seqOff >= 1<<31) && endOff != 0 && endOff < 1<<31 {
		m.rcvNxt = seg.Seq + l
	}
	return Accepted
}

func checkReason(err error, r reason) bool {
	switch r {
	case rOK:
		return err == nil
	case rInvalid:
		return errors.Is(err, ErrInvalidParam)
	case rClock:
		return errors.Is(err, ErrClockBackwards)
	case rClosed:
		return errors.Is(err, ErrAlreadyClosed)
	}
	return false
}

func randWindow(rng *rand.Rand) uint32 {
	switch rng.Intn(4) {
	case 0:
		return 0
	case 1:
		return uint32(rng.Intn(200))
	default:
		return uint32(rng.Int63n(int64(MaxWindow) + 1))
	}
}

func randParams(rng *rand.Rand) Params {
	sndUna := rng.Uint32()
	return Params{
		RcvNxt:    rng.Uint32(),
		RcvWnd:    randWindow(rng),
		SndUna:    sndUna,
		SndNxt:    sndUna + uint32(rng.Int63n(int64(MaxWindow)+1)),
		MaxSndWnd: randWindow(rng),
		Limit:     uint64(1 + rng.Intn(20)),
		Period:    uint64(1 + rng.Intn(2000)),
	}
}

func randSegment(rng *rand.Rand, p Params) Segment {
	var seg Segment
	// Seq: usually near rcvNxt, sometimes anywhere in the space.
	switch rng.Intn(4) {
	case 0, 1, 2:
		seg.Seq = p.RcvNxt + uint32(rng.Intn(601)) - 300
	default:
		seg.Seq = rng.Uint32()
	}
	// Ack: usually near sndUna/sndNxt, sometimes anywhere.
	switch rng.Intn(3) {
	case 0, 1:
		seg.Ack = p.SndUna + uint32(rng.Intn(1201)) - 600
	default:
		seg.Ack = rng.Uint32()
	}
	switch rng.Intn(4) {
	case 0:
		seg.Len = 0
	case 1:
		seg.Len = uint32(rng.Intn(64))
	default:
		seg.Len = uint32(rng.Int63n(int64(MaxWindow) + 1))
	}
	seg.Wnd = randWindow(rng)
	seg.SYN = rng.Intn(4) == 0
	seg.ACK = rng.Intn(2) == 0
	seg.RST = rng.Intn(5) == 0
	seg.FIN = rng.Intn(8) == 0
	if rng.Intn(50) == 0 { // occasionally invalid
		seg.SYN, seg.RST = true, true
	}
	if rng.Intn(50) == 0 { // occasionally out of range
		seg.Len = MaxWindow + 1
	}
	return seg
}

// Differential test: 2000 random segment sequences, replayed step by
// step against the naive model. Inputs, outputs and the decision basis
// are logged for every step.
func TestAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261229))
	const trials = 2000

	for trial := 0; trial < trials; trial++ {
		p := randParams(rng)
		v, err := NewValidator(p)
		if err != nil {
			t.Fatalf("trial %d: NewValidator(%+v): %v", trial, p, err)
		}
		m := newModel(p)

		now := uint64(rng.Intn(100))
		steps := 1 + rng.Intn(30)
		for step := 0; step < steps; step++ {
			switch rng.Intn(20) {
			case 0: // occasionally rewind the clock
				if now > 0 {
					now -= uint64(rng.Intn(int(now)))
				}
			case 1: // occasionally jump beyond the clock bound
				now = MaxClock + 1
			default:
				now += uint64(rng.Intn(3000))
				if now > MaxClock {
					now = MaxClock
				}
			}
			seg := randSegment(rng, p)

			gotAct, gotErr := v.Process(now, seg)
			wantAct, wantReason := m.step(now, seg)

			s := v.Snapshot()
			t.Logf("trial=%d step=%d now=%d seg=%+v -> action=%s err=%v "+
				"| basis: rcvNxt=%d rcvWnd=%d sndUna=%d sndNxt=%d maxSndWnd=%d "+
				"ws=(%v,%d) cnt=%d cntR=%d state=%s",
				trial, step, now, seg, gotAct, gotErr,
				s.RcvNxt, s.RcvWnd, s.SndUna, s.SndNxt, s.MaxSndWnd,
				s.HasWindow, s.WindowStart, s.Count, s.CountRST, s.State)

			if !checkReason(gotErr, wantReason) {
				t.Fatalf("trial %d step %d: err=%v, want reason %s", trial, step, gotErr, wantReason)
			}
			if gotAct != wantAct {
				t.Fatalf("trial %d step %d: action=%s, want %s (now=%d seg=%+v)",
					trial, step, gotAct, wantAct, now, seg)
			}
			if s.RcvNxt != m.rcvNxt || s.SndUna != m.sndUna || s.MaxSndWnd != m.maxSndWnd {
				t.Fatalf("trial %d step %d: state rcvNxt=%d sndUna=%d maxSndWnd=%d, want %d/%d/%d",
					trial, step, s.RcvNxt, s.SndUna, s.MaxSndWnd, m.rcvNxt, m.sndUna, m.maxSndWnd)
			}
			if s.Count != m.cnt || s.CountRST != m.cntR || s.WindowStart != m.ws || s.HasWindow != m.hasWs {
				t.Fatalf("trial %d step %d: limiter cnt=%d cntR=%d ws=%d, want %d/%d/%d",
					trial, step, s.Count, s.CountRST, s.WindowStart, m.cnt, m.cntR, m.ws)
			}
			if (s.State == Closed) != m.closed {
				t.Fatalf("trial %d step %d: state=%s, model closed=%v", trial, step, s.State, m.closed)
			}
			// Global invariants on every accepted call.
			if s.Count > p.Limit {
				t.Fatalf("trial %d step %d: cnt=%d exceeds C=%d", trial, step, s.Count, p.Limit)
			}
			if s.CountRST > (p.Limit+1)/2 {
				t.Fatalf("trial %d step %d: cntR=%d exceeds ceil(C/2)", trial, step, s.CountRST)
			}
			if s.CountRST > s.Count {
				t.Fatalf("trial %d step %d: cntR=%d > cnt=%d", trial, step, s.CountRST, s.Count)
			}
		}
	}
	fmt.Printf("differential test: %d random sequences matched the naive model\n", trials)
}
