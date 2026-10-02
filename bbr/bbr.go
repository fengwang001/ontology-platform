// Package bbr implements a round-driven BBR-style bandwidth and RTT
// estimator with a four-state controller (Startup, Drain, ProbeBW,
// ProbeRTT). Every accepted acknowledgment advances a deterministic
// state machine; replaying the same acknowledgment sequence reproduces
// the exact same states, pacing rates and congestion windows.
package bbr

import (
	"errors"
	"fmt"
	"math/bits"
	"sync"
)

// State is the controller state.
type State int

const (
	Startup State = iota
	Drain
	ProbeBW
	ProbeRTT
)

func (s State) String() string {
	switch s {
	case Startup:
		return "Startup"
	case Drain:
		return "Drain"
	case ProbeBW:
		return "ProbeBW"
	case ProbeRTT:
		return "ProbeRTT"
	}
	return "Unknown"
}

// Rejection reasons for OnAck, distinguishable via errors.Is.
var (
	ErrInvalidParam  = errors.New("bbr: invalid parameter")
	ErrClockRewind   = errors.New("bbr: clock rewind")
	ErrInvalidSample = errors.New("bbr: invalid sample")
)

const (
	filterWindow  = 10    // a sample recorded at round r is valid while R-r < 10
	minRttExpiry  = 10000 // ms
	probeRttWait  = 200   // ms
	startupPacing = 289
	drainPacing   = 35
	cwndGain      = 289
	probeBWCwnd   = 200
)

// pacingGainCycle is the ProbeBW gain cycle; ci starts at 2 on entry.
var pacingGainCycle = [8]uint64{125, 75, 100, 100, 100, 100, 100, 100}

// sample is a bandwidth sample tagged with the round it was recorded in.
type sample struct {
	round uint64
	bw    uint64
}

// BBR is the estimator/controller. All methods are safe for concurrent
// use; results are equivalent to some serial order of the calls.
type BBR struct {
	mu sync.Mutex

	mss uint64

	delivered uint64 // D: cumulative delivered bytes
	round     uint64 // R: current round
	nextRound uint64 // N: delivered-count boundary that opens the next round

	filter    []sample // monotone deque, front holds the window maximum
	filterOps uint64   // enqueues + dequeues + evictions

	minRtt    uint64
	stamp     uint64
	hasMinRtt bool

	state   State
	filled  bool
	fullBw  uint64
	fullCnt uint64

	ci int    // cycle index into pacingGainCycle
	cs uint64 // cycle start time (ms)
	pd uint64 // ProbeRTT exit deadline; 0 means unset

	lastNow uint64
	hasAck  bool
}

// New creates a controller. mss must be in [1, 65535].
func New(mss uint64) (*BBR, error) {
	if mss < 1 || mss > 65535 {
		return nil, ErrInvalidParam
	}
	return &BBR{mss: mss, state: Startup}, nil
}

// mulDiv returns floor(a*b/c). The 128-bit intermediate product keeps the
// BDP arithmetic exact for the full input range (the quotient always fits
// in 64 bits here, and the product's high word is always below c).
func mulDiv(a, b, c uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	q, _ := bits.Div64(hi, lo, c)
	return q
}

// bdpLocked returns floor(maxBw*minRtt/1000) using 128-bit products.
func (b *BBR) bdpLocked() uint64 {
	return mulDiv(b.maxBwLocked(), b.minRtt, 1000)
}

// maxBwLocked returns the maximum bandwidth in the filter window (0 if empty).
func (b *BBR) maxBwLocked() uint64 {
	if len(b.filter) == 0 {
		return 0
	}
	return b.filter[0].bw
}

// evictLocked drops samples whose round r satisfies R-r >= filterWindow.
func (b *BBR) evictLocked() {
	for len(b.filter) > 0 && b.round-b.filter[0].round >= filterWindow {
		b.filter = b.filter[1:]
		b.filterOps++
	}
}

// pushLocked inserts s into the monotone deque: smaller-or-equal entries at
// the back can never become the window maximum while s is inside the window,
// so they are removed. The deque stays non-increasing from front to back.
func (b *BBR) pushLocked(s sample) {
	for len(b.filter) > 0 && b.filter[len(b.filter)-1].bw <= s.bw {
		b.filter = b.filter[:len(b.filter)-1]
		b.filterOps++
	}
	b.filter = append(b.filter, s)
	b.filterOps++
}

// OnAck processes one acknowledgment event.
//
// now: current time in ms; n: newly acknowledged bytes; rtt: RTT sample in
// ms; ds: snapshot of D when the acked packet was sent; inflight: bytes in
// flight after this ack; appLimited: the sample is application-limited.
//
// A rejected acknowledgment changes nothing. Rejection reasons are checked
// in the order: invalid parameter, clock rewind, invalid sample.
func (b *BBR) OnAck(now, n, rtt, ds, inflight uint64, appLimited bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if now > 1e12 || n < 1 || n > 1e6 || rtt < 1 || rtt > 1e5 || inflight > 1e12 {
		return fmt.Errorf("%w: now=%d n=%d rtt=%d inflight=%d out of range",
			ErrInvalidParam, now, n, rtt, inflight)
	}
	if b.hasAck && now < b.lastNow {
		return fmt.Errorf("%w: now=%d < last=%d", ErrClockRewind, now, b.lastNow)
	}
	if ds > b.delivered {
		return fmt.Errorf("%w: ds=%d > delivered=%d", ErrInvalidSample, ds, b.delivered)
	}

	startState := b.state

	// Step 1: delivery total, round advance, bandwidth sample.
	b.delivered += n
	newRound := false
	if ds >= b.nextRound {
		b.round++
		b.nextRound = b.delivered
		newRound = true
	}
	s := (b.delivered - ds) * 1000 / rtt

	// Step 2: windowed max-bandwidth filter.
	b.evictLocked()
	if !appLimited || s >= b.maxBwLocked() {
		b.pushLocked(sample{round: b.round, bw: s})
	}
	maxBw := b.maxBwLocked()

	// Step 3: min-RTT tracking with 10s expiry.
	expired := b.hasMinRtt && now-b.stamp >= minRttExpiry
	if !b.hasMinRtt || rtt <= b.minRtt || expired {
		b.minRtt = rtt
		b.stamp = now
		b.hasMinRtt = true
	}

	// Step 4: full-pipe detection, then Startup -> Drain.
	if newRound && !appLimited && !b.filled {
		if maxBw*100 >= b.fullBw*125 {
			b.fullBw = maxBw
			b.fullCnt = 0
		} else {
			b.fullCnt++
			if b.fullCnt >= 3 {
				b.filled = true
			}
		}
	}
	if b.state == Startup && b.filled {
		b.state = Drain
	}

	// Step 5: Drain -> ProbeBW once inflight drains to the BDP.
	if b.state == Drain && inflight <= b.bdpLocked() {
		b.state = ProbeBW
		b.ci = 2
		b.cs = now
	}

	// Step 6: ProbeBW gain-cycle advance, at most one step per ack, and
	// only if the state was already ProbeBW when this ack began.
	if startState == ProbeBW {
		el := now - b.cs
		bdp := b.bdpLocked()
		advance := false
		switch pacingGainCycle[b.ci] {
		case 100:
			advance = el >= b.minRtt
		case 125:
			advance = el >= b.minRtt && inflight >= mulDiv(bdp, 125, 100)
		case 75:
			advance = el >= b.minRtt || inflight <= bdp
		}
		if advance {
			b.ci = (b.ci + 1) % 8
			b.cs = now
		}
	}

	// Step 7: ProbeRTT entry/exit.
	if expired && b.state != ProbeRTT {
		b.state = ProbeRTT
		b.pd = 0
	}
	if b.state == ProbeRTT {
		if b.pd == 0 && inflight <= 4*b.mss {
			b.pd = now + probeRttWait
		}
		if b.pd != 0 && now >= b.pd {
			b.stamp = now
			if b.filled {
				b.state = ProbeBW
				b.ci = 2
				b.cs = now
			} else {
				b.state = Startup
			}
			b.pd = 0
		}
	}

	b.lastNow = now
	b.hasAck = true
	return nil
}

// Pacing returns the current pacing rate in bytes per second
// (floor(maxBw*pg/100)). It is 0 before the first accepted ack.
func (b *BBR) Pacing() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.hasAck {
		return 0
	}
	var pg uint64
	switch b.state {
	case Startup:
		pg = startupPacing
	case Drain:
		pg = drainPacing
	case ProbeBW:
		pg = pacingGainCycle[b.ci]
	case ProbeRTT:
		pg = 100
	}
	return b.maxBwLocked() * pg / 100
}

// Cwnd returns the congestion window in bytes. Before the first accepted
// ack it is 10*mss; in ProbeRTT it is 4*mss; otherwise it is
// max(floor(BDP*cg/100), 4*mss).
func (b *BBR) Cwnd() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.hasAck {
		return 10 * b.mss
	}
	if b.state == ProbeRTT {
		return 4 * b.mss
	}
	var cg uint64
	switch b.state {
	case Startup, Drain:
		cg = cwndGain
	case ProbeBW:
		cg = probeBWCwnd
	}
	cwnd := mulDiv(b.bdpLocked(), cg, 100)
	if min := 4 * b.mss; cwnd < min {
		cwnd = min
	}
	return cwnd
}

// State returns the current controller state.
func (b *BBR) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// MaxBw returns the windowed maximum bandwidth sample (0 if empty).
func (b *BBR) MaxBw() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.maxBwLocked()
}

// MinRtt returns the current minimum RTT and whether one has been recorded.
func (b *BBR) MinRtt() (uint64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.minRtt, b.hasMinRtt
}

// Round returns the current round counter R.
func (b *BBR) Round() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.round
}
