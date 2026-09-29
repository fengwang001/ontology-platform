package dwellq

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sync"
)

// 可区分的拒绝原因。被拒绝的操作不会改变任何状态。
var (
	// ErrInvalidParam 表示构造参数非正（T、I、maxPacketSize、byteCapacity）。
	ErrInvalidParam = errors.New("dwellq: non-positive parameter")
	// ErrTargetTooLarge 表示 T >= I：目标逗留时间必须严格小于观察期。
	ErrTargetTooLarge = errors.New("dwellq: target T must be strictly smaller than interval I")
	// ErrInvalidPacket 表示包长非正或超过最大包长。
	ErrInvalidPacket = errors.New("dwellq: packet size non-positive or exceeds max packet size")
	// ErrClockRewind 表示时钟回拨：传入时刻早于此前观察到的最大时刻。
	ErrClockRewind = errors.New("dwellq: clock moved backwards")
)

// Packet 是队列中的包。时间戳单位由调用方约定（例如纳秒），
// 全系统（T、I、Enqueue/Dequeue 的 now）必须使用同一单位。
type Packet struct {
	// ID 为调用方提供的可复现标识，仅用于日志与结果回传，不参与判定。
	ID uint64
	// Size 为包长（字节），必须满足 1 <= Size <= maxPacketSize。
	Size int64
}

// DequeueResult 描述一次出队操作的完整结果。
type DequeueResult struct {
	// Packet 为本次返回（放行）的包；因队空返回时为 nil。
	Packet *Packet
	// Empty 为 true 表示出队前队列已空。
	Empty bool
	// Err 为拒绝原因；非 nil 时其它字段均无意义。
	Err error
	// Dropped 为本次操作过程中被主动丢弃的包（可能为空或多个）。
	Dropped []Packet
	// Reason 是人类可读的判定依据，用于可复现日志。
	Reason string
}

// Stats 为计数器快照。
type Stats struct {
	Enqueued int64
	Dequeued int64
	Dropped  int64
	InQueue  int64
	InBytes  int64
}

// Manager 是按逗留时间主动丢包的队列管理器。
// 所有方法可被并发调用。
type Manager struct {
	mu sync.Mutex

	target       int64 // 目标逗留时间 T
	interval     int64 // 观察期 I
	maxPacket    int64 // 最大包长
	capacity     int64 // 字节容量
	pkts         []queuedPacket
	head         int   // pkts[head:] 为队列中的包
	usedBytes    int64 // 当前占用字节
	enqueued     int64 // 累计入队成功数（不含尾丢）
	dequeued     int64 // 累计正常出队数
	dropped      int64 // 累计主动丢包数
	firstAbove   int64 // 非丢弃状态下首次超标时刻 + I；0 表示未设置
	dropping     bool  // 是否处于丢弃状态
	count        int64 // 丢弃状态计数
	nextDrop     int64 // 丢弃状态下的下次丢弃时刻
	lastExitTime int64 // 上次退出丢弃状态的时刻
	lastExitCnt  int64 // 上次退出丢弃状态时的计数
	hasLastExit  bool  // 是否曾退出过丢弃状态
	lastTime     int64 // 已观察到的最大时刻；未调用过操作时为 0
	hasTime      bool  // lastTime 是否有效
}

type queuedPacket struct {
	packet Packet
	enqAt  int64 // 入队时刻
}

// New 创建管理器。target=T 为目标逗留时间，interval=I 为观察期。
func New(target, interval, maxPacketSize, byteCapacity int64) (*Manager, error) {
	if target <= 0 || interval <= 0 || maxPacketSize <= 0 || byteCapacity <= 0 {
		return nil, ErrInvalidParam
	}
	if target >= interval {
		return nil, ErrTargetTooLarge
	}
	return &Manager{
		target:    target,
		interval:  interval,
		maxPacket: maxPacketSize,
		capacity:  byteCapacity,
	}, nil
}

// Enqueue 在 now 时刻入队；超容量尾丢不算错误，TailDropped=true。
func (m *Manager) Enqueue(p Packet, now int64) (ok bool, tailDropped bool, reason string, err error) {
	if p.Size <= 0 || p.Size > m.maxPacket {
		return false, false, fmt.Sprintf("enqueue id=%d size=%d at now=%d REJECTED: %s", p.ID, p.Size, now, ErrInvalidPacket), ErrInvalidPacket
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.hasTime && now < m.lastTime {
		return false, false, fmt.Sprintf("enqueue id=%d size=%d at now=%d REJECTED: %s (last=%d)", p.ID, p.Size, now, ErrClockRewind, m.lastTime), ErrClockRewind
	}

	m.lastTime = now
	m.hasTime = true

	if m.usedBytes+p.Size > m.capacity {
		// 入队超容量即尾丢：到达的包被丢弃，队列状态不变。
		return false, true, fmt.Sprintf("enqueue id=%d size=%d at now=%d TAIL_DROP: used=%d + size=%d > capacity=%d", p.ID, p.Size, now, m.usedBytes, p.Size, m.capacity), nil
	}

	m.pkts = append(m.pkts, queuedPacket{packet: p, enqAt: now})
	m.usedBytes += p.Size
	m.enqueued++
	return true, false, fmt.Sprintf("enqueue id=%d size=%d at now=%d ACCEPTED: inQueue=%d inBytes=%d", p.ID, p.Size, now, len(m.pkts)-m.head, m.usedBytes), nil
}

// Dequeue 在 now 时刻出队，必要时主动丢弃一个或多个包。
func (m *Manager) Dequeue(now int64) DequeueResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.hasTime && now < m.lastTime {
		return DequeueResult{
			Err:    ErrClockRewind,
			Reason: fmt.Sprintf("dequeue at now=%d REJECTED: %s (last=%d)", now, ErrClockRewind, m.lastTime),
		}
	}
	m.lastTime = now
	m.hasTime = true

	var dropped []Packet

	// 队空：返回空、退出丢弃状态并清除首次超标时刻。
	if m.head >= len(m.pkts) {
		m.pkts = m.pkts[:0]
		m.head = 0
		exitReason := ""
		if m.dropping {
			m.lastExitTime = now
			m.lastExitCnt = m.count
			m.hasLastExit = true
			exitReason = fmt.Sprintf("; empty queue EXIT dropping state (count=%d saved)", m.count)
		}
		m.dropping = false
		m.count = 0
		m.nextDrop = 0
		m.firstAbove = 0
		return DequeueResult{
			Empty:  true,
			Reason: fmt.Sprintf("dequeue at now=%d EMPTY: nothing to return%s", now, exitReason),
		}
	}

	if !m.dropping {
		head := m.pkts[m.head]
		remainingAfterPop := m.usedBytes - head.packet.Size
		dwell := now - head.enqAt
		overTarget := dwell >= m.target
		overBytes := remainingAfterPop > m.maxPacket
		if !overTarget || !overBytes {
			// 未超标：放行并清除首次超标时刻。
			pkt := head.packet
			m.popFront()
			m.dequeued++
			m.firstAbove = 0
			why := "dwell < T"
			if !overTarget {
				why = fmt.Sprintf("dwell=%d < T=%d", dwell, m.target)
			} else {
				why = fmt.Sprintf("remainingBytes=%d <= maxPacket=%d", remainingAfterPop, m.maxPacket)
			}
			return DequeueResult{
				Packet: &pkt,
				Reason: fmt.Sprintf("dequeue at now=%d id=%d NOT_OVER_TARGET (%s): return, clear firstAbove", now, head.packet.ID, why),
			}
		}
		if m.firstAbove == 0 {
			m.firstAbove = safeAdd(now, m.interval)
			pkt := head.packet
			m.popFront()
			m.dequeued++
			return DequeueResult{
				Packet: &pkt,
				Reason: fmt.Sprintf("dequeue at now=%d id=%d OVER_TARGET (dwell=%d>=T=%d, remainingBytes=%d>maxPacket=%d): set firstAbove=%d (now+I=%d+%d), return",
					now, head.packet.ID, dwell, m.target, remainingAfterPop, m.maxPacket, m.firstAbove, now, m.interval),
			}
		}
		if now < m.firstAbove {
			pkt := head.packet
			m.popFront()
			m.dequeued++
			return DequeueResult{
				Packet: &pkt,
				Reason: fmt.Sprintf("dequeue at now=%d id=%d OVER_TARGET but now < firstAbove=%d: return (within observation window)",
					now, head.packet.ID, m.firstAbove),
			}
		}
		// now >= firstAbove：丢弃该包并进入丢弃状态，继续判定下一个包。
		dropped = append(dropped, head.packet)
		m.discardFront()
		if m.hasLastExit && now-m.lastExitTime < 16*m.interval && m.lastExitCnt > 2 {
			m.count = m.lastExitCnt - 2
		} else {
			m.count = 1
		}
		m.dropping = true
		m.firstAbove = 0
		m.nextDrop = safeAdd(now, controlLaw(m.interval, m.count))
	}

	// 丢弃状态：循环判定。
	for m.head < len(m.pkts) {
		head := m.pkts[m.head]
		remainingAfterPop := m.usedBytes - head.packet.Size
		dwell := now - head.enqAt
		overTarget := dwell >= m.target
		overBytes := remainingAfterPop > m.maxPacket
		if !overTarget || !overBytes {
			// 未超标即退出丢弃状态并返回它。
			pkt := head.packet
			m.popFront()
			m.dequeued++
			m.dropping = false
			m.lastExitTime = now
			m.lastExitCnt = m.count
			m.hasLastExit = true
			m.count = 0
			m.nextDrop = 0
			m.firstAbove = 0
			why := "dwell < T"
			if !overTarget {
				why = fmt.Sprintf("dwell=%d < T=%d", dwell, m.target)
			} else {
				why = fmt.Sprintf("remainingBytes=%d <= maxPacket=%d", remainingAfterPop, m.maxPacket)
			}
			return DequeueResult{
				Packet:  &pkt,
				Dropped: dropped,
				Reason: fmt.Sprintf("dequeue at now=%d id=%d NOT_OVER_TARGET (%s): EXIT dropping state and return; droppedInCall=%d",
					now, head.packet.ID, why, len(dropped)),
			}
		}
		if now >= m.nextDrop {
			// 丢弃它、计数加一、下次丢弃时刻在原值上加 I/sqrt(新计数)。
			dropped = append(dropped, head.packet)
			m.discardFront()
			m.count++
			m.nextDrop = safeAdd(m.nextDrop, controlLaw(m.interval, m.count))
			continue
		}
		// 超标但未到下次丢弃时刻：返回它。
		pkt := head.packet
		m.popFront()
		m.dequeued++
		return DequeueResult{
			Packet:  &pkt,
			Dropped: dropped,
			Reason: fmt.Sprintf("dequeue at now=%d id=%d OVER_TARGET but now < nextDrop=%d: return; count=%d; droppedInCall=%d",
				now, head.packet.ID, m.nextDrop, m.count, len(dropped)),
		}
	}

	// 丢弃状态下连续丢到队空：按队空规则退出。
	m.pkts = m.pkts[:0]
	m.head = 0
	m.lastExitTime = now
	m.lastExitCnt = m.count
	m.hasLastExit = true
	m.dropping = false
	m.count = 0
	m.nextDrop = 0
	m.firstAbove = 0
	return DequeueResult{
		Empty:   true,
		Dropped: dropped,
		Reason:  fmt.Sprintf("dequeue at now=%d EMPTY after dropping %d packet(s): EXIT dropping state", now, len(dropped)),
	}
}

// Stats 返回当前计数快照。
func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Stats{
		Enqueued: m.enqueued,
		Dequeued: m.dequeued,
		Dropped:  m.dropped,
		InQueue:  int64(len(m.pkts) - m.head),
		InBytes:  m.usedBytes,
	}
}

// Dropping 报告是否处于丢弃状态（主要用于测试/可观测性）。
func (m *Manager) Dropping() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dropping
}

// StateSnapshot 是管理器内部状态的只读快照，供测试与可观测性使用。
type StateSnapshot struct {
	Dropping     bool
	Count        int64
	FirstAbove   int64
	NextDrop     int64
	LastExitTime int64
	LastExitCnt  int64
	HasLastExit  bool
	LastTime     int64
}

// State 返回内部状态快照。
func (m *Manager) State() StateSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return StateSnapshot{
		Dropping:     m.dropping,
		Count:        m.count,
		FirstAbove:   m.firstAbove,
		NextDrop:     m.nextDrop,
		LastExitTime: m.lastExitTime,
		LastExitCnt:  m.lastExitCnt,
		HasLastExit:  m.hasLastExit,
		LastTime:     m.lastTime,
	}
}

// popFront 正常放行队首包：从队列移除并推进出队计数由调用方负责。
func (m *Manager) popFront() {
	front := m.pkts[m.head]
	m.usedBytes -= front.packet.Size
	m.head++
	m.compact()
}

// discardFront 主动丢弃队首包。
func (m *Manager) discardFront() {
	front := m.pkts[m.head]
	m.usedBytes -= front.packet.Size
	m.dropped++
	m.head++
	m.compact()
}

// compact 在底层数组头部积累过多空槽时压缩，避免长期运行内存泄漏。
func (m *Manager) compact() {
	if m.head >= len(m.pkts) {
		m.pkts = m.pkts[:0]
		m.head = 0
		return
	}
	if m.head > 64 && m.head*2 >= len(m.pkts) {
		n := copy(m.pkts, m.pkts[m.head:])
		m.pkts = m.pkts[:n]
		m.head = 0
	}
}

// safeAdd 计算 a+b，溢出时返回 math.MaxInt64（时刻上界）。
func safeAdd(a, b int64) int64 {
	sum := a + b
	if sum < a {
		return math.MaxInt64
	}
	return sum
}

// controlLaw 计算 d = floor(I / sqrt(count))，
// 即满足 d^2*count <= I^2 的最大整数 d。
func controlLaw(interval, count int64) int64 {
	var i2 big.Int
	i2.Mul(big.NewInt(interval), big.NewInt(interval))
	var c big.Int
	c.SetInt64(count)
	var quot big.Int
	quot.Quo(&i2, &c)
	var d big.Int
	d.Sqrt(&quot)
	return d.Int64()
}
