// Package quicloss implements a QUIC-style (RFC 9002) loss detection and
// probe timeout controller with two packet number spaces.
package quicloss

import (
	"fmt"
	"sort"
	"sync"
)

// Space identifies a packet number space.
type Space string

const (
	SpaceHandshake Space = "H"
	SpaceApp       Space = "A"
)

// TimerMode distinguishes loss timers from probe timeout timers.
type TimerMode string

const (
	ModeLoss TimerMode = "Loss"
	ModePTO  TimerMode = "PTO"
)

// Packet is an unacknowledged packet record.
type Packet struct {
	PN       int64
	SendTime int64
	Size     int
	AckElic  bool
}

// TimerInfo is the result of Timer.
type TimerInfo struct {
	Time  int64
	Mode  TimerMode
	Space Space
}

// ProbeRequest describes a PTO probe that must be sent.
type ProbeRequest struct {
	Space Space
	Count int
}

// TimeoutResult is the result of OnTimeout.
type TimeoutResult struct {
	Mode   TimerMode
	Space  Space
	LostPN []int64
	Probe  *ProbeRequest
}

type spaceState struct {
	packets      map[int64]*Packet
	largestAcked *int64
	lossTime     *int64
	largestSent  *int64
	lastAe       *int64
}

// Snapshot is a deep, value-comparable copy of the full controller state.
type Snapshot struct {
	Latest, SRTT, RTTVar int64
	MinRTT               *int64
	HasSample            bool
	PTOCount             int
	Confirmed            bool
	LastNow              *int64
	HDiscarded           bool
	H, A                 SpaceSnapshot
}

// SpaceSnapshot is one space within a Snapshot.
type SpaceSnapshot struct {
	Packets      []Packet
	LargestAcked *int64
	LossTime     *int64
	LargestSent  *int64
	LastAe       *int64
}

func newSpaceState() spaceState {
	return spaceState{packets: map[int64]*Packet{}}
}

// Controller is the loss detection / PTO controller. All methods are safe
// for concurrent use and linearize on a single mutex, so any concurrent
// execution is equivalent to some serial order, and replaying the same
// accepted call sequence reproduces identical results.
type Controller struct {
	mu sync.Mutex

	maxAckDelay int64

	latest    int64
	srtt      int64
	rttvar    int64
	minRtt    *int64
	hasSample bool
	ptoCount  int
	confirmed bool

	lastNow *int64

	hDiscarded bool
	h          spaceState
	a          spaceState
}

// New creates a Controller. maxAckDelay is in milliseconds, 0..10000.
func New(maxAckDelay int64) (*Controller, error) {
	if maxAckDelay < 0 || maxAckDelay > 10000 {
		return nil, fmt.Errorf("%w: maxAckDelay=%d", ErrInvalidAckDelay, maxAckDelay)
	}
	return &Controller{
		maxAckDelay: maxAckDelay,
		latest:      333,
		srtt:        333,
		rttvar:      166,
		h:           newSpaceState(),
		a:           newSpaceState(),
	}, nil
}

// Send records a packet in a space.
func (c *Controller) Send(now int64, space Space, pn int64, size int, ackElic bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := validateSpace(space); err != nil {
		return err
	}
	if size < 1 || size > 65535 {
		return fmt.Errorf("%w: size=%d", ErrInvalidSize, size)
	}
	if err := validateTime(now); err != nil {
		return err
	}
	if pn < 0 {
		return fmt.Errorf("%w: pn=%d", ErrInvalidPN, pn)
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	if space == SpaceHandshake && c.hDiscarded {
		return ErrSpaceDiscarded
	}
	sp := c.getSpace(space)
	if sp.largestSent != nil && pn <= *sp.largestSent {
		return fmt.Errorf("%w: pn=%d <= %d", ErrPNNotIncreasing, pn, *sp.largestSent)
	}

	sp.packets[pn] = &Packet{PN: pn, SendTime: now, Size: size, AckElic: ackElic}
	v := pn
	sp.largestSent = &v
	if ackElic {
		t := now
		sp.lastAe = &t
	}
	t := now
	c.lastNow = &t
	return nil
}

// Ack processes acknowledged packet numbers and returns newly detected
// losses in ascending packet-number order. Duplicates are ignored.
func (c *Controller) Ack(now int64, space Space, pns []int64, ackDelay int64) ([]int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := validateSpace(space); err != nil {
		return nil, err
	}
	if err := validateTime(now); err != nil {
		return nil, err
	}
	if len(pns) == 0 {
		return nil, ErrEmptyAck
	}
	for _, pn := range pns {
		if pn < 0 {
			return nil, fmt.Errorf("%w: pn=%d", ErrInvalidPN, pn)
		}
	}
	if ackDelay < 0 {
		return nil, fmt.Errorf("%w: ackDelay=%d", ErrInvalidAckDelay, ackDelay)
	}
	if err := c.checkClock(now); err != nil {
		return nil, err
	}
	if space == SpaceHandshake && c.hDiscarded {
		return nil, ErrSpaceDiscarded
	}
	sp := c.getSpace(space)
	for _, pn := range pns {
		if sp.largestSent == nil || pn > *sp.largestSent {
			return nil, fmt.Errorf("%w: pn=%d", ErrAckedNeverSent, pn)
		}
	}

	t := now
	c.lastNow = &t

	seen := map[int64]bool{}
	var newly []*Packet
	for _, pn := range pns {
		if seen[pn] {
			continue
		}
		seen[pn] = true
		if pkt, ok := sp.packets[pn]; ok {
			newly = append(newly, pkt)
		}
	}
	if len(newly) == 0 {
		return []int64{}, nil
	}

	var largest *Packet
	for _, pkt := range newly {
		if largest == nil || pkt.PN > largest.PN {
			largest = pkt
		}
	}
	la := largest.PN
	if sp.largestAcked == nil || la > *sp.largestAcked {
		sp.largestAcked = &la
	}

	if largest.AckElic {
		c.updateRTT(now, space, largest, ackDelay)
	}

	anyAe := false
	for _, pkt := range newly {
		if pkt.AckElic {
			anyAe = true
		}
		delete(sp.packets, pkt.PN)
	}
	if anyAe {
		c.ptoCount = 0
	}
	sp.refreshLastAe()

	return c.detectLocked(sp, now), nil
}

// Detect runs loss detection for one space and returns lost packet numbers.
func (c *Controller) Detect(space Space, now int64) ([]int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := validateSpace(space); err != nil {
		return nil, err
	}
	if err := validateTime(now); err != nil {
		return nil, err
	}
	if err := c.checkClock(now); err != nil {
		return nil, err
	}
	if space == SpaceHandshake && c.hDiscarded {
		return nil, ErrSpaceDiscarded
	}
	t := now
	c.lastNow = &t
	return c.detectLocked(c.getSpace(space), now), nil
}

// Timer returns the next firing timer, or nil when nothing is scheduled.
func (c *Controller) Timer() *TimerInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timerLocked()
}

// OnTimeout handles exactly one timer firing. A Loss firing runs detection
// for that space; a PTO firing increments ptoCount and returns a probe.
func (c *Controller) OnTimeout(now int64) (*TimeoutResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := validateTime(now); err != nil {
		return nil, err
	}
	if err := c.checkClock(now); err != nil {
		return nil, err
	}
	info := c.timerLocked()
	if info == nil || now < info.Time {
		return nil, fmt.Errorf("%w: now=%d", ErrTimeoutEarly, now)
	}
	t := now
	c.lastNow = &t

	sp := c.getSpace(info.Space)
	if info.Mode == ModeLoss {
		lost := c.detectLocked(sp, now)
		return &TimeoutResult{Mode: ModeLoss, Space: info.Space, LostPN: lost}, nil
	}
	c.ptoCount++
	return &TimeoutResult{
		Mode:   ModePTO,
		Space:  info.Space,
		LostPN: []int64{},
		Probe:  &ProbeRequest{Space: info.Space, Count: c.ptoCount},
	}, nil
}

// HandshakeConfirmed confirms the handshake, discards the H space and
// resets ptoCount. Afterwards Send/Ack/Detect on H are rejected.
func (c *Controller) HandshakeConfirmed(now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := validateTime(now); err != nil {
		return err
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	t := now
	c.lastNow = &t
	c.confirmed = true
	c.hDiscarded = true
	c.h = newSpaceState()
	c.ptoCount = 0
	return nil
}

// updateRTT applies the RFC 9002 smoothed-RTT update. The caller must hold
// c.mu and has already established that the largest newly-acked packet was
// ack-eliciting.
func (c *Controller) updateRTT(now int64, space Space, largest *Packet, ackDelay int64) {
	sample := now - largest.SendTime
	if sample < 1 {
		sample = 1
	}
	c.latest = sample
	if c.minRtt == nil || sample < *c.minRtt {
		m := sample
		c.minRtt = &m
	}
	if !c.hasSample {
		c.srtt = sample
		c.rttvar = sample / 2
		c.hasSample = true
		return
	}
	var ad int64
	if space == SpaceApp && c.confirmed {
		ad = ackDelay
		if c.maxAckDelay < ad {
			ad = c.maxAckDelay
		}
	}
	var adj int64
	if c.latest < *c.minRtt+ad {
		adj = c.latest
	} else {
		adj = c.latest - ad
	}
	diff := c.srtt - adj
	if diff < 0 {
		diff = -diff
	}
	// rttvar uses the pre-update srtt; srtt is updated after.
	c.rttvar = (3*c.rttvar + diff) / 4
	c.srtt = (7*c.srtt + adj) / 8
}

// detectLocked implements RFC 9002 DetectLoss for one space.
func (c *Controller) detectLocked(sp *spaceState, now int64) []int64 {
	sp.lossTime = nil
	lost := []int64{}
	if sp.largestAcked == nil {
		return lost
	}
	la := *sp.largestAcked
	lossDelay := max64(9*max64(c.latest, c.srtt)/8, 1)

	pns := make([]int64, 0, len(sp.packets))
	for pn := range sp.packets {
		pns = append(pns, pn)
	}
	sort.Slice(pns, func(i, j int) bool { return pns[i] < pns[j] })

	for _, pn := range pns {
		if pn >= la {
			continue
		}
		pkt := sp.packets[pn]
		if la-pn >= 3 || pkt.SendTime <= now-lossDelay {
			lost = append(lost, pn)
			delete(sp.packets, pn)
			continue
		}
		lt := pkt.SendTime + lossDelay
		if sp.lossTime == nil || lt < *sp.lossTime {
			sp.lossTime = &lt
		}
	}
	sp.refreshLastAe()
	return lost
}

func (c *Controller) timerLocked() *TimerInfo {
	// Any loss timer wins over every PTO timer.
	if !c.hDiscarded && c.h.lossTime != nil {
		if c.a.lossTime == nil || *c.h.lossTime <= *c.a.lossTime {
			return &TimerInfo{Time: *c.h.lossTime, Mode: ModeLoss, Space: SpaceHandshake}
		}
	}
	if c.a.lossTime != nil {
		if c.hDiscarded || c.h.lossTime == nil || *c.a.lossTime < *c.h.lossTime {
			return &TimerInfo{Time: *c.a.lossTime, Mode: ModeLoss, Space: SpaceApp}
		}
	}

	var best *TimerInfo
	consider := func(space Space, sp *spaceState, eligible bool) {
		if !eligible || sp.lastAe == nil {
			return
		}
		pto := c.srtt + max64(4*c.rttvar, 1)
		if space == SpaceApp {
			pto += c.maxAckDelay
		}
		shift := c.ptoCount
		if shift > 20 {
			shift = 20
		}
		at := *sp.lastAe + pto*(int64(1)<<uint(shift))
		if best == nil || at < best.Time || (at == best.Time && space == SpaceHandshake) {
			best = &TimerInfo{Time: at, Mode: ModePTO, Space: space}
		}
	}
	consider(SpaceHandshake, &c.h, !c.hDiscarded)
	consider(SpaceApp, &c.a, c.confirmed)
	return best
}

func (c *Controller) getSpace(space Space) *spaceState {
	if space == SpaceHandshake {
		return &c.h
	}
	return &c.a
}

func (c *Controller) checkClock(now int64) error {
	if c.lastNow != nil && now < *c.lastNow {
		return fmt.Errorf("%w: now=%d < %d", ErrClockBackward, now, *c.lastNow)
	}
	return nil
}

func (sp *spaceState) refreshLastAe() {
	sp.lastAe = nil
	for _, pkt := range sp.packets {
		if !pkt.AckElic {
			continue
		}
		if sp.lastAe == nil || pkt.SendTime > *sp.lastAe {
			t := pkt.SendTime
			sp.lastAe = &t
		}
	}
}

func validateSpace(space Space) error {
	if space != SpaceHandshake && space != SpaceApp {
		return fmt.Errorf("%w: %q", ErrInvalidSpace, space)
	}
	return nil
}

func validateTime(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return fmt.Errorf("%w: now=%d", ErrInvalidTime, now)
	}
	return nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// snapshotLocked returns a deep copy of all state (caller holds c.mu).
func (c *Controller) snapshotLocked() Snapshot {
	snap := Snapshot{
		Latest: c.latest, SRTT: c.srtt, RTTVar: c.rttvar,
		MinRTT: cpInt(c.minRtt), HasSample: c.hasSample, PTOCount: c.ptoCount,
		Confirmed: c.confirmed, LastNow: cpInt(c.lastNow), HDiscarded: c.hDiscarded,
		H: c.h.snapshot(), A: c.a.snapshot(),
	}
	return snap
}

func (sp *spaceState) snapshot() SpaceSnapshot {
	s := SpaceSnapshot{
		LargestAcked: cpInt(sp.largestAcked),
		LossTime:     cpInt(sp.lossTime),
		LargestSent:  cpInt(sp.largestSent),
		LastAe:       cpInt(sp.lastAe),
	}
	for _, p := range sp.packets {
		s.Packets = append(s.Packets, *p)
	}
	sort.Slice(s.Packets, func(i, j int) bool { return s.Packets[i].PN < s.Packets[j].PN })
	return s
}

func cpInt(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
