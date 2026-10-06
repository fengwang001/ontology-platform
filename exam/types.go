package exam

// Config 为引擎级配置，创建后不变。
type Config struct {
	AnswerBudget   int64 // 每个会话的作答时长预算
	PauseBudget    int64 // 暂停总预算，超出部分计入作答时间
	SinglePauseMax int64 // 单次暂停上限，超过则会话自动结束
	Window         int64 // 异常事件滑动窗口长度，区间为 (now-Window, now]
	WarnThreshold  int   // 窗口内事件数达到该值记一次警告
	LockThreshold  int   // 窗口内事件数达到该值会话锁定
	WarnLimit      int   // 警告次数上限，达到则会话结束并记违规
}

// Status 为会话状态。
type Status int

const (
	StatusActive Status = iota
	StatusPaused
	StatusLocked
	StatusEnded
)

func (s Status) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusPaused:
		return "paused"
	case StatusLocked:
		return "locked"
	case StatusEnded:
		return "ended"
	}
	return "unknown"
}

// EndReason 为会话结束原因。
type EndReason int

const (
	EndReasonNone EndReason = iota
	EndReasonSubmitted
	EndReasonTimeExhausted
	EndReasonDeadline
	EndReasonPauseLimit
	EndReasonViolation
)

func (r EndReason) String() string {
	switch r {
	case EndReasonSubmitted:
		return "submitted"
	case EndReasonTimeExhausted:
		return "time-exhausted"
	case EndReasonDeadline:
		return "deadline"
	case EndReasonPauseLimit:
		return "pause-limit"
	case EndReasonViolation:
		return "violation"
	}
	return "none"
}

// EventKind 为异常事件类型。
type EventKind int

const (
	EventLeavePage EventKind = iota + 1
	EventSwitchWindow
)

// Answer 为一条作答记录。ClientTime 仅作记录，不参与任何判定。
type Answer struct {
	QuestionID string
	Seq        int64
	Generation int64
	Payload    string
	ClientTime int64
}

// LandedAnswer 为某题已落定的作答。
type LandedAnswer struct {
	Seq        int64
	Payload    string
	ClientTime int64
}

// Settlement 为会话结算结果，一经完成不可变。
type Settlement struct {
	Answers             map[string]LandedAnswer
	EffectiveAnswerTime int64
	TotalPauseTime      int64
	Warnings            int
	Violation           bool
	EndAt               int64
	Reason              EndReason
}
