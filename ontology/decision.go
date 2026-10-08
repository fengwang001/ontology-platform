package ontology

// DecisionKind 标识一次裁定决策的类型。
type DecisionKind int

const (
	// DecideFastCommit 表示无竞争时的快速路径提交。
	DecideFastCommit DecisionKind = iota
	// DecideConflict 表示裁定优胜但版本已过期的写冲突（可立即重试）。
	DecideConflict
	// DecidePreempted 表示优先级裁定落败被挤出（可稍后重试）。
	DecidePreempted
	// DecideCommit 表示裁定优胜且版本有效，获得提交权并提交。
	DecideCommit
	// DecideExhausted 表示请求在重新调度时被判定重试耗尽（最终失败）。
	DecideExhausted
)

// String 返回决策类型的可读名称。
func (k DecisionKind) String() string {
	switch k {
	case DecideFastCommit:
		return "fast_commit"
	case DecideConflict:
		return "conflict"
	case DecidePreempted:
		return "preempted"
	case DecideCommit:
		return "commit"
	case DecideExhausted:
		return "exhausted"
	default:
		return "unknown"
	}
}

// ContenderSnapshot 记录裁定轮次中一个竞争者在裁定时刻的完整优先级依据。
type ContenderSnapshot struct {
	ID         RequestID
	ArrivalSeq uint64
	Failures   uint64 // 本轮增量之前的失败次数
	Score      uint64 // 以裁定时刻逻辑时钟计算的分数
}

// Decision 是一次裁定的完整记录。每次裁定都会追加一条 Decision，
// 其中包含重放核验所需的全部输入（到达时刻、失败次数、分数）与结果。
type Decision struct {
	// Seq 是裁定发生时的实例逻辑时钟值。
	Seq uint64
	// Kind 是决策类型。
	Kind DecisionKind
	// Attempter 是触发本次裁定的请求。
	Attempter RequestID
	// Outcome 是触发本次裁定的请求所得到的结果。
	Outcome Outcome
	// Version 是本次裁定之后的实例版本号。
	// 对未提交的裁定（冲突、被挤出、耗尽），该值与裁定前一致，
	// 以此证明失败路径不产生任何版本变化。
	Version uint64
	// Ticket 是触发请求的票据在裁定后的权威值。
	Ticket Ticket
	// MaxRetries 是触发请求携带的重试上限（供耗尽判定重放）。
	MaxRetries uint64

	// AdmissionChecks 记录"新到达请求相对全部等待者的优先级判定"
	// 所执行的比较次数。本实现中该值恒为 1（空集检查），
	// 是 O(1) 准入判定的可验证证据，随决策日志自然暴露，
	// 不依赖任何额外对外暴露的状态。
	AdmissionChecks int

	// 以下字段仅在发生竞争者集合的决策（冲突、被挤出、提交）时有意义。

	// Contenders 是参与本次裁定的全部竞争者快照，按优先级从高到低排序。
	Contenders []ContenderSnapshot
	// Head 是本次裁定中优先级最高的请求。
	Head RequestID
	// RoundComparisons 是本次裁定选出最高优先级者所执行的比较次数
	// （= 竞争者数 - 1）。
	RoundComparisons int
}
