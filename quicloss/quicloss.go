// Package quicloss implements a deterministic RFC 9002 style loss detector
// and probe timeout (PTO) controller over two packet number spaces:
// the handshake space (H) and the application space (A).
//
// All times are non-negative millisecond timestamps bounded by MaxTime.
// Every exported method serializes on the controller mutex, so concurrent
// calls have an equivalent serial order and replaying the same accepted
// call sequence reproduces identical loss lists, timers and probes.
package quicloss

import (
	"errors"
	"sort"
	"sync"
)

// Space identifiers.
const (
	SpaceH byte = 'H' // handshake packet number space
	SpaceA byte = 'A' // application / 1-RTT packet number space
)

// Bounds mandated by the specification.
const (
	MaxTime         int64 = 1_000_000_000_000
	MinPacketSize         = 1
	MaxPacketSize         = 65535
	MaxBackoffShift       = 20
)

// Distinguishable rejection reasons. Rejected calls change no state,
// including the monotonic accepted-call clock.
var (
	ErrInvalidMaxAckDelay = errors.New("quicloss: maxAckDelay out of range [0,10000]ms")
	ErrInvalidSpace       = errors.New("quicloss: invalid packet number space")
	ErrInvalidSize        = errors.New("quicloss: packet size out of range [1,65535]")
	ErrInvalidTime        = errors.New("quicloss: timestamp out of range [0,1e12]ms")
	ErrEmptyAck           = errors.New("quicloss: acknowledged packet number set is empty")
	ErrNegativePN         = errors.New("quicloss: negative packet number")
	ErrClockBackwards     = errors.New("quicloss: clock moved backwards")
	ErrSpaceDiscarded     = errors.New("quicloss: handshake space discarded")
	ErrPNNotIncreasing    = errors.New("quicloss: packet number not strictly increasing")
	ErrPNNeverSent        = errors.New("quicloss: acknowledged packet number was never sent")
	ErrTimeoutEarly       = errors.New("quicloss: timeout fired before its timer")
	ErrNoTimer            = errors.New("quicloss: no timer armed")
)

// Mode identifies the kind of timer event.
type Mode int

const (
	ModeLoss Mode = iota + 1
	ModePTO
)

func (m Mode) String() string {
	switch m {
	case ModeLoss:
		return "Loss"
	case ModePTO:
		return "PTO"
	default:
		return "Unknown"
	}
}

// Packet is an inspection snapshot of one outstanding sent packet.
type Packet struct {
	PN        int64
	SentAt    int64
	Size      int
	AckElicit bool
}

// TimerInfo is the deterministic next firing decision from Timer.
type TimerInfo struct {
	Time  int64
	Space byte
	Mode  Mode
}

// ProbeRequest is the observable result of a PTO firing.
type ProbeRequest struct {
	Space byte
}

// TimeoutResult is the outcome of OnTimeout: either a loss list or a probe.
type TimeoutResult struct {
	Loss  []int64
	Probe *ProbeRequest
}

type packet struct {
	sentAt int64
	size   int
	ae     bool
}

type space struct {
	packets    map[int64]packet
	maxSent    int64
	hasSent    bool
	largestAck int64
	hasLA      bool
	lossTime   int64
	hasLT      bool
}

// Controller owns both spaces and the shared RTT/PTO state.
// The zero value is not usable; construct it with New.
type Controller struct {
	mu sync.Mutex

	maxAckDelay int64

	h space
	a space

	latest    int64
	srtt      int64
	rttvar    int64
	minRTT    int64
	hasSample bool

	ptoCount  int
	confirmed bool

	lastNow  int64
	hasClock bool
}

// New creates a controller. maxAckDelay must be within [0,10000] milliseconds.
func New(maxAckDelay int64) (*Controller, error) {
	if maxAckDelay < 0 || maxAckDelay > 10000 {
		return nil, ErrInvalidMaxAckDelay
	}
	return &Controller{
		maxAckDelay: maxAckDelay,
		h:           space{packets: make(map[int64]packet)},
		a:           space{packets: make(map[int64]packet)},
		latest:      333,
		srtt:        333,
		rttvar:      166,
	}, nil
}

func (c *Controller) target(spaceID byte) (*space, error) {
	switch spaceID {
	case SpaceH:
		return &c.h, nil
	case SpaceA:
		return &c.a, nil
	default:
		return nil, ErrInvalidSpace
	}
}

func (c *Controller) acceptClock(now int64) {
	c.lastNow = now
	c.hasClock = true
}

// Send records a packet. pn must be non-negative and, after a space's first
// packet, strictly greater than the largest pn previously sent in that space.
func (c *Controller) Send(now int64, spaceID byte, pn int64, size int, ae bool) error {
	if spaceID != SpaceH && spaceID != SpaceA {
		return ErrInvalidSpace
	}
	if size < MinPacketSize || size > MaxPacketSize {
		return ErrInvalidSize
	}
	if now < 0 || now > MaxTime {
		return ErrInvalidTime
	}
	if pn < 0 {
		return ErrNegativePN
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasClock && now < c.lastNow {
		return ErrClockBackwards
	}
	if spaceID == SpaceH && c.confirmed {
		return ErrSpaceDiscarded
	}
	sp, err := c.target(spaceID)
	if err != nil {
		return err
	}
	if sp.hasSent && pn <= sp.maxSent {
		return ErrPNNotIncreasing
	}

	sp.packets[pn] = packet{sentAt: now, size: size, ae: ae}
	sp.maxSent = pn
	sp.hasSent = true
	c.acceptClock(now)
	return nil
}

// Ack processes a non-empty acknowledged pn set. Numbers absent from the
// outstanding table are duplicate gaps and are ignored; a number above the
// space's largest ever sent pn is ErrPNNeverSent. It returns the loss list
// produced by the mandatory post-ack loss detection.
func (c *Controller) Ack(now int64, spaceID byte, pns []int64, ackDelay int64) ([]int64, error) {
	if spaceID != SpaceH && spaceID != SpaceA {
		return nil, ErrInvalidSpace
	}
	if now < 0 || now > MaxTime {
		return nil, ErrInvalidTime
	}
	if len(pns) == 0 {
		return nil, ErrEmptyAck
	}
	for _, pn := range pns {
		if pn < 0 {
			return nil, ErrNegativePN
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasClock && now < c.lastNow {
		return nil, ErrClockBackwards
	}
	if spaceID == SpaceH && c.confirmed {
		return nil, ErrSpaceDiscarded
	}
	sp, err := c.target(spaceID)
	if err != nil {
		return nil, err
	}
	for _, pn := range pns {
		if !sp.hasSent || pn > sp.maxSent {
			return nil, ErrPNNeverSent
		}
	}

	// The call is accepted: clock and state may change from here on.
	c.acceptClock(now)

	newly := make(map[int64]struct{}, len(pns))
	for _, pn := range pns {
		if _, ok := sp.packets[pn]; ok {
			newly[pn] = struct{}{}
		}
	}
	if len(newly) == 0 {
		// A duplicate-only ACK changes nothing except the accepted clock.
		return []int64{}, nil
	}

	var largest int64
	first := true
	for pn := range newly {
		if first || pn > largest {
			largest = pn
			first = false
		}
	}
	if !sp.hasLA || largest > sp.largestAck {
		sp.largestAck = largest
		sp.hasLA = true
	}

	if lp := sp.packets[largest]; lp.ae {
		c.takeRTTSample(spaceID, now-lp.sentAt, ackDelay)
	}

	clearsPTO := false
	for pn := range newly {
		if sp.packets[pn].ae {
			clearsPTO = true
		}
		delete(sp.packets, pn)
	}
	if clearsPTO {
		c.ptoCount = 0
	}

	return c.detectLocked(sp, now), nil
}

// takeRTTSample updates latest/minRtt and the RFC 9002 SRTT/RTTVAR estimates.
// It must be called while the largest newly acknowledged packet is still in
// the table and known to be ack-eliciting.
func (c *Controller) takeRTTSample(spaceID byte, rawSample int64, ackDelay int64) {
	sample := rawSample
	if sample < 1 {
		sample = 1
	}
	c.latest = sample
	if !c.hasSample {
		c.minRTT = sample
		c.srtt = sample
		c.rttvar = sample / 2
		c.hasSample = true
		return
	}
	if sample < c.minRTT {
		c.minRTT = sample
	}
	var ad int64
	if spaceID == SpaceA && c.confirmed {
		ad = ackDelay
		if ad > c.maxAckDelay {
			ad = c.maxAckDelay
		}
		if ad < 0 {
			ad = 0
		}
	}
	adj := sample
	if sample >= c.minRTT+ad {
		adj = sample - ad
	}
	diff := c.srtt - adj
	if diff < 0 {
		diff = -diff
	}
	c.rttvar = (3*c.rttvar + diff) / 4
	c.srtt = (7*c.srtt + adj) / 8
}

// Detect runs loss detection for one space at now and returns the newly lost
// packet numbers in ascending order.
func (c *Controller) Detect(spaceID byte, now int64) ([]int64, error) {
	if spaceID != SpaceH && spaceID != SpaceA {
		return nil, ErrInvalidSpace
	}
	if now < 0 || now > MaxTime {
		return nil, ErrInvalidTime
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasClock && now < c.lastNow {
		return nil, ErrClockBackwards
	}
	if spaceID == SpaceH && c.confirmed {
		return nil, ErrSpaceDiscarded
	}
	sp, err := c.target(spaceID)
	if err != nil {
		return nil, err
	}
	c.acceptClock(now)
	return c.detectLocked(sp, now), nil
}

func lossDelay(latest, srtt int64) int64 {
	base := latest
	if srtt > base {
		base = srtt
	}
	d := 9 * base / 8
	if d < 1 {
		d = 1
	}
	return d
}

// detectLocked resets lt, removes packets below la satisfying either the
// packet-number threshold (la-pn >= 3, equality inclusive) or the time
// threshold (sentAt <= now-loss_delay, equality inclusive), and otherwise
// records the earliest re-evaluation time in lt.
func (c *Controller) detectLocked(sp *space, now int64) []int64 {
	sp.lossTime = 0
	sp.hasLT = false
	if !sp.hasLA {
		return []int64{}
	}

	delay := lossDelay(c.latest, c.srtt)
	cutoff := now - delay
	lost := make([]int64, 0)
	var nextTime int64
	hasNext := false

	for pn, pkt := range sp.packets {
		if pn >= sp.largestAck {
			continue
		}
		if sp.largestAck-pn >= 3 || pkt.sentAt <= cutoff {
			lost = append(lost, pn)
			delete(sp.packets, pn)
			continue
		}
		candidate := pkt.sentAt + delay
		if !hasNext || candidate < nextTime {
			nextTime = candidate
			hasNext = true
		}
	}

	if hasNext {
		sp.lossTime = nextTime
		sp.hasLT = true
	}
	sort.Slice(lost, func(i, j int) bool { return lost[i] < lost[j] })
	return lost
}

func (c *Controller) ptoBase(spaceID byte) int64 {
	pto := c.srtt
	extra := 4 * c.rttvar
	if extra < 1 {
		extra = 1
	}
	pto += extra
	if spaceID == SpaceA {
		pto += c.maxAckDelay
	}
	return pto
}

// lastAckElicit returns the largest sentAt among ack-eliciting packets.
func lastAckElicit(sp *space) (int64, bool) {
	var last int64
	found := false
	for _, pkt := range sp.packets {
		if pkt.ae && (!found || pkt.sentAt > last) {
			last = pkt.sentAt
			found = true
		}
	}
	return last, found
}

// Timer returns the next deterministic firing event. Any space's loss time
// wins over PTO; H wins ties. PTO candidates require ack-eliciting traffic,
// and the application space participates only after handshake confirmation.
// Timer is a pure query: it takes no timestamp and changes no state.
func (c *Controller) Timer() (TimerInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var info TimerInfo
	found := false
	consider := func(spaceID byte, sp *space) {
		if sp.hasLT {
			if !found || info.Mode != ModeLoss || sp.lossTime < info.Time {
				info = TimerInfo{Time: sp.lossTime, Space: spaceID, Mode: ModeLoss}
				found = true
			}
		}
	}
	consider(SpaceH, &c.h)
	consider(SpaceA, &c.a)
	if found {
		return info, true
	}

	considerPTO := func(spaceID byte, sp *space) {
		last, ok := lastAckElicit(sp)
		if !ok {
			return
		}
		shift := c.ptoCount
		if shift > MaxBackoffShift {
			shift = MaxBackoffShift
		}
		t := last + c.ptoBase(spaceID)*(int64(1)<<uint(shift))
		if !found || t < info.Time {
			info = TimerInfo{Time: t, Space: spaceID, Mode: ModePTO}
			found = true
		}
	}
	considerPTO(SpaceH, &c.h)
	if c.confirmed {
		considerPTO(SpaceA, &c.a)
	}
	if found {
		return info, true
	}
	return TimerInfo{}, false
}

// OnTimeout processes exactly one due timer. A Loss timeout re-runs
// detection on the selected space; a PTO timeout increments ptoCount and
// returns one probe request without declaring losses.
func (c *Controller) OnTimeout(now int64) (*TimeoutResult, error) {
	if now < 0 || now > MaxTime {
		return nil, ErrInvalidTime
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasClock && now < c.lastNow {
		return nil, ErrClockBackwards
	}

	info, ok := c.timerLocked()
	if !ok {
		return nil, ErrNoTimer
	}
	if now < info.Time {
		return nil, ErrTimeoutEarly
	}

	c.acceptClock(now)
	sp, _ := c.target(info.Space)
	switch info.Mode {
	case ModeLoss:
		return &TimeoutResult{Loss: c.detectLocked(sp, now)}, nil
	case ModePTO:
		c.ptoCount++
		return &TimeoutResult{Probe: &ProbeRequest{Space: info.Space}}, nil
	default:
		panic("quicloss: unknown timer mode")
	}
}

// timerLocked is the lock-held body of Timer.
func (c *Controller) timerLocked() (TimerInfo, bool) {
	var info TimerInfo
	found := false
	considerLoss := func(spaceID byte, sp *space) {
		if sp.hasLT && (!found || sp.lossTime < info.Time) {
			info = TimerInfo{Time: sp.lossTime, Space: spaceID, Mode: ModeLoss}
			found = true
		}
	}
	considerLoss(SpaceH, &c.h)
	considerLoss(SpaceA, &c.a)
	if found {
		return info, true
	}

	considerPTO := func(spaceID byte, sp *space) {
		last, ok := lastAckElicit(sp)
		if !ok {
			return
		}
		shift := c.ptoCount
		if shift > MaxBackoffShift {
			shift = MaxBackoffShift
		}
		t := last + c.ptoBase(spaceID)*(int64(1)<<uint(shift))
		if !found || t < info.Time {
			info = TimerInfo{Time: t, Space: spaceID, Mode: ModePTO}
			found = true
		}
	}
	considerPTO(SpaceH, &c.h)
	if c.confirmed {
		considerPTO(SpaceA, &c.a)
	}
	return info, found
}

// HandshakeConfirmed enters the confirmed phase: the application space may
// use ack delay and arm PTOs, the handshake space is dropped without loss
// events, and the PTO backoff counter resets. Repeated calls are allowed and
// keep the confirmed state idempotent.
func (c *Controller) HandshakeConfirmed(now int64) error {
	if now < 0 || now > MaxTime {
		return ErrInvalidTime
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasClock && now < c.lastNow {
		return ErrClockBackwards
	}

	c.acceptClock(now)
	c.confirmed = true
	c.ptoCount = 0
	c.h = space{packets: make(map[int64]packet)}
	return nil
}

// Outstanding returns ascending packet-number snapshots for a live space.
// It is intended for tests, diagnostics and deterministic replay checks.
func (c *Controller) Outstanding(spaceID byte) ([]Packet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sp, err := c.target(spaceID)
	if err != nil {
		return nil, err
	}
	if spaceID == SpaceH && c.confirmed {
		return nil, ErrSpaceDiscarded
	}
	pns := make([]int64, 0, len(sp.packets))
	for pn := range sp.packets {
		pns = append(pns, pn)
	}
	sort.Slice(pns, func(i, j int) bool { return pns[i] < pns[j] })
	out := make([]Packet, 0, len(pns))
	for _, pn := range pns {
		pkt := sp.packets[pn]
		out = append(out, Packet{PN: pn, SentAt: pkt.sentAt, Size: pkt.size, AckElicit: pkt.ae})
	}
	return out, nil
}

// Snapshot exposes the RTT estimator and PTO counter for documentation tests.
type Snapshot struct {
	Latest    int64
	SRTT      int64
	RTTVar    int64
	MinRTT    int64
	HasSample bool
	PTOCount  int
	Confirmed bool
}

// RTT returns a point-in-time copy of the shared estimator state.
func (c *Controller) RTT() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{
		Latest:    c.latest,
		SRTT:      c.srtt,
		RTTVar:    c.rttvar,
		MinRTT:    c.minRTT,
		HasSample: c.hasSample,
		PTOCount:  c.ptoCount,
		Confirmed: c.confirmed,
	}
}
