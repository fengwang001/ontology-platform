package watermark

import (
	"math"
	"sort"
	"sync"
)

// Maintainer 是双时钟水位维护器：事件时间与处理时间两套水位完全独立。
//
// 事件时间水位 = 已见最大事件时间 - 允许迟到；未见过事件时为负无穷。
// 迟到判定只比较事件时间与该水位；处理时间（含心跳）绝不参与判定，
// 也不改变任何事件时间相关状态。所有方法均为并发安全，两个水位各自只进不退。
type Maintainer struct {
	mu              sync.Mutex
	allowedLateness int64
	hasEvent        bool
	maxEventTime    int64
	hasHeartbeat    bool
	procWatermark   int64
	accepted        map[string]struct{}
	acceptedEvent   map[string]int64
	lateCount       int
	seq             int64
	log             []op
}

// New 创建维护器。allowedLateness 必须非负，否则返回 RejectNegativeAllowedLateness。
func New(allowedLateness int64) (*Maintainer, error) {
	if allowedLateness < 0 {
		return nil, &RejectError{Reason: RejectNegativeAllowedLateness}
	}
	return &Maintainer{
		allowedLateness: allowedLateness,
		accepted:        map[string]struct{}{},
		acceptedEvent:   map[string]int64{},
	}, nil
}

// eventWatermarkLocked 在持锁状态下计算事件时间水位。
// 第二返回值为 true 表示尚未见过事件，水位为负无穷。
func (m *Maintainer) eventWatermarkLocked() (int64, bool) {
	if !m.hasEvent {
		return 0, true
	}
	return m.maxEventTime - m.allowedLateness, false
}

// Ingest 摄入一条事件并给出其被处理时的处理时间戳。
//
// processingTime 仅记录到操作日志用于复现核对：它绝不参与迟到判定，
// 也绝不推进处理时间水位（处理时间水位只能由 Heartbeat 推进）。
// 空标识返回 RejectEmptyID，且不改变任何状态。
// 事件时间不超过事件时间水位即为迟到：计入迟到计数并丢弃，不推进任何水位；
// 否则按时接受，推进事件时间水位（由最大事件时间驱动），并加入已接受视图。
func (m *Maintainer) Ingest(e Event, processingTime int64) (Decision, error) {
	if e.ID == "" {
		return "", &RejectError{Reason: RejectEmptyID}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	wm, negInf := m.eventWatermarkLocked()
	if !negInf && e.EventTime <= wm {
		m.lateCount++
		m.appendLocked(opIngest, e.ID, e.EventTime, processingTime)
		return DecisionLate, nil
	}

	if !m.hasEvent || e.EventTime > m.maxEventTime {
		m.maxEventTime = e.EventTime
		m.hasEvent = true
	}
	if _, seen := m.accepted[e.ID]; !seen {
		m.accepted[e.ID] = struct{}{}
		m.acceptedEvent[e.ID] = e.EventTime
	}
	m.appendLocked(opIngest, e.ID, e.EventTime, processingTime)
	return DecisionAccepted, nil
}

// Heartbeat 仅推进处理时间水位，只进不退；它绝不参与迟到判定，
// 也绝不改变事件时间水位、已接受事件与迟到计数。
// 相对当前处理时间水位回退的心跳返回 RejectHeartbeatRegress，且不改变任何状态。
func (m *Maintainer) Heartbeat(processingTime int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.hasHeartbeat && processingTime < m.procWatermark {
		return &RejectError{Reason: RejectHeartbeatRegress}
	}

	m.procWatermark = processingTime
	m.hasHeartbeat = true
	m.appendLocked(opHeartbeat, "", 0, processingTime)
	return nil
}

func (m *Maintainer) appendLocked(kind opKind, id string, eventTime, processingTime int64) {
	m.seq++
	m.log = append(m.log, op{
		kind:       kind,
		seq:        m.seq,
		id:         id,
		eventTime:  eventTime,
		processing: processingTime,
	})
}

// Snapshot 返回当前状态的不可变视图。可与摄入、心跳及其他查询并发调用。
// 两个水位为负无穷（未见事件 / 未见心跳）时用 math.MinInt64 作哨兵，
// 事件时间水位另以 EventWatermarkNegInf 明确标识。
func (m *Maintainer) Snapshot() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Maintainer) snapshotLocked() View {
	wm, negInf := m.eventWatermarkLocked()
	ids := make([]string, 0, len(m.accepted))
	for id := range m.accepted {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	proc := int64(math.MinInt64)
	if m.hasHeartbeat {
		proc = m.procWatermark
	}
	return View{
		EventWatermark:       wm,
		EventWatermarkNegInf: negInf,
		ProcessingWatermark:  proc,
		AcceptedEvents:       ids,
		LateCount:            m.lateCount,
	}
}

// SelfCheck 校验内部不变量并用操作日志做按序重放核对；
// 无异常返回空串，否则返回异常描述。可与任何方法并发调用。
func (m *Maintainer) SelfCheck() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.allowedLateness < 0 {
		return "allowed lateness must be non-negative"
	}
	if m.hasEvent != (len(m.accepted) > 0) {
		return "hasEvent flag inconsistent with accepted set"
	}
	if len(m.accepted) != len(m.acceptedEvent) {
		return "accepted set and event-time index diverged"
	}
	var maxSeen int64
	for id := range m.accepted {
		if et := m.acceptedEvent[id]; et > maxSeen {
			maxSeen = et
		}
	}
	if m.hasEvent && maxSeen != m.maxEventTime {
		return "max event time inconsistent with accepted events"
	}
	for i, o := range m.log {
		if o.seq != int64(i+1) {
			return "operation log sequence is not contiguous"
		}
	}
	replayed, err := m.replayLocked()
	if err != nil {
		return "replay failed: " + err.Error()
	}
	if !viewsEqual(replayed.snapshotLocked(), m.snapshotLocked()) {
		return "replayed state diverges from current state"
	}
	return ""
}

func viewsEqual(a, b View) bool {
	if a.EventWatermark != b.EventWatermark ||
		a.EventWatermarkNegInf != b.EventWatermarkNegInf ||
		a.ProcessingWatermark != b.ProcessingWatermark ||
		a.LateCount != b.LateCount ||
		len(a.AcceptedEvents) != len(b.AcceptedEvents) {
		return false
	}
	for i := range a.AcceptedEvents {
		if a.AcceptedEvents[i] != b.AcceptedEvents[i] {
			return false
		}
	}
	return true
}
