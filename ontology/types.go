// Package ontology 提供对象实例存储，以及在同一实例上并存的两种并发控制：
// 普通属性更新的乐观并发控制（版本号判定）与动作（Action）触发的
// 生命周期状态机转换所使用的独占占用权。
package ontology

// Value 是属性的可序列化值。
type Value = string

// Props 是实例的属性集合。
type Props map[string]Value

// Patch 表示一次普通乐观属性更新：把每个键设置为给定值。
type Patch map[string]Value

// LinkType 描述两个实例间的链接类型及其基数约束。
type LinkType struct {
	Name      string
	SourceKey string
	TargetKey string
	// MaxOut 是 SourceKey 实例可通过该链接类型持有的目标实例数上限；0 表示不限。
	MaxOut int
}

// Instance 是一次只读快照。
type Instance struct {
	Key        string
	Version    int64
	State      string
	Props      Props
	Links      map[string][]string
	Occupied   bool
	Holder     string
	LeaseUntil int64
}

// Outcome 是每一次占用申请或更新尝试的互斥结果分类。
type Outcome int

const (
	// OutcomeCommitted 表示尝试成功提交。
	OutcomeCommitted Outcome = iota
	// OutcomeOccupied 表示占用申请被拒绝：目标实例已被另一动作占用。
	OutcomeOccupied
	// OutcomeLockConflict 表示跨实例占用申请被确定性规则拒绝。
	OutcomeLockConflict
	// OutcomeOptimisticRejected 表示占用期间的普通乐观更新被拒绝。
	OutcomeOptimisticRejected
	// OutcomeVersionStale 表示乐观更新因版本落后被拒绝。
	OutcomeVersionStale
	// OutcomeCardinality 表示链接基数冲突。
	OutcomeCardinality
	// OutcomeInvalidLease 表示占用权已失效（过期/中止/令牌不符）。
	OutcomeInvalidLease
)
