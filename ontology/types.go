// Package ontology 提供本体平台的跨实例批量更新能力：
// 每个批次携带一组"目标实例当前必须恰好处于指定版本"的联合前置条件，
// 全部满足才整体生效，否则整体拒绝且不产生任何可观察的状态变化。
package ontology

// InstanceID 实例标识。
type InstanceID string

// Version 实例版本号。每个实例拥有自己独立的、严格单调递增的版本序列。
type Version uint64

// LinkTypeID 关联（链接）类型标识。
type LinkTypeID string

// LinkRef 一条关联关系：从源实例指向 Target。
type LinkRef struct {
	Type   LinkTypeID
	Target InstanceID
}

// Item 批次中的一项变更：以 Instance 当前版本必须恰好等于 Expect 为前置条件，
// 满足后对该实例应用属性与关联变更。
type Item struct {
	Instance    InstanceID
	Expect      Version
	SetAttrs    map[string]string
	AddLinks    []LinkRef
	RemoveLinks []LinkRef
}

// Batch 一次跨实例批量更新。所有 Item 的前置版本条件构成一次联合判定。
type Batch struct {
	ID    string
	Items []Item
}

// Outcome 批次判定结果，四类互斥。
type Outcome int

const (
	// OutcomeCommitted 整体成功提交。
	OutcomeCommitted Outcome = iota
	// OutcomeDuplicatePrecondition 批次内存在对同一实例的重复前置声明。
	OutcomeDuplicatePrecondition
	// OutcomeVersionConflict 至少一项前置版本不满足，整体拒绝。
	OutcomeVersionConflict
	// OutcomeCardinalityViolation 批次生效后链接基数约束不满足，整体拒绝。
	OutcomeCardinalityViolation
)

func (o Outcome) String() string {
	switch o {
	case OutcomeCommitted:
		return "committed"
	case OutcomeDuplicatePrecondition:
		return "duplicate-precondition"
	case OutcomeVersionConflict:
		return "version-conflict"
	case OutcomeCardinalityViolation:
		return "cardinality-violation"
	}
	return "unknown"
}

// Decision 一个批次的完整判定记录：前置条件、判定依据与最终结果，
// 全部追加到判定日志以便重放核验。
type Decision struct {
	BatchID string
	Items   []Item
	Outcome Outcome
	// Detail 判定依据的人类可读描述（冲突实例、期望/实际版本、基数明细等）。
	Detail string
	// Observed 联合判定时刻实际读到的各实例版本（判定依据）。
	Observed map[InstanceID]Version
	// Reads 本次联合判定执行的版本读取次数，恒等于 len(Items)，
	// 与系统中实例总数无关——这是判定开销 O(批次大小) 的可验证证据。
	Reads int
	// Tick 判定逻辑时刻的全局序号（在持有全部相关实例锁时取号），
	// 用于把并发提交重排为等价的串行顺序；仅属判定日志元数据，
	// 不属于任何实例的状态。
	Tick uint64
	// CommitSeq 提交序号，仅 OutcomeCommitted 时非零，给出已提交批次的串行序。
	CommitSeq uint64
}
