// Package bbr implements a deterministic round-driven BBR-like controller.
package bbr

import (
	"errors"
	"math/bits"
	"sync"
)

var (
	// ErrInvalidArgument reports an out-of-range constructor or ACK argument.
	ErrInvalidArgument = errors.New("bbr: invalid argument")
	// ErrClockRollback reports an ACK timestamp earlier than the accepted clock.
	ErrClockRollback = errors.New("bbr: clock rollback")
	// ErrInvalidSample reports ds after the delivery snapshot before this ACK.
	ErrInvalidSample = errors.New("bbr: invalid sample")
)

type State int

const (
	// Startup uses the startup pacing and cwnd gains.
	Startup State = iota
	// Drain waits for inflight data to fall to BDP.
	Drain
	// ProbeBW cycles the deterministic bandwidth-probing gain schedule.
	ProbeBW
	// ProbeRTT reduces inflight data to refresh minimum RTT.
	ProbeRTT
)

// Controller is a concurrency-safe deterministic BBR estimator and state machine.
type Controller struct {
	mu sync.RWMutex

	mss int64
	d   int64
	r   int64
	n   int64

	filter []bwSample

	minRtt   int64
	hasRtt   bool
	stamp    int64
	state    State
	filled   bool
	fullBw   int64
	fullCnt  int
	ci       int
	cs       int64
	pd       int64
	lastNow  int64
	hasClock bool

	filterOps int64
}

type bwSample struct {
	round int64
	value int64
}

// New validates mss and returns a controller in Startup.
func New(mss int64) (*Controller, error) {
	if mss < 1 || mss > 65535 {
		return nil, ErrInvalidArgument
	}
	return &Controller{mss: mss}, nil
}

// OnAck applies one acknowledged sample using the fixed ordered state-machine steps.
func (c *Controller) OnAck(now int64, n int64, rtt int64, ds int64, inflight int64, appLimited bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.mss < 1 || now < 0 || now > 1_000_000_000_000 ||
		n < 1 || n > 1_000_000 ||
		rtt < 1 || rtt > 100_000 ||
		inflight < 0 || inflight > 1_000_000_000_000 ||
		c.d+n > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	if c.hasClock && now < c.lastNow {
		return ErrClockRollback
	}
	if ds < 0 || ds > c.d {
		return ErrInvalidSample
	}

	startedInProbeBW := c.state == ProbeBW
	c.lastNow = now
	c.hasClock = true

	c.d += n
	newRound := false
	if ds >= c.n {
		c.r++
		c.n = c.d
		newRound = true
	}

	sample := (c.d - ds) * 1000 / rtt

	for len(c.filter) > 0 && c.r-c.filter[0].round >= 10 {
		c.filter = c.filter[1:]
		c.filterOps++
	}
	currentMax := int64(0)
	if len(c.filter) > 0 {
		currentMax = c.filter[0].value
	}
	if !appLimited || sample >= currentMax {
		for len(c.filter) > 0 && c.filter[len(c.filter)-1].value <= sample {
			c.filter = c.filter[:len(c.filter)-1]
			c.filterOps++
		}
		c.filter = append(c.filter, bwSample{round: c.r, value: sample})
		c.filterOps++
	}
	maxBw := int64(0)
	if len(c.filter) > 0 {
		maxBw = c.filter[0].value
	}

	expired := c.hasRtt && now-c.stamp >= 10_000
	if !c.hasRtt || rtt <= c.minRtt || expired {
		c.minRtt = rtt
		c.stamp = now
		c.hasRtt = true
	}

	if newRound && !appLimited && !c.filled {
		if maxBw*100 >= c.fullBw*125 {
			c.fullBw = maxBw
			c.fullCnt = 0
		} else {
			c.fullCnt++
			if c.fullCnt == 3 {
				c.filled = true
			}
		}
		if c.state == Startup && c.filled {
			c.state = Drain
		}
	}

	bdp := mulDivFloor(maxBw, c.minRtt, 1000)
	if c.state == Drain && inflight <= bdp {
		c.state = ProbeBW
		c.ci = 2
		c.cs = now
	}

	if startedInProbeBW {
		gains := [...]int{125, 75, 100, 100, 100, 100, 100, 100}
		elapsed := now - c.cs
		g := gains[c.ci]
		advance := false
		switch g {
		case 100:
			advance = elapsed >= c.minRtt
		case 125:
			target := mulDivFloor(bdp, 125, 100)
			advance = elapsed >= c.minRtt && inflight >= target
		case 75:
			advance = elapsed >= c.minRtt || inflight <= bdp
		}
		if advance {
			c.ci = (c.ci + 1) % 8
			c.cs = now
		}
	}

	if expired && c.state != ProbeRTT {
		c.state = ProbeRTT
		c.pd = 0
	}
	if c.state == ProbeRTT {
		if c.pd == 0 && inflight <= 4*c.mss {
			c.pd = now + 200
		}
		if c.pd != 0 && now >= c.pd {
			c.stamp = now
			c.pd = 0
			if c.filled {
				c.state = ProbeBW
				c.ci = 2
				c.cs = now
			} else {
				c.state = Startup
			}
		}
	}

	return nil
}

func (c *Controller) pacingLocked() int64 {
	if c.d == 0 {
		return 0
	}
	pg := int64(100)
	switch c.state {
	case Startup, Drain:
		pg = 289
		if c.state == Drain {
			pg = 35
		}
	case ProbeBW:
		pg = int64(probeGain(c.ci))
	case ProbeRTT:
		pg = 100
	}
	return mulDivFloor(c.maxBwLocked(), pg, 100)
}

// Cwnd returns the current congestion window in bytes.
func (c *Controller) Cwnd() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cwndLocked()
}

func (c *Controller) cwndLocked() int64 {
	if c.d == 0 {
		return 10 * c.mss
	}
	if c.state == ProbeRTT {
		return 4 * c.mss
	}
	bdp := mulDivFloor(c.maxBwLocked(), c.minRtt, 1000)
	cg := int64(289)
	if c.state == ProbeBW {
		cg = 200
	}
	window := mulDivFloor(bdp, cg, 100)
	minimum := int64(4) * c.mss
	if window < minimum {
		return minimum
	}
	return window
}

// Pacing returns the current sending rate in bytes per second.
func (c *Controller) Pacing() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pacingLocked()
}

// State returns the current BBR state.
func (c *Controller) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// MaxBw returns the ten-round maximum bandwidth sample in bytes per second.
func (c *Controller) MaxBw() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.maxBwLocked()
}

func (c *Controller) maxBwLocked() int64 {
	if len(c.filter) == 0 {
		return 0
	}
	return c.filter[0].value
}

// MinRtt returns the current minimum RTT sample in milliseconds.
func (c *Controller) MinRtt() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.hasRtt {
		return 0
	}
	return c.minRtt
}

// Round returns the current delivery-round counter.
func (c *Controller) Round() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.r
}

// FilterOps returns monotonic-queue enqueue, dequeue, and expiry operation counts.
func (c *Controller) FilterOps() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.filterOps
}

func probeGain(index int) int {
	return [...]int{125, 75, 100, 100, 100, 100, 100, 100}[index]
}

func mulDivFloor(a int64, b int64, divisor int64) int64 {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	div := uint64(divisor)
	var quotient uint64
	var remainder uint64
	for bit := 63; bit >= 0; bit-- {
		remainder = remainder<<1 | hi>>uint(bit)&1
		if remainder >= div {
			remainder -= div
			quotient |= 1 << uint(bit)
		}
	}
	for bit := 63; bit >= 0; bit-- {
		remainder = remainder<<1 | lo>>uint(bit)&1
		if remainder >= div {
			remainder -= div
			quotient |= 1 << uint(bit)
		}
	}
	return int64(quotient)
}

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
	default:
		return "Unknown"
	}
}
