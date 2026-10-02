// Package tricolor implements an RFC 4115 style two-rate three-color
// marker with overflow-coupled committed/excess token buckets, a red
// penalty window and online reconfiguration.
package tricolor

import (
	"errors"
	"fmt"
	"sync"
)

// Color is the output color of a packet.
type Color int

const (
	Green Color = iota
	Yellow
	Red
)

func (c Color) String() string {
	switch c {
	case Green:
		return "Green"
	case Yellow:
		return "Yellow"
	case Red:
		return "Red"
	}
	return "Unknown"
}

// Input is the (pre-)color of an incoming packet. Blind selects
// color-blind mode and is treated exactly like Green.
type Input int

const (
	InGreen Input = iota
	InYellow
	InRed
	InBlind
)

func (i Input) String() string {
	switch i {
	case InGreen:
		return "Green"
	case InYellow:
		return "Yellow"
	case InRed:
		return "Red"
	case InBlind:
		return "Blind"
	}
	return "Unknown"
}

// Distinguishable rejection reasons. Invalid parameters always take
// precedence over clock regression.
var (
	ErrInvalidParam  = errors.New("tricolor: invalid parameter")
	ErrClockBackward = errors.New("tricolor: clock moved backward")
)

const (
	maxCIR = int64(1_000_000)
	maxCBS = int64(1_000_000_000)
	maxEBS = int64(1_000_000_000)
	maxW   = int64(1_000_000)
	maxK   = int64(1_000)
	maxPn  = int64(1_000_000)
	maxNow = int64(1_000_000_000_000)
	maxB   = int64(1_000_000)
	maxS   = 3
)

// Params holds the marker configuration.
type Params struct {
	CIR int64 // committed rate, bytes per millisecond, [1, 1e6]
	CBS int64 // committed bucket depth, bytes, [1, 1e9]
	EBS int64 // excess bucket depth, bytes, [0, 1e9]
	W   int64 // red window, milliseconds, [1, 1e6]
	K   int64 // red threshold, [1, 1000]
	Pn  int64 // base penalty duration, milliseconds, [1, 1e6]
}

func validateParams(p Params) error {
	if p.CIR < 1 || p.CIR > maxCIR {
		return fmt.Errorf("%w: CIR %d out of range [1, %d]", ErrInvalidParam, p.CIR, maxCIR)
	}
	if p.CBS < 1 || p.CBS > maxCBS {
		return fmt.Errorf("%w: CBS %d out of range [1, %d]", ErrInvalidParam, p.CBS, maxCBS)
	}
	if p.EBS < 0 || p.EBS > maxEBS {
		return fmt.Errorf("%w: EBS %d out of range [0, %d]", ErrInvalidParam, p.EBS, maxEBS)
	}
	if p.W < 1 || p.W > maxW {
		return fmt.Errorf("%w: W %d out of range [1, %d]", ErrInvalidParam, p.W, maxW)
	}
	if p.K < 1 || p.K > maxK {
		return fmt.Errorf("%w: K %d out of range [1, %d]", ErrInvalidParam, p.K, maxK)
	}
	if p.Pn < 1 || p.Pn > maxPn {
		return fmt.Errorf("%w: Pn %d out of range [1, %d]", ErrInvalidParam, p.Pn, maxPn)
	}
	return nil
}

func validateNow(now int64) error {
	if now < 0 || now > maxNow {
		return fmt.Errorf("%w: now %d out of range [0, %d]", ErrInvalidParam, now, maxNow)
	}
	return nil
}

// Snapshot is a consistent read-only view of the marker state.
type Snapshot struct {
	CIR, CBS, EBS int64
	W, K, Pn      int64
	Tc, Te        int64
	Last          int64
	Until         int64
	LastUntil     int64
	S             int
	RedQueue      []int64
}

// InPenalty reports whether the marker is penalized at time now.
func (s Snapshot) InPenalty(now int64) bool { return now < s.Until }

// Stats accumulates per-output-color packet and byte counts.
type Stats struct {
	Packets [3]int64
	Bytes   [3]int64
}

// Marker is a concurrency-safe two-rate three-color marker.
type Marker struct {
	mu        sync.Mutex
	params    Params
	tc, te    int64
	last      int64
	until     int64
	lastUntil int64
	s         int
	redQ      []int64
	head      int
	stats     Stats
}

// New constructs a Marker. Tc starts full (CBS), Te starts full (EBS).
func New(p Params) (*Marker, error) {
	if err := validateParams(p); err != nil {
		return nil, err
	}
	return &Marker{params: p, tc: p.CBS, te: p.EBS}, nil
}

// refill settles tokens up to now. The excess bucket is fed only by
// committed-bucket overflow; overflow beyond EBS is discarded.
// (now-last)*CIR <= 1e12 * 1e6 = 1e18, no int64 overflow.
func (m *Marker) refill(now int64) {
	add := (now - m.last) * m.params.CIR
	m.tc += add
	if m.tc > m.params.CBS {
		o := m.tc - m.params.CBS
		m.tc = m.params.CBS
		if m.te += o; m.te > m.params.EBS {
			m.te = m.params.EBS
		}
	}
	m.last = now
}

// Mark classifies one packet of b bytes arriving at now with the given
// input color. It returns the output color and whether the red verdict
// was caused by an active penalty period.
func (m *Marker) Mark(now int64, in Input, b int64) (Color, bool, error) {
	if err := validateNow(now); err != nil {
		return Red, false, err
	}
	if in < InGreen || in > InBlind {
		return Red, false, fmt.Errorf("%w: color %d", ErrInvalidParam, int(in))
	}
	if b < 1 || b > maxB {
		return Red, false, fmt.Errorf("%w: b %d out of range [1, %d]", ErrInvalidParam, b, maxB)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.last {
		return Red, false, fmt.Errorf("%w: now %d < last %d", ErrClockBackward, now, m.last)
	}

	m.refill(now)

	if now < m.until {
		m.record(Red, b)
		return Red, true, nil
	}

	var out Color
	switch in {
	case InGreen, InBlind:
		if m.tc >= b {
			m.tc -= b
			out = Green
		} else if m.te >= b {
			m.te -= b
			out = Yellow
		} else {
			out = Red
		}
	case InYellow:
		if m.te >= b {
			m.te -= b
			out = Yellow
		} else {
			out = Red
		}
	case InRed:
		out = Red
	}

	if out == Red && in != InRed {
		m.recordRed(now)
	}
	m.record(out, b)
	return out, false, nil
}

// recordRed appends now to the red queue, evicts entries with t+W <= now
// (amortized O(1)) and triggers a penalty once the queue reaches K.
func (m *Marker) recordRed(now int64) {
	w := m.params.W
	for m.head < len(m.redQ) && m.redQ[m.head]+w <= now {
		m.head++
	}
	if m.head > 64 && m.head*2 >= len(m.redQ) {
		m.redQ = append([]int64(nil), m.redQ[m.head:]...)
		m.head = 0
	}
	m.redQ = append(m.redQ, now)
	if int64(len(m.redQ)-m.head) < m.params.K {
		return
	}
	m.redQ = m.redQ[:0]
	m.head = 0
	if m.lastUntil > 0 && now-m.lastUntil < w {
		if m.s < maxS {
			m.s++
		}
	} else {
		m.s = 0
	}
	dur := m.params.Pn << m.s
	m.until = now + dur
	m.lastUntil = m.until
}

func (m *Marker) record(c Color, b int64) {
	m.stats.Packets[c]++
	m.stats.Bytes[c] += b
}

// Reconfigure refills with the old parameters, then clamps both buckets
// to the new depths (truncated tokens are discarded, not spilled) and
// installs the new parameters. Penalty state and the red queue are kept.
func (m *Marker) Reconfigure(now int64, cir, cbs, ebs int64) error {
	p := m.params
	p.CIR, p.CBS, p.EBS = cir, cbs, ebs
	if err := validateParams(p); err != nil {
		return err
	}
	if err := validateNow(now); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.last {
		return fmt.Errorf("%w: now %d < last %d", ErrClockBackward, now, m.last)
	}

	m.refill(now)
	if m.tc > cbs {
		m.tc = cbs
	}
	if m.te > ebs {
		m.te = ebs
	}
	m.params = p
	return nil
}

// Snapshot returns a consistent copy of the current state.
func (m *Marker) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Snapshot{
		CIR: m.params.CIR, CBS: m.params.CBS, EBS: m.params.EBS,
		W: m.params.W, K: m.params.K, Pn: m.params.Pn,
		Tc: m.tc, Te: m.te, Last: m.last,
		Until: m.until, LastUntil: m.lastUntil, S: m.s,
		RedQueue: append([]int64(nil), m.redQ[m.head:]...),
	}
}

// Stats returns the accumulated per-output-color counters.
func (m *Marker) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}
