// Package linkrepair 修复快照中局部损坏的链接实例记录。
//
// 修复过程分为三个职责清晰、顺序固定的阶段：
//
//  1. 结构有效性判定（validate.go）
//  2. 引用可用性核对（references.go）
//  3. 基数约束裁决与去重（cardinality.go）
//
// 四个舍弃判定（[Reason]）互斥且按上述阶段顺序固定裁决。
package linkrepair

// Cardinality 描述链接类型某一端的基数约束。
//
// Max 表示该端的单个对象实例最多可以关联多少个对方实例：
//   - Max == 1：单一基数（to-one），最多关联一个对方实例；
//   - Max > 1：多值基数但存在声明的数量上限；
//   - Max <= 0：多值基数且无数量上限。
type Cardinality struct {
	Max int
}

// LinkType 描述一种链接类型及其两端的基数约束。
type LinkType struct {
	// ID 为链接类型标识。
	ID string

	// EndAName/EndBName 仅用于报告与调试，不参与裁决。
	EndAName string
	EndBName string

	// MaxA/MaxB 分别是 A 端、B 端单个实例可关联对方实例的数量上限，
	// 语义见 [Cardinality.Max]。
	MaxA int
	MaxB int
}

// RawRecord 是快照中恢复出的一条链接实例记录，可能已损坏。
//
// SourceID 为 A 端对象实例标识，TargetID 为 B 端对象实例标识；
// 任一字段为空字符串表示该端信息丢失或不可解析。
type RawRecord struct {
	// ID 为链接实例自身的标识，可能因损坏为空。
	ID string

	// LinkTypeID 可能为空，或指向未知链接类型。
	LinkTypeID string

	SourceID string
	TargetID string
}

// Snapshot 是一次修复裁决的只读输入。
type Snapshot struct {
	// LinkTypes 为本次恢复已知的链接类型定义。
	LinkTypes []LinkType

	// AvailableObjects 为同一次恢复中被判定为可用的对象实例标识集合。
	// 不在该集合中的实例一律视为不可用（丢失或不可解析）。
	AvailableObjects map[string]struct{}

	// Records 为快照中残留的全部链接记录，顺序即快照中的出现位置，
	// 该位置用于在记录自身标识丢失时给出稳定定位。
	Records []RawRecord
}

// Reason 是记录被舍弃的互斥判定原因。
type Reason int

const (
	// ReasonNone 表示记录被保留。
	ReasonNone Reason = iota

	// ReasonMalformed：链接记录自身结构损坏无法解析
	// （链接类型缺失/未知，或任一端对象信息丢失）。
	ReasonMalformed

	// ReasonReferencedUnavailable：记录结构完整，
	// 但至少一端引用的对象实例在同一次恢复中不可用。
	ReasonReferencedUnavailable

	// ReasonCardinalityConflict：记录通过前两项检查，
	// 但在单一基数或带上限多值基数的裁决中被舍弃。
	ReasonCardinalityConflict

	// ReasonDuplicate：记录与另一条最终保留的记录内容完全相同
	// （同一链接类型、同一对对象实例），属于重复副本。
	ReasonDuplicate
)

// String 返回判定原因的稳定名称。
func (r Reason) String() string {
	switch r {
	case ReasonNone:
		return "none"
	case ReasonMalformed:
		return "malformed"
	case ReasonReferencedUnavailable:
		return "referenced_object_unavailable"
	case ReasonCardinalityConflict:
		return "cardinality_conflict"
	case ReasonDuplicate:
		return "duplicate"
	default:
		return "unknown"
	}
}

// Verdict 是对单条原始记录的最终裁决。
type Verdict struct {
	// Position 为该记录在输入 Records 中的下标，稳定且永不重复。
	Position int

	// RecordID 为记录自身标识，可能因损坏为空。
	RecordID string

	// Kept 为 true 表示保留，false 表示舍弃。
	Kept bool

	// Reason 为舍弃原因；保留时为 [ReasonNone]。
	Reason Reason

	// Detail 为人类可读、确定性的裁决依据说明。
	Detail string
}

// Report 是一次修复裁决的完整结果。
type Report struct {
	// Kept 为最终保留的记录，按内容键确定性排序，
	// 对同一份快照重复执行结果完全一致。
	Kept []RawRecord

	// Verdicts 与输入记录一一对应（按下标对齐），
	// 既包含保留裁决也包含舍弃裁决。
	Verdicts []Verdict
}
