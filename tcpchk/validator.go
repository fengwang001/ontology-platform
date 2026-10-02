// Package tcpchk implements an RFC 5961 style validity checker for TCP
// segments arriving on an established connection.
//
// All sequence arithmetic is modulo 2^32. Positions are always compared
// via relative offsets (x - base) mod 2^32: an offset < 2^31 places x to
// the right of (or at) base, an offset >= 2^31 places x to the left.
package tcpchk

import (
	"errors"
	"fmt"
	"sync"
)

// Limits imposed by the specification.
const (
	MaxWindow = uint32(1) << 30 // rcvWnd / maxSndWnd / Len / Wnd upper bound
	MaxClock  = uint64(1_000_000_000_000)
	MaxLimit  = uint64(1000) // challenge ACK quota C upper bound
	MaxPeriod = uint64(1_000_000)
	halfSpace = uint32(1) << 31
)

// Distinguishable rejection reasons. The first matching reason in the
// order ErrInvalidParam, ErrClockBackwards, ErrAlreadyClosed is reported.
var (
	ErrInvalidParam   = errors.New("tcpchk: invalid parameter")
	ErrClockBackwards = errors.New("tcpchk: clock moved backwards")
	ErrAlreadyClosed  = errors.New("tcpchk: connection is closed")
)

// State is the connection state.
type State int

const (
	Open State = iota
	Closed
)

func (s State) String() string {
	if s == Closed {
		return "Closed"
	}
	return "Open"
}

// Action is the outcome of an accepted Process call.
type Action int

const (
	Drop Action = iota
	AckPlain
	AckChallenge
	Suppressed
	Reset
	Accepted
)

func (a Action) String() string {
	switch a {
	case Drop:
		return "Drop"
	case AckPlain:
		return "AckPlain"
	case AckChallenge:
		return "AckChallenge"
	case Suppressed:
		return "Suppressed"
	case Reset:
		return "Reset"
	case Accepted:
		return "Accepted"
	}
	return "Unknown"
}

// Segment describes one incoming TCP segment.
type Segment struct {
	Seq uint32
	Ack uint32
	Len uint32 // payload length, 0..2^30
	Wnd uint32 // advertised window, 0..2^30
	SYN bool
	ACK bool
	RST bool
	FIN bool
}

// Params are the constructor parameters of a Validator.
type Params struct {
	RcvNxt    uint32 // next expected receive sequence number
	RcvWnd    uint32 // receive window, 0..2^30
	SndUna    uint32 // oldest unacknowledged sequence number
	SndNxt    uint32 // next send sequence number
	MaxSndWnd uint32 // max window ever advertised by the peer, 0..2^30
	Limit     uint64 // challenge ACK quota C, 1..1000
	Period    uint64 // rate limit window P in milliseconds, 1..10^6
}

func (p Params) validate() error {
	if p.RcvWnd > MaxWindow {
		return fmt.Errorf("%w: rcvWnd %d exceeds 2^30", ErrInvalidParam, p.RcvWnd)
	}
	if p.MaxSndWnd > MaxWindow {
		return fmt.Errorf("%w: maxSndWnd %d exceeds 2^30", ErrInvalidParam, p.MaxSndWnd)
	}
	if p.SndNxt-p.SndUna > MaxWindow {
		return fmt.Errorf("%w: sndNxt-sndUna exceeds 2^30", ErrInvalidParam)
	}
	if p.Limit < 1 || p.Limit > MaxLimit {
		return fmt.Errorf("%w: limit %d out of [1,1000]", ErrInvalidParam, p.Limit)
	}
	if p.Period < 1 || p.Period > MaxPeriod {
		return fmt.Errorf("%w: period %d out of [1,10^6]", ErrInvalidParam, p.Period)
	}
	return nil
}

func (s Segment) validate() error {
	if s.Len > MaxWindow {
		return fmt.Errorf("%w: seg.Len %d exceeds 2^30", ErrInvalidParam, s.Len)
	}
	if s.Wnd > MaxWindow {
		return fmt.Errorf("%w: seg.Wnd %d exceeds 2^30", ErrInvalidParam, s.Wnd)
	}
	if s.SYN && s.RST {
		return fmt.Errorf("%w: SYN and RST both set", ErrInvalidParam)
	}
	return nil
}

// seqLen is the sequence space length L = Len + SYN + FIN.
func (s Segment) seqLen() uint32 {
	l := s.Len
	if s.SYN {
		l++
	}
	if s.FIN {
		l++
	}
	return l
}

// Validator checks incoming segments for one established connection.
// It is safe for concurrent use; results equal some serial order.
type Validator struct {
	mu sync.Mutex

	rcvNxt    uint32
	rcvWnd    uint32
	sndUna    uint32
	sndNxt    uint32
	maxSndWnd uint32
	limit     uint64
	period    uint64
	rstQuota  uint64 // ceil(limit/2)

	state State

	hasWs bool
	ws    uint64 // rate limit window start
	cnt   uint64 // challenges sent in current window
	cntR  uint64 // of which triggered by RST

	hasLast bool
	lastNow uint64 // now of the last accepted call
}

// NewValidator constructs a Validator, validating the parameters.
func NewValidator(p Params) (*Validator, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &Validator{
		rcvNxt:    p.RcvNxt,
		rcvWnd:    p.RcvWnd,
		sndUna:    p.SndUna,
		sndNxt:    p.SndNxt,
		maxSndWnd: p.MaxSndWnd,
		limit:     p.Limit,
		period:    p.Period,
		rstQuota:  (p.Limit + 1) / 2,
		state:     Open,
	}, nil
}

// Snapshot is a consistent view of the validator state.
type Snapshot struct {
	State       State
	RcvNxt      uint32
	RcvWnd      uint32
	SndUna      uint32
	SndNxt      uint32
	MaxSndWnd   uint32
	HasWindow   bool
	WindowStart uint64
	Count       uint64
	CountRST    uint64
	LastNow     uint64
	HasLast     bool
}

// Snapshot returns a consistent copy of the current state.
func (v *Validator) Snapshot() Snapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	return Snapshot{
		State:       v.state,
		RcvNxt:      v.rcvNxt,
		RcvWnd:      v.rcvWnd,
		SndUna:      v.sndUna,
		SndNxt:      v.sndNxt,
		MaxSndWnd:   v.maxSndWnd,
		HasWindow:   v.hasWs,
		WindowStart: v.ws,
		Count:       v.cnt,
		CountRST:    v.cntR,
		LastNow:     v.lastNow,
		HasLast:     v.hasLast,
	}
}

// Process evaluates one incoming segment at time now (milliseconds).
//
// Rejected calls (invalid parameters, backwards clock, closed connection)
// change no state, including the clock and the rate limiter. Accepted
// calls (any Action) advance the clock.
func (v *Validator) Process(now uint64, seg Segment) (Action, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Rejection checks, in the mandated order; only the first reason is
	// reported and no state changes on rejection.
	if now > MaxClock {
		return Drop, fmt.Errorf("%w: now %d exceeds 10^12", ErrInvalidParam, now)
	}
	if err := seg.validate(); err != nil {
		return Drop, err
	}
	if v.hasLast && now < v.lastNow {
		return Drop, fmt.Errorf("%w: now %d < last accepted now %d", ErrClockBackwards, now, v.lastNow)
	}
	if v.state == Closed {
		return Drop, ErrAlreadyClosed
	}

	action := v.process(now, seg)
	v.lastNow = now
	v.hasLast = true
	return action, nil
}

// process runs the fixed-order decision pipeline. The caller holds the
// lock and has completed the rejection checks.
func (v *Validator) process(now uint64, seg Segment) Action {
	// (1) RST segments: payload length is ignored, judged with L=0.
	if seg.RST {
		if !v.acceptable(seg.Seq, 0) {
			return Drop
		}
		if seg.Seq == v.rcvNxt {
			v.state = Closed
			return Reset
		}
		return v.challenge(now, true)
	}

	l := seg.seqLen()

	// (2) Sequence window check; a plain ACK is sent without touching
	// the rate limiter or any sequence state.
	if !v.acceptable(seg.Seq, l) {
		return AckPlain
	}

	// (3) SYN on an acceptable segment triggers a challenge.
	if seg.SYN {
		return v.challenge(now, false)
	}

	// (4) Segments without the ACK flag are dropped silently.
	if !seg.ACK {
		return Drop
	}

	// (5) ACK number range check: acceptable iff
	// (Ack - lo) mod 2^32 <= (sndNxt - lo) mod 2^32 with
	// lo = sndUna - maxSndWnd; the interval is closed at both ends.
	lo := v.sndUna - v.maxSndWnd
	if seg.Ack-lo > v.sndNxt-lo {
		return v.challenge(now, false)
	}

	// (6) All checks passed: advance state and accept.
	if d := seg.Ack - v.sndUna; d != 0 && d <= v.sndNxt-v.sndUna {
		v.sndUna = seg.Ack
	}
	if seg.Wnd > v.maxSndWnd {
		v.maxSndWnd = seg.Wnd
	}
	seqOff := seg.Seq - v.rcvNxt
	endOff := seg.Seq + l - v.rcvNxt
	if (seqOff == 0 || seqOff >= halfSpace) && endOff != 0 && endOff < halfSpace {
		v.rcvNxt = seg.Seq + l
	}
	return Accepted
}

// acceptable reports whether a segment occupying sequence space
// [seq, seq+L) is acceptable per the receive window rules.
func (v *Validator) acceptable(seq, l uint32) bool {
	if l == 0 {
		if v.rcvWnd == 0 {
			return seq == v.rcvNxt
		}
		return seq-v.rcvNxt < v.rcvWnd
	}
	if v.rcvWnd == 0 {
		return false
	}
	// First or last byte inside the window suffices. Offsets >= 2^31
	// lie to the left of rcvNxt and are never inside; since
	// rcvWnd <= 2^30 < 2^31 the comparison excludes them naturally.
	return seq-v.rcvNxt < v.rcvWnd || seq+l-1-v.rcvNxt < v.rcvWnd
}

// challenge runs the global challenge ACK rate limiter. The limiter is
// only advanced when a challenge ACK is actually needed. A suppressed
// challenge leaves ws, cnt and cntR untouched.
func (v *Validator) challenge(now uint64, fromRst bool) Action {
	if !v.hasWs || now-v.ws >= v.period {
		v.ws = now
		v.hasWs = true
		v.cnt = 0
		v.cntR = 0
	}
	if v.cnt < v.limit && (!fromRst || v.cntR < v.rstQuota) {
		v.cnt++
		if fromRst {
			v.cntR++
		}
		return AckChallenge
	}
	return Suppressed
}
