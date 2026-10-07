package ontology

// Privilege 是动作在本体平台上被赋予的权限等级。
// 数值越大权限越高；抢占只能发生在权限严格不同的两方之间。
type Privilege int

// Attrs 是对象实例上的属性集合。
type Attrs map[string]any

// Mutator 是动作在一次尝试中对当前属性状态执行的纯函数变换。
// 变换基于该次尝试读取到的基线，每次重试都会被重新调用。
type Mutator func(Attrs) Attrs

// Op 描述一个针对某个实例的乐观更新动作。
type Op struct {
	// ID 是动作在本次运行中的唯一标识，用于日志与重放。
	ID int
	// Instance 是目标实例名。
	Instance string
	// Priv 是动作自身的权限等级。
	Priv Privilege
	// MaxAttempts 是动作自身的重试预算：总共允许的尝试次数（含首次）。
	MaxAttempts int
	// Apply 是该动作的属性变换。
	Apply Mutator
}

// Status 是动作终结时可被单独识别的四类互斥结果之一。
type Status int

const (
	// StatusCommitted 表示成功提交。
	StatusCommitted Status = iota
	// StatusPreempted 表示因更高权限写入已生效而被立即抢占终止。
	// 该终止不消耗重试预算。
	StatusPreempted
	// StatusExhausted 表示因自身重试预算耗尽而最终失败。
	StatusExhausted
)

func (s Status) String() string {
	switch s {
	case StatusCommitted:
		return "committed"
	case StatusPreempted:
		return "preempted"
	case StatusExhausted:
		return "exhausted"
	default:
		return "unknown"
	}
}

// verdict 是单次提交尝试的判定结果。
type verdict int

const (
	verdictCommitted verdict = iota
	verdictPreempted
	verdictConflict
)

// AttemptRecord 完整记录一次尝试的判定依据，供重放核验。
type AttemptRecord struct {
	OpID          int
	Attempt       int
	Priv          Privilege
	BaseVersion   int64
	BaseHighWater Privilege
	SawVersion    int64
	SawHighWater  Privilege
	Verdict       string
	CommittedVer  int64
}

// OpResult 是一个动作的最终结果与完整轨迹。
type OpResult struct {
	OpID        int
	Instance    string
	Priv        Privilege
	Status      Status
	CommittedAt int64
	Attempts    []AttemptRecord
}
