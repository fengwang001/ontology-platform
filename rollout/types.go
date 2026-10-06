package rollout

// Version 标识请求路由的目标版本。
type Version int

const (
	VersionStable Version = 0 // 稳定版本
	VersionCanary Version = 1 // 灰度版本
)

func (v Version) String() string {
	switch v {
	case VersionStable:
		return "stable"
	case VersionCanary:
		return "canary"
	default:
		return "invalid"
	}
}

// Phase 是切分器的宏观状态。
type Phase int

const (
	PhaseNotStarted Phase = iota // 未开始：比例为 0
	PhaseRunning                 // 进行中
	PhaseCompleted               // 已完成：保持最后一阶段比例
	PhaseRolledBack              // 已回滚：比例为 0，仅人工重置可离开
)

func (p Phase) String() string {
	switch p {
	case PhaseNotStarted:
		return "not_started"
	case PhaseRunning:
		return "running"
	case PhaseCompleted:
		return "completed"
	case PhaseRolledBack:
		return "rolled_back"
	default:
		return "unknown"
	}
}

// RouteSource 标识一次路由判定的来源。
type RouteSource int

const (
	SourceForceStable RouteSource = iota // 回滚态强制稳定
	SourceSticky                         // 会话粘性沿用
	SourceProportion                     // 按比例归属
)

func (s RouteSource) String() string {
	switch s {
	case SourceForceStable:
		return "force_stable"
	case SourceSticky:
		return "sticky"
	case SourceProportion:
		return "proportion"
	default:
		return "unknown"
	}
}

// EvalResult 是周期性评估的判定结果。
type EvalResult int

const (
	EvalDwellNotMet  EvalResult = iota // 驻留未满
	EvalInsufficient                   // 灰度样本不足
	EvalPassed                         // 通过并晋级（或完成）
	EvalFailed                         // 未通过，累计失败
)

func (r EvalResult) String() string {
	switch r {
	case EvalDwellNotMet:
		return "dwell_not_met"
	case EvalInsufficient:
		return "insufficient_sample"
	case EvalFailed:
		return "failed"
	case EvalPassed:
		return "passed"
	default:
		return "unknown"
	}
}

// RouteDecision 是一次路由的返回值。
type RouteDecision struct {
	Version Version
	Source  RouteSource
}

// Snapshot 是切分器的只读状态快照。
type Snapshot struct {
	Phase       Phase
	PhaseIndex  int   // 仅 Running 有意义
	Ratio       int   // 当前比例（万分之一）
	EnteredAtMs int64 // 当前阶段进入时刻（毫秒）；非 Running 时为 0
	FailStreak  int   // 连续失败累计
	StickyCount int   // 当前粘性记录条数
	LastClockMs int64 // 最近一次注入时钟
}
