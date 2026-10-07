// Package replay 提供灾难恢复场景下的差异记录重放校验能力。
//
// 输入为一份良好快照（Snapshot）与一份差异记录（DeltaLog），
// 输出为三类互不相同、按固定优先级判定的结论之一：
//  1. 差异记录自身内部不自洽（VerdictInconsistentDelta）
//  2. 重放所需前提环境不满足（VerdictPreconditionFailed）
//  3. 重放结果与差异记录声明的目标状态不等价（VerdictNotEquivalent）
//
// 全部通过时返回等价（VerdictEquivalent）。
package replay

// PropertyValue 是属性取值，本组件将其视为不透明的可比较值。
type PropertyValue = any

// ConstraintUniqueSource 是链接类型上内建支持的约束名：
// 值为 true 时，同一链接类型下每个源对象至多出现一次。
const ConstraintUniqueSource = "uniqueSource"

// LinkKey 唯一标识一条链接实例。
type LinkKey struct {
	LinkTypeID string
	SourceID   string
	TargetID   string
}

// LinkSourceKey 是 (链接类型, 源对象) 索引键。
type LinkSourceKey struct {
	LinkTypeID string
	SourceID   string
}

// TypePropertyKey 是 (对象类型, 属性) 索引键。
type TypePropertyKey struct {
	TypeID   string
	Property string
}

// LinkInstance 是一条链接实例。
type LinkInstance struct {
	LinkTypeID string
	SourceID   string
	TargetID   string
}

// Key 返回该链接实例的键。
func (l LinkInstance) Key() LinkKey {
	return LinkKey{LinkTypeID: l.LinkTypeID, SourceID: l.SourceID, TargetID: l.TargetID}
}

// ObjectInstance 是一个对象实例。
type ObjectInstance struct {
	ObjectID   string
	ObjectType string
	Properties map[string]PropertyValue
}

// ObjectTypeDef 是对象类型定义。
type ObjectTypeDef struct {
	TypeID     string
	Properties map[string]struct{} // 属性名集合
}

// LinkTypeDef 是链接类型定义。
type LinkTypeDef struct {
	TypeID      string
	SourceType  string
	TargetType  string
	Constraints map[string]PropertyValue // 结构层约束，如 {"uniqueSource": true}
}

// Schema 是结构层状态：对象类型与链接类型。
type Schema struct {
	ObjectTypes map[string]ObjectTypeDef
	LinkTypes   map[string]LinkTypeDef
}

// Snapshot 是灾难发生前的已知良好快照。
type Snapshot struct {
	Schema  Schema
	Objects map[string]ObjectInstance
	Links   map[LinkKey]LinkInstance
	// Indexes 是快照携带的引用完整性与约束索引（如同数据库检查点
	// 会携带其索引一样，属于快照的一部分）。校验组件只读使用这些
	// 索引，并在覆盖层中维护增量，从而保证校验开销与快照中未被
	// 触及的对象/链接总数无关。索引缺失属于前提环境问题。
	Indexes SnapshotIndexes
}

// SnapshotIndexes 是快照携带的索引集合，全部由快照内容派生。
type SnapshotIndexes struct {
	// ObjectTypeCounts: 对象类型 ID -> 该类型对象数。
	ObjectTypeCounts map[string]int
	// LinkTypeCounts: 链接类型 ID -> 该类型链接数。
	LinkTypeCounts map[string]int
	// LinkEndpointCounts: 对象 ID -> 以该对象为源或目标的链接数。
	LinkEndpointCounts map[string]int
	// LinkSourceCounts: (链接类型, 源对象) -> 链接数。
	LinkSourceCounts map[LinkSourceKey]int
	// PropertyUsageCounts: (对象类型, 属性) -> 设置了该属性的对象数。
	PropertyUsageCounts map[TypePropertyKey]int
}

// State 是重放过程中使用的工作状态（快照的可变副本）。
type State struct {
	Schema  Schema
	Objects map[string]ObjectInstance
	Links   map[LinkKey]LinkInstance
}

// ChangeKind 枚举差异记录中一条变化的种类。
type ChangeKind int

const (
	ChangeAddObjectType ChangeKind = iota
	ChangeRemoveObjectType
	ChangeAddProperty
	ChangeRemoveProperty
	ChangeAddLinkType
	ChangeRemoveLinkType
	ChangeAddLinkTypeConstraint
	ChangeRemoveLinkTypeConstraint
	ChangeAddObject
	ChangeUpdateObject
	ChangeRemoveObject
	ChangeAddLink
	ChangeRemoveLink
)

// String 返回变化种类的可读名称。
func (k ChangeKind) String() string {
	switch k {
	case ChangeAddObjectType:
		return "AddObjectType"
	case ChangeRemoveObjectType:
		return "RemoveObjectType"
	case ChangeAddProperty:
		return "AddProperty"
	case ChangeRemoveProperty:
		return "RemoveProperty"
	case ChangeAddLinkType:
		return "AddLinkType"
	case ChangeRemoveLinkType:
		return "RemoveLinkType"
	case ChangeAddLinkTypeConstraint:
		return "AddLinkTypeConstraint"
	case ChangeRemoveLinkTypeConstraint:
		return "RemoveLinkTypeConstraint"
	case ChangeAddObject:
		return "AddObject"
	case ChangeUpdateObject:
		return "UpdateObject"
	case ChangeRemoveObject:
		return "RemoveObject"
	case ChangeAddLink:
		return "AddLink"
	case ChangeRemoveLink:
		return "RemoveLink"
	default:
		return "Unknown"
	}
}

// IsSchemaChange 报告该变化是否属于结构层。
func (k ChangeKind) IsSchemaChange() bool {
	return k <= ChangeRemoveLinkTypeConstraint
}

// Change 是差异记录中的一条变化。
//
// 不同种类使用不同字段子集：
//   - AddObjectType/RemoveObjectType: TypeID, ObjectTypeAfter(仅 Add)
//   - AddProperty/RemoveProperty: TypeID, Property
//   - AddLinkType/RemoveLinkType: LinkTypeID, LinkTypeAfter(仅 Add)
//   - AddLinkTypeConstraint/RemoveLinkTypeConstraint: LinkTypeID, Constraint, ConstraintValue(仅 Add)
//   - AddObject: ObjectAfter
//   - UpdateObject: ObjectBefore, ObjectAfter（ObjectID 一致）
//   - RemoveObject: ObjectBefore
//   - AddLink: LinkAfter
//   - RemoveLink: LinkBefore
type Change struct {
	Kind ChangeKind

	TypeID   string // 对象类型相关变化的目标类型
	Property string // 属性名

	LinkTypeID      string        // 链接类型相关变化的目标类型
	Constraint      string        // 约束名
	ConstraintValue PropertyValue // 约束值（Add 时使用）

	ObjectTypeAfter ObjectTypeDef
	LinkTypeAfter   LinkTypeDef

	ObjectBefore ObjectInstance
	ObjectAfter  ObjectInstance

	LinkBefore LinkInstance
	LinkAfter  LinkInstance
}

// DeltaLog 是差异记录：从良好快照到目标状态之间的变化序列，
// 以及声明重放后应达到的目标状态。
type DeltaLog struct {
	Changes       []Change
	TargetSchema  Schema
	TargetObjects map[string]ObjectInstance
	TargetLinks   map[LinkKey]LinkInstance
}

// Verdict 是校验结论。
type Verdict int

const (
	// VerdictEquivalent 表示差异记录自洽、前提满足、重放结果与声明目标等价。
	VerdictEquivalent Verdict = iota
	// VerdictInconsistentDelta 表示差异记录自身内部不自洽。
	VerdictInconsistentDelta
	// VerdictPreconditionFailed 表示重放所需前提环境（良好快照）不满足。
	VerdictPreconditionFailed
	// VerdictNotEquivalent 表示重放结果与差异记录声明的目标状态不等价。
	VerdictNotEquivalent
)

// String 返回结论的可读名称。
func (v Verdict) String() string {
	switch v {
	case VerdictEquivalent:
		return "Equivalent"
	case VerdictInconsistentDelta:
		return "InconsistentDelta"
	case VerdictPreconditionFailed:
		return "PreconditionFailed"
	case VerdictNotEquivalent:
		return "NotEquivalent"
	default:
		return "Unknown"
	}
}

// MismatchCategory 是不一致位置的类别。
type MismatchCategory int

const (
	// CategorySchema 结构层（对象类型/链接类型定义）不一致。
	CategorySchema MismatchCategory = iota
	// CategoryObject 对象实例不一致。
	CategoryObject
	// CategoryLink 链接实例不一致。
	CategoryLink
)

// String 返回类别的可读名称。
func (c MismatchCategory) String() string {
	switch c {
	case CategorySchema:
		return "schema"
	case CategoryObject:
		return "object"
	case CategoryLink:
		return "link"
	default:
		return "unknown"
	}
}

// Mismatch 描述一处不一致的具体位置。
type Mismatch struct {
	Category MismatchCategory
	Key      string // 类型 ID、对象 ID 或链接键的可读表示
	Detail   string
}

// Result 是一次校验的完整输出。
type Result struct {
	Verdict Verdict
	// ChangeIndex 在 VerdictInconsistentDelta / VerdictPreconditionFailed 时
	// 指向触发判定的差异记录下标；其他情况为 -1。
	ChangeIndex int
	// Reason 是判定依据的人类可读描述。
	Reason string
	// Mismatches 在 VerdictNotEquivalent 时列出全部不一致位置。
	Mismatches []Mismatch
}
