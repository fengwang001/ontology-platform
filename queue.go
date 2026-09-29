package dwellqueue

import (
	"errors"
	"math"
	"math/big"
	"sync"
	"time"
)

var (
	ErrNonPositiveParameter = errors.New("dwellqueue: parameter must be positive")
	ErrTargetTooLarge       = errors.New("dwellqueue: target delay must be smaller than observation interval")
	ErrInvalidPacketLength  = errors.New("dwellqueue: packet length must be positive")
	ErrPacketTooLarge       = errors.New("dwellqueue: packet length exceeds maximum packet size")
	ErrClockRollback        = errors.New("dwellqueue: clock moved backwards")
)

type Reason string

const (
	ReasonEnqueued      Reason = "enqueued"
	ReasonTailDropped   Reason = "tail_dropped"
	ReasonReturned      Reason = "returned"
	ReasonActiveDrop    Reason = "active_dropped"
	ReasonEnterDropping Reason = "enter_dropping"
	ReasonExitDropping  Reason = "exit_dropping"
	ReasonEmpty         Reason = "empty"
)

type EnqueueResult struct {
	Sequence uint64
	Length   int
	Accepted bool
	Reason   Reason
	Bytes    int
}

type DropDecision struct {
	Sequence   uint64
	Length     int
	EnqueuedAt time.Duration
	DwellTime  time.Duration
	Count      int
	NextDropAt time.Duration
}

type DequeueResult struct {
	Sequence    uint64
	Packet      []byte
	Length      int
	EnqueuedAt  time.Duration
	DwellTime   time.Duration
	Reason      Reason
	Dropped     []DropDecision
	Count       int
	NextDropAt  time.Duration
	FirstOverAt time.Duration
	Bytes       int
}

type Stats struct {
	EnqueueSuccesses uint64
	Dequeues         uint64
	ActiveDrops      uint64
	TailDrops        uint64
	Packets          int
	Bytes            int
	Count            int
	Dropping         bool
}

type Manager struct {
	mu               sync.Mutex
	target           time.Duration
	interval         time.Duration
	maxPacket        int
	capacity         int
	queue            []packet
	bytes            int
	nextSequence     uint64
	lastClock        time.Duration
	haveClock        bool
	firstExceededAt  time.Duration
	firstExceededSet bool
	nextDropAt       time.Duration
	count            int
	dropping         bool
	lastExitAt       time.Duration
	haveLastExit     bool
	lastExitCount    int
	enqueueSuccesses uint64
	dequeues         uint64
	activeDrops      uint64
	tailDrops        uint64
}

type packet struct {
	sequence   uint64
	data       []byte
	length     int
	enqueuedAt time.Duration
}

func New(target, interval time.Duration, maxPacketSize, capacityBytes int) (*Manager, error) {
	if target <= 0 || interval <= 0 || maxPacketSize <= 0 || capacityBytes <= 0 {
		return nil, ErrNonPositiveParameter
	}
	if target >= interval {
		return nil, ErrTargetTooLarge
	}
	return &Manager{
		target:       target,
		interval:     interval,
		maxPacket:    maxPacketSize,
		capacity:     capacityBytes,
		nextSequence: 1,
	}, nil
}

func (m *Manager) Enqueue(data []byte, now time.Duration) (EnqueueResult, error) {
	return m.enqueue(data, now, false)
}

func (m *Manager) enqueue(data []byte, now time.Duration, useNextClock bool) (EnqueueResult, error) {
	length := len(data)
	if length <= 0 {
		return EnqueueResult{}, ErrInvalidPacketLength
	}
	if length > m.maxPacket {
		return EnqueueResult{}, ErrPacketTooLarge
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if useNextClock {
		now = m.advanceClockLocked()
	}
	if !m.checkClock(now) {
		return EnqueueResult{}, ErrClockRollback
	}

	result := EnqueueResult{
		Length: length,
		Bytes:  m.bytes,
	}
	if m.bytes+length > m.capacity {
		result.Sequence = 0
		result.Reason = ReasonTailDropped
		m.tailDrops++
		return result, nil
	}

	packetData := append([]byte(nil), data...)
	sequence := m.nextSequence
	m.nextSequence++
	m.queue = append(m.queue, packet{
		sequence:   sequence,
		data:       packetData,
		length:     length,
		enqueuedAt: now,
	})
	m.bytes += length
	m.enqueueSuccesses++

	result.Sequence = sequence
	result.Accepted = true
	result.Reason = ReasonEnqueued
	result.Bytes = m.bytes
	return result, nil
}

func (m *Manager) Dequeue(now time.Duration) (DequeueResult, error) {
	return m.dequeue(now, false)
}

func (m *Manager) dequeue(now time.Duration, useNextClock bool) (DequeueResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if useNextClock {
		now = m.advanceClockLocked()
	}
	if !m.checkClock(now) {
		return DequeueResult{}, ErrClockRollback
	}

	if len(m.queue) == 0 {
		m.exitDropping(now)
		m.clearFirstExceeded()
		return m.emptyResult(now), nil
	}

	if !m.dropping {
		return m.dequeueNormal(now)
	}
	return m.dequeueDropping(now)
}

func (m *Manager) advanceClockLocked() time.Duration {
	m.lastClock++
	m.haveClock = true
	return m.lastClock
}

func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()

	return Stats{
		EnqueueSuccesses: m.enqueueSuccesses,
		Dequeues:         m.dequeues,
		ActiveDrops:      m.activeDrops,
		TailDrops:        m.tailDrops,
		Packets:          len(m.queue),
		Bytes:            m.bytes,
		Count:            m.count,
		Dropping:         m.dropping,
	}
}

func (m *Manager) checkClock(now time.Duration) bool {
	if m.haveClock && now < m.lastClock {
		return false
	}
	m.lastClock = now
	m.haveClock = true
	return true
}

func (m *Manager) dequeueNormal(now time.Duration) (DequeueResult, error) {
	current := m.queue[0]
	dwell := now - current.enqueuedAt
	if !m.isExceeded(current, dwell) {
		m.clearFirstExceeded()
		return m.returnPacket(current, dwell, ReasonReturned), nil
	}

	if !m.firstExceededSet {
		m.firstExceededAt = saturatingAdd(now, m.interval)
		m.firstExceededSet = true
		return m.returnPacket(current, dwell, ReasonReturned), nil
	}
	if now < m.firstExceededAt {
		return m.returnPacket(current, dwell, ReasonReturned), nil
	}

	m.enterDropping(now)
	decision := m.dropHead(now)
	if len(m.queue) == 0 {
		m.exitDropping(now)
		m.clearFirstExceeded()
		result := m.emptyResult(now)
		result.Reason = ReasonEnterDropping
		result.Dropped = []DropDecision{decision}
		return result, nil
	}
	return m.continueDropping(now, []DropDecision{decision}, ReasonEnterDropping)
}

func (m *Manager) dequeueDropping(now time.Duration) (DequeueResult, error) {
	return m.continueDropping(now, nil, ReasonExitDropping)
}

func (m *Manager) continueDropping(now time.Duration, dropped []DropDecision, entryReason Reason) (DequeueResult, error) {
	for len(m.queue) > 0 {
		current := m.queue[0]
		dwell := now - current.enqueuedAt
		if !m.isExceeded(current, dwell) {
			m.exitDropping(now)
			m.clearFirstExceeded()
			result := m.returnPacket(current, dwell, ReasonExitDropping)
			result.Dropped = dropped
			return result, nil
		}

		if now < m.nextDropAt {
			result := m.returnPacket(current, dwell, ReasonReturned)
			result.Dropped = dropped
			return result, nil
		}

		decision := m.dropHead(now)
		m.count++
		m.nextDropAt = saturatingAdd(m.nextDropAt, dropInterval(m.interval, m.count))
		decision.Count = m.count
		decision.NextDropAt = m.nextDropAt
		dropped = append(dropped, decision)
	}

	m.exitDropping(now)
	m.clearFirstExceeded()
	result := m.emptyResult(now)
	result.Reason = entryReason
	result.Dropped = dropped
	return result, nil
}

func (m *Manager) isExceeded(current packet, dwell time.Duration) bool {
	if dwell < m.target {
		return false
	}
	remainingAfterReturn := m.bytes - current.length
	return remainingAfterReturn > m.maxPacket
}

func (m *Manager) dropHead(now time.Duration) DropDecision {
	current := m.queue[0]
	dwell := now - current.enqueuedAt
	decision := DropDecision{
		Sequence:   current.sequence,
		Length:     current.length,
		EnqueuedAt: current.enqueuedAt,
		DwellTime:  dwell,
		Count:      m.count,
		NextDropAt: m.nextDropAt,
	}

	m.queue = m.queue[1:]
	m.bytes -= current.length
	m.activeDrops++
	return decision
}

func (m *Manager) enterDropping(now time.Duration) {
	m.dropping = true
	m.clearFirstExceeded()
	if m.haveLastExit && now-m.lastExitAt < saturatingScale(m.interval, 16) && m.lastExitCount > 2 {
		m.count = m.lastExitCount - 2
	} else {
		m.count = 1
	}
	m.nextDropAt = saturatingAdd(now, dropInterval(m.interval, m.count))
}

func (m *Manager) clearFirstExceeded() {
	m.firstExceededAt = 0
	m.firstExceededSet = false
}

func (m *Manager) exitDropping(now time.Duration) {
	if !m.dropping {
		return
	}
	m.dropping = false
	m.lastExitAt = now
	m.haveLastExit = true
	m.lastExitCount = m.count
	m.count = 0
	m.nextDropAt = 0
}

func (m *Manager) returnPacket(current packet, dwell time.Duration, reason Reason) DequeueResult {
	m.queue = m.queue[1:]
	m.bytes -= current.length
	m.dequeues++
	return DequeueResult{
		Sequence:    current.sequence,
		Packet:      append([]byte(nil), current.data...),
		Length:      current.length,
		EnqueuedAt:  current.enqueuedAt,
		DwellTime:   dwell,
		Reason:      reason,
		Count:       m.count,
		NextDropAt:  m.nextDropAt,
		FirstOverAt: m.firstExceededAt,
		Bytes:       m.bytes,
	}
}

func (m *Manager) emptyResult(now time.Duration) DequeueResult {
	return DequeueResult{
		Reason:      ReasonEmpty,
		Count:       m.count,
		NextDropAt:  m.nextDropAt,
		FirstOverAt: m.firstExceededAt,
		Bytes:       m.bytes,
	}
}

func dropInterval(interval time.Duration, count int) time.Duration {
	if count <= 0 {
		return 0
	}
	value := new(big.Int).SetInt64(int64(interval))
	value.Mul(value, value)
	value.Quo(value, big.NewInt(int64(count)))
	value.Sqrt(value)
	return time.Duration(value.Int64())
}

func saturatingAdd(left, right time.Duration) time.Duration {
	if right > 0 && left > math.MaxInt64-right {
		return math.MaxInt64
	}
	if right < 0 && left < math.MinInt64-right {
		return math.MinInt64
	}
	return left + right
}

func saturatingScale(value time.Duration, factor int64) time.Duration {
	result := new(big.Int).Mul(big.NewInt(int64(value)), big.NewInt(factor))
	if result.IsInt64() {
		return time.Duration(result.Int64())
	}
	if result.Sign() >= 0 {
		return math.MaxInt64
	}
	return math.MinInt64
}
