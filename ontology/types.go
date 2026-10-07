// Package ontology 提供本体平台对象实例的乐观并发控制，
// 支持按动作权限等级进行抢占式终止的低权限重试循环。
package ontology

// Priority 表示动作的权限等级，数值越大权限越高。
type Priority int

// Mutation 描述一次对对象实例属性的确定性修改。
type Mutation struct {
	Set map[string]any
	Del []string
}

// Snapshot 是对象实例在某一版本上的只读快照，作为乐观并发的判定基线。
type Snapshot struct {
	Version int64
	Props   map[string]any
}

// FinalKind 是一个动作执行的最终互斥结果。
type FinalKind int

const (
	// Committed 表示动作成功提交。
	Committed FinalKind = iota
	// Preempted 表示动作被更高权限动作的已生效写入抢占而立即终止。
	Preempted
	// RetriesExhausted 表示动作因普通版本冲突耗尽自身重试预算而失败。
	RetriesExhausted
)

func (k FinalKind) String() string {
	switch k {
	case Committed:
		return "Committed"
	case Preempted:
		return "Preempted"
	case RetriesExhausted:
		return "RetriesExhausted"
	}
	return "Unknown"
}

// AttemptOutcome 是单次提交尝试的结果。
type AttemptOutcome int

const (
	// AttemptCommitted 本次尝试成功提交。
	AttemptCommitted AttemptOutcome = iota
	// AttemptConflict 基线版本落后，产生普通冲突（可重试）。
	AttemptConflict
	// AttemptPreempted 基线已被更高权限写入推进，立即终止。
	AttemptPreempted
)

func (o AttemptOutcome) String() string {
	switch o {
	case AttemptCommitted:
		return "AttemptCommitted"
	case AttemptConflict:
		return "AttemptConflict"
	case AttemptPreempted:
		return "AttemptPreempted"
	}
	return "Unknown"
}

// Action 描述一个待执行的动作。
type Action struct {
	ID       string
	ObjectID string
	Priority Priority
	// MaxRetries 是普通版本冲突允许的最大重试次数；
	// 被抢占终止不消耗该预算。
	MaxRetries int
	// Apply 基于读取到的属性快照计算本次修改，必须是确定性的。
	Apply func(props map[string]any) Mutation
}

// AttemptRecord 记录单次尝试的判定依据与结果，用于重放核验。
type AttemptRecord struct {
	Baseline int64
	Outcome  AttemptOutcome
}

// Result 是动作执行的完整记录。
type Result struct {
	ActionID string
	Final    FinalKind
	Attempts []AttemptRecord
	// Version 仅在 Final == Committed 时为提交后的新版本号。
	Version int64
}
