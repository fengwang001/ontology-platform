// Package watermark 维护事件时间与处理时间两套相互独立的水位。
//
// 迟到判定只以事件时间水位为准；处理时间水位仅由心跳推进，用于表达
// “处理进度”这一外部观测，绝不参与迟到判定，也不改变事件时间相关状态。
package watermark

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Reason 是一次被整体拒绝的调用的可区分原因。
type Reason string

const (
	// ReasonNegativeAllowedLateness 表示构造时允许迟到值为负。
	ReasonNegativeAllowedLateness Reason = "negative_allowed_lateness"
	// ReasonEmptyID 表示摄入事件使用了空标识。
	ReasonEmptyID Reason = "empty_event_id"
	// ReasonHeartbeatRegression 表示心跳时间早于（或语义上回退于）当前处理水位。
	ReasonHeartbeatRegression Reason = "heartbeat_regression"
)

// RejectError 表示一次因输入非法而被整体拒绝的调用。
// 被拒绝的调用不改变任何状态。
type RejectError struct {
	Reason Reason
	msg    string
}

func (e *RejectError) Error() string { return e.msg }

// Decision 是一次摄入的迟到判定结果。
type Decision string

const (
	// DecisionAccepted 表示事件按时到达（事件时间严格大于事件时间水位）。
	DecisionAccepted Decision = "accepted"
	// DecisionLate 表示事件迟到（事件时间不超过事件时间水位），被丢弃并计入迟到计数。
	DecisionLate Decision = "late"
)

// Event 是一条被按时接受的事件的只读视图。
type Event struct {
	ID   string
	Time time.Time
}

// IngestResult 是一次摄入调用的判定结果与判定依据。
type IngestResult struct {
	EventID             string
	EventTime           time.Time
	Decision            Decision
	EventWatermark      time.Time
	EventWatermarkSet   bool
	ProcessingWatermark time.Time
	ProcessingSet       bool
	// Basis 用自然语言说明判定依据，便于单测日志复核。
	Basis string
}

// View 是某一时刻维护器状态的一致快照。
type View struct {
	Accepted            []Event
	EventWatermark      time.Time
	EventWatermarkSet   bool
	ProcessingWatermark time.Time
	ProcessingSet       bool
	AcceptedCount       int
	LateCount           int
	AllowedLateness     time.Duration
}

// Maintainer 是并发安全的双时钟水位维护器。
type Maintainer struct {
	mu sync.Mutex

	allowedLateness time.Duration

	// 事件时间侧：水位 = 已见最大事件时间 - 允许迟到；未见过事件时为负无穷。
	hasMaxEvent  bool
	maxEventTime time.Time

	// 处理时间侧：仅由心跳推进。
	hasProcessing bool
	processingWM  time.Time

	accepted  []Event
	lateCount int
}

func reject(reason Reason, format string, args ...any) error {
	return &RejectError{Reason: reason, msg: fmt.Sprintf(format, args...)}
}

// eventWatermarkLocked 调用方必须持有 m.mu。
func (m *Maintainer) eventWatermarkLocked() (time.Time, bool) {
	if !m.hasMaxEvent {
		return time.Time{}, false
	}
	return m.maxEventTime.Add(-m.allowedLateness), true
}

// New 创建一个维护器。allowedLateness 为负时整体拒绝。
func New(allowedLateness time.Duration) (*Maintainer, error) {
	if allowedLateness < 0 {
		return nil, reject(ReasonNegativeAllowedLateness,
			"allowed lateness must be non-negative, got %s", allowedLateness)
	}
	return &Maintainer{allowedLateness: allowedLateness}, nil
}

// Ingest 摄入一条事件。迟到事件被丢弃并计入迟到计数；
// 空标识等非法输入被整体拒绝，不改变任何状态。
func (m *Maintainer) Ingest(id string, eventTime time.Time) (IngestResult, error) {
	if id == "" {
		return IngestResult{}, reject(ReasonEmptyID, "event id must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	wm, wmSet := m.eventWatermarkLocked()
	res := IngestResult{
		EventID:             id,
		EventTime:           eventTime,
		EventWatermark:      wm,
		EventWatermarkSet:   wmSet,
		ProcessingWatermark: m.processingWM,
		ProcessingSet:       m.hasProcessing,
	}

	// 迟到判定只看事件时间水位：事件时间不超过水位即迟到。
	// 未见过事件时水位为负无穷，任何事件时间都严格大于它，一律按时。
	late := wmSet && !eventTime.After(wm)
	if late {
		m.lateCount++
		res.Decision = DecisionLate
		res.Basis = fmt.Sprintf(
			"event_time=%s <= event_watermark=%s (max_seen=%s - allowed_lateness=%s); processing watermark %s is never consulted",
			eventTime.Format(time.RFC3339Nano), wm.Format(time.RFC3339Nano),
			m.maxEventTime.Format(time.RFC3339Nano), m.allowedLateness,
			processingDesc(m.hasProcessing, m.processingWM))
		return res, nil
	}

	m.accepted = append(m.accepted, Event{ID: id, Time: eventTime})
	if !m.hasMaxEvent || eventTime.After(m.maxEventTime) {
		m.maxEventTime = eventTime
		m.hasMaxEvent = true
	}

	newWM, _ := m.eventWatermarkLocked()
	res.Decision = DecisionAccepted
	res.EventWatermark = newWM
	res.EventWatermarkSet = true
	if wmSet {
		res.Basis = fmt.Sprintf(
			"event_time=%s > event_watermark=%s; accepted and event watermark advances to %s",
			eventTime.Format(time.RFC3339Nano), wm.Format(time.RFC3339Nano),
			newWM.Format(time.RFC3339Nano))
	} else {
		res.Basis = fmt.Sprintf(
			"event watermark is -infinity before the first event; event_time=%s accepted and event watermark initializes to %s",
			eventTime.Format(time.RFC3339Nano), newWM.Format(time.RFC3339Nano))
	}
	return res, nil
}

// Heartbeat 仅推进处理时间水位，只进不退；不参与迟到判定。
func (m *Maintainer) Heartbeat(now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.hasProcessing && now.Before(m.processingWM) {
		return reject(ReasonHeartbeatRegression,
			"heartbeat time %s must not be before current processing watermark %s",
			now.Format(time.RFC3339Nano), m.processingWM.Format(time.RFC3339Nano))
	}
	m.processingWM = now
	m.hasProcessing = true
	return nil
}

// View 返回状态的一致快照（已接受事件按摄入顺序排列）。
func (m *Maintainer) View() View {
	m.mu.Lock()
	defer m.mu.Unlock()

	wm, wmSet := m.eventWatermarkLocked()
	accepted := make([]Event, len(m.accepted))
	copy(accepted, m.accepted)
	return View{
		Accepted:            accepted,
		EventWatermark:      wm,
		EventWatermarkSet:   wmSet,
		ProcessingWatermark: m.processingWM,
		ProcessingSet:       m.hasProcessing,
		AcceptedCount:       len(m.accepted),
		LateCount:           m.lateCount,
		AllowedLateness:     m.allowedLateness,
	}
}

// CheckInvariants 对当前状态做不变量自检，返回第一个被违反的不变量。
func (m *Maintainer) CheckInvariants() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.allowedLateness < 0 {
		return errors.New("allowed lateness must be non-negative")
	}
	if m.lateCount < 0 {
		return errors.New("late count must be non-negative")
	}
	if len(m.accepted) < 0 {
		return errors.New("accepted slice cannot have negative length")
	}
	if !m.hasMaxEvent {
		if len(m.accepted) != 0 {
			return errors.New("accepted events exist but max event time is unset")
		}
	} else {
		if len(m.accepted) == 0 {
			return errors.New("max event time is set but no accepted event exists")
		}
		var maxSeen time.Time
		for i, ev := range m.accepted {
			if i == 0 || ev.Time.After(maxSeen) {
				maxSeen = ev.Time
			}
		}
		if !maxSeen.Equal(m.maxEventTime) {
			return fmt.Errorf("stored max event time %s != recomputed %s",
				m.maxEventTime.Format(time.RFC3339Nano), maxSeen.Format(time.RFC3339Nano))
		}
		wm, _ := m.eventWatermarkLocked()
		want := maxSeen.Add(-m.allowedLateness)
		if !wm.Equal(want) {
			return fmt.Errorf("event watermark %s != max_seen %s - allowed_lateness %s (= %s)",
				wm.Format(time.RFC3339Nano), maxSeen.Format(time.RFC3339Nano),
				m.allowedLateness, want.Format(time.RFC3339Nano))
		}
	}
	if !m.hasProcessing && !m.processingWM.IsZero() {
		return errors.New("processing watermark has a value while its presence flag is unset")
	}
	return nil
}

func processingDesc(set bool, t time.Time) string {
	if !set {
		return "-infinity"
	}
	return t.Format(time.RFC3339Nano)
}
