// Package rfc5961 implements an RFC 5961 style in-window validation for
// segments arriving on an established TCP connection.
package rfc5961

import (
	"errors"
	"sync"
)

// Domain limits, see package documentation.
const (
	maxWnd      uint64 = 1 << 30
	maxSeqSpace uint64 = 1 << 30
	maxTime     int64  = 1_000_000_000_000
)

// Sentinel errors reported by New and Process. They are deliberately
// distinct so callers can discriminate rejection reasons.
var (
	ErrInvalidRcvWnd   = errors.New("rfc5961: rcvWnd out of range [0, 2^30]")
	ErrInvalidMaxWnd   = errors.New("rfc5961: maxSndWnd out of range [0, 2^30]")
	ErrFlightSize      = errors.New("rfc5961: (sndNxt-sndUna) mod 2^32 must not exceed 2^30")
	ErrChallengeQuota  = errors.New("rfc5961: challenge quota C out of range [1, 1000]")
	ErrChallengeWindow = errors.New("rfc5961: challenge window P out of range [1, 10^6]")
	ErrInvalidNow      = errors.New("rfc5961: now out of range [0, 10^12]")
	ErrInvalidLen      = errors.New("rfc5961: segment Len out of range [0, 2^30]")
	ErrInvalidWnd      = errors.New("rfc5961: segment Wnd out of range [0, 2^30]")
	ErrSYNAndRST       = errors.New("rfc5961: segment has both SYN and RST set")
	ErrClockRegression = errors.New("rfc5961: now precedes the last accepted call")
	ErrClosed          = errors.New("rfc5961: connection is closed")
)

// Action is the disposition produced by Validator.Process.
type Action int

const (
	// Drop silently discards the segment.
	Drop Action = iota
	// AckPlain sends an ordinary ACK without touching the rate limiter.
	AckPlain
	// AckChallenge sends a challenge ACK, charged against the limiter.
	AckChallenge
	// Suppressed is a challenge ACK that the limiter refused to send.
	Suppressed
	// Reset tears the connection down; the validator becomes Closed.
	Reset
	// Accepted means the segment passed every check.
	Accepted
)

// String returns the disposition name.
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
	default:
		return "Unknown"
	}
}

// Segment is a TCP segment presented to Process.
type Segment struct {
	Seq, Ack uint32
	Len, Wnd uint64
	SYN      bool
	ACK      bool
	RST      bool
	FIN      bool
}

// Config holds the constructor parameters for Validator.
type Config struct {
	RcvNxt    uint32 // Next expected receive sequence number.
	RcvWnd    uint64 // Receive window, 0..2^30.
	SndUna    uint32 // Oldest unacknowledged send sequence number.
	SndNxt    uint32 // Next send sequence number.
	MaxSndWnd uint64 // Largest window ever advertised by the peer, 0..2^30.
	QuotaC    int    // Challenge ACK quota per window, 1..1000.
	WindowP   int64  // Rate-limit window in milliseconds, 1..10^6.
}

// Validator validates arriving segments on one connection.
//
// Every method is safe for concurrent use; the behavior is equivalent to
// some serial ordering of the calls.
type Validator struct {
	mu sync.Mutex

	rcvNxt    uint32
	rcvWnd    uint64
	sndUna    uint32
	sndNxt    uint32
	maxSndWnd uint64
	quotaC    int
	windowP   int64
	rstQuota  int // ceil(C/2)

	closed bool

	// lastNow records the timestamp of the most recent accepted call.
	// An empty value is represented by haveLastNow == false.
	haveLastNow bool
	lastNow     int64

	// Challenge ACK sliding window. wsValid == false means empty.
	wsValid bool
	ws      int64
	cnt     int
	cntR    int
}

// New constructs a Validator and validates every constructor parameter.
func New(c Config) (*Validator, error) {
	if c.RcvWnd > maxWnd {
		return nil, ErrInvalidRcvWnd
	}
	if c.MaxSndWnd > maxWnd {
		return nil, ErrInvalidMaxWnd
	}
	if uint64(c.SndNxt-c.SndUna) > maxSeqSpace {
		return nil, ErrFlightSize
	}
	if c.QuotaC < 1 || c.QuotaC > 1000 {
		return nil, ErrChallengeQuota
	}
	if c.WindowP < 1 || c.WindowP > 1_000_000 {
		return nil, ErrChallengeWindow
	}
	return &Validator{
		rcvNxt:    c.RcvNxt,
		rcvWnd:    c.RcvWnd,
		sndUna:    c.SndUna,
		sndNxt:    c.SndNxt,
		maxSndWnd: c.MaxSndWnd,
		quotaC:    c.QuotaC,
		windowP:   c.WindowP,
		rstQuota:  (c.QuotaC + 1) / 2,
	}, nil
}

// seqSpace is L = Len + SYN + FIN; each control bit occupies one number.
func (seg Segment) seqSpace() uint64 {
	l := seg.Len
	if seg.SYN {
		l++
	}
	if seg.FIN {
		l++
	}
	return l
}

// acceptable reports whether a segment occupying L sequence numbers
// starting at seq intersects the receive window.
//
// With L == 0 the segment is acceptable only when seq itself lies in the
// window (or equals rcvNxt when the window is closed). With L > 0 either
// the first or the last occupied number must lie in the window. Offsets
// at or above 2^31 denote positions strictly left of rcvNxt and never
// match a window no larger than 2^30.
func (v *Validator) acceptable(seq uint32, length uint64) bool {
	if v.rcvWnd == 0 {
		return length == 0 && seq == v.rcvNxt
	}
	if uint64(seq-v.rcvNxt) < v.rcvWnd {
		return true
	}
	if length > 0 {
		last := seq + uint32(length-1)
		if uint64(last-v.rcvNxt) < v.rcvWnd {
			return true
		}
	}
	return false
}

// challenge runs the challenge ACK rate limiter and reports whether the
// challenge may be sent. The limiter advances only when a challenge is
// actually requested; suppressed challenges leave the window untouched.
func (v *Validator) challenge(now int64, fromRST bool) Action {
	if !v.wsValid || now-v.ws >= v.windowP {
		v.wsValid = true
		v.ws = now
		v.cnt = 0
		v.cntR = 0
	}
	if v.cnt >= v.quotaC {
		return Suppressed
	}
	if fromRST && v.cntR >= v.rstQuota {
		return Suppressed
	}
	v.cnt++
	if fromRST {
		v.cntR++
	}
	return AckChallenge
}

func validateSegment(seg Segment) error {
	if seg.Len > maxWnd {
		return ErrInvalidLen
	}
	if seg.Wnd > maxWnd {
		return ErrInvalidWnd
	}
	if seg.SYN && seg.RST {
		return ErrSYNAndRST
	}
	return nil
}

// Process validates one arriving segment and returns the resulting action.
//
// Rejections (parameter errors, clock regression, closed connection)
// change no state whatsoever, including the clock and the limiter.
func (v *Validator) Process(now int64, seg Segment) (Action, error) {
	if now < 0 || now > maxTime {
		return Drop, ErrInvalidNow
	}
	if err := validateSegment(seg); err != nil {
		return Drop, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if v.haveLastNow && now < v.lastNow {
		return Drop, ErrClockRegression
	}
	if v.closed {
		return Drop, ErrClosed
	}

	action := v.processLocked(now, seg)

	// Every disposition is an accepted call and advances the clock.
	v.haveLastNow = true
	v.lastNow = now
	return action, nil
}

func (v *Validator) processLocked(now int64, seg Segment) Action {
	length := seg.seqSpace()

	// (1) RST segments ignore payload and flags: the RST check always
	// uses L = 0.
	if seg.RST {
		if !v.acceptable(seg.Seq, 0) {
			return Drop
		}
		if seg.Seq == v.rcvNxt {
			v.closed = true
			return Reset
		}
		return v.challenge(now, true)
	}

	// (2) Segments outside the receive window get a plain ACK.
	if !v.acceptable(seg.Seq, length) {
		return AckPlain
	}

	// (3) An in-window SYN is always a challenge.
	if seg.SYN {
		return v.challenge(now, false)
	}

	// (4) A non-SYN segment without ACK is dropped.
	if !seg.ACK {
		return Drop
	}

	// (5) Closed ACK range [sndUna-maxSndWnd, sndNxt], both ends included.
	lo := v.sndUna - uint32(v.maxSndWnd)
	if uint64(seg.Ack-lo) > uint64(v.sndNxt-lo) {
		return v.challenge(now, false)
	}

	// (6) The segment is accepted: advance send and receive state.
	if off := uint64(seg.Ack - v.sndUna); off > 0 && off <= uint64(v.sndNxt-v.sndUna) {
		v.sndUna = seg.Ack
	}
	if seg.Wnd > v.maxSndWnd {
		v.maxSndWnd = seg.Wnd
	}
	// A segment beginning at rcvNxt or to its left and reaching strictly
	// past rcvNxt advances rcvNxt; in-window out-of-order data does not.
	startOff := uint64(seg.Seq - v.rcvNxt)
	if startOff == 0 || startOff >= 1<<31 { // seg.Seq at or left of rcvNxt
		end := seg.Seq + uint32(length)
		endOff := uint64(end - v.rcvNxt)
		if endOff > 0 && endOff < 1<<31 {
			v.rcvNxt = end
		}
	}
	return Accepted
}

// Closed reports whether the connection has been reset.
func (v *Validator) Closed() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.closed
}

// RcvNxt returns the current receive next sequence number.
func (v *Validator) RcvNxt() uint32 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.rcvNxt
}

// SndUna returns the oldest unacknowledged send sequence number.
func (v *Validator) SndUna() uint32 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.sndUna
}

// MaxSndWnd returns the largest peer window recorded so far.
func (v *Validator) MaxSndWnd() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.maxSndWnd
}

// LastNow returns the timestamp of the last accepted call and whether
// any accepted call has happened.
func (v *Validator) LastNow() (int64, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lastNow, v.haveLastNow
}

// WindowStart returns the limiter window start and whether it is set.
func (v *Validator) WindowStart() (int64, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ws, v.wsValid
}

// Count returns the total number of challenges sent in the window and
// the subset triggered by RST segments.
func (v *Validator) Count() (cnt, cntR int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.cnt, v.cntR
}
