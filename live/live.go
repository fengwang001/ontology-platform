package live

// Kind 是对局事件种类。
type Kind int

const (
	Normal Kind = iota
	Hidden
	End
)

// Event 是一条对局事件, Seq 从 1 起连续递增, Now 为事件时刻 t。
type Event struct {
	Seq  int64
	Now  int64
	Kind Kind
}

// Stream 是只增的对局事件流, 自带单调时钟与结束态。
type Stream struct {
	events  []Event
	maxNow  int64
	ended   bool
	endTime int64
}

// NewStream 创建空事件流。
func NewStream() *Stream {
	return &Stream{}
}

// Emit 追加一条事件。kind 必须合法; now 不得小于此前接受的最大 now;
// End 之后再 Emit 报已结束。被拒绝时不改变任何状态。
func (s *Stream) Emit(now int64, kind Kind) (Event, error) {
	if kind != Normal && kind != Hidden && kind != End {
		return Event{}, ErrInvalidKind
	}
	if now < s.maxNow {
		return Event{}, ErrClockRollback
	}
	if s.ended {
		return Event{}, ErrAlreadyEnded
	}
	e := Event{Seq: int64(len(s.events) + 1), Now: now, Kind: kind}
	s.events = append(s.events, e)
	s.maxNow = now
	if kind == End {
		s.ended = true
		s.endTime = now
	}
	return e, nil
}

// MaxNow 返回已接受 Emit 的最大 now。
func (s *Stream) MaxNow() int64 { return s.maxNow }

// At 返回 1 起算的 seq 对应事件。
func (s *Stream) At(seq int64) (Event, bool) {
	if seq < 1 || seq > int64(len(s.events)) {
		return Event{}, false
	}
	return s.events[seq-1], true
}

// Len 返回事件总数。
func (s *Stream) Len() int { return len(s.events) }

// Ended 报告是否已出现 End 事件。
func (s *Stream) Ended() bool { return s.ended }

// EndTime 返回 End 事件时刻 tE。
func (s *Stream) EndTime() (int64, bool) {
	if !s.ended {
		return 0, false
	}
	return s.endTime, true
}
