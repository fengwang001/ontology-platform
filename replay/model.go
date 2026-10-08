// Package replay 提供灾难恢复场景下的差异记录重放校验能力。
//
// 输入为一份已知良好快照与一份结构与实例差异记录，输出为三类互不相同、
// 按固定优先级判定的结论：
//  1. 差异记录自身内部不自洽（DeltaInconsistent）
//  2. 重放所需前提环境（良好快照）不满足（PreconditionUnmet）
//  3. 重放结果与差异记录声明的目标状态不等价（NotEquivalent）
//
// 全部校验通过时返回 Valid，表示重放结果被确证为与声明目标等价。
package replay

// 属性类型取值。
const (
	PropString = "string"
	PropInt    = "int"
	PropBool   = "bool"
)

// ObjectType 描述一种对象类型的结构定义。
type ObjectType struct {
	Name       string
	Properties map[string]string // 属性名 -> 属性类型（PropString/PropInt/PropBool）
}

// Constraint 描述链接类型上的基数约束，0 表示不限制。
type Constraint struct {
	MaxOutgoingPerSource int
	MaxIncomingPerTarget int
}

// LinkType 描述一种链接类型的结构定义。
type LinkType struct {
	Name       string
	SourceType string
	TargetType string
	Constraint Constraint
}

// Schema 为对象类型结构与链接类型结构的集合。
type Schema struct {
	ObjectTypes map[string]ObjectType
	LinkTypes   map[string]LinkType
}

// ObjectInstance 为一个对象实例。
type ObjectInstance struct {
	ID         string
	Type       string
	Properties map[string]any
}

// LinkInstance 为一个链接实例。
type LinkInstance struct {
	ID     string
	Type   string
	Source string // 源对象 ID
	Target string // 目标对象 ID
}

// Snapshot 为灾难发生前的已知良好状态。校验过程对其只读。
// 必须通过 NewSnapshot 构造，以建立约束核对所需的索引。
type Snapshot struct {
	Schema  Schema
	Objects map[string]ObjectInstance
	Links   map[string]LinkInstance

	// 以下索引在 NewSnapshot 时一次性建立，不属于校验开销。
	outCount map[[2]string]int // (链接类型, 源对象) -> 链接数
	inCount  map[[2]string]int // (链接类型, 目标对象) -> 链接数
}

// NewSnapshot 构造快照并建立内部索引。
func NewSnapshot(schema Schema, objects map[string]ObjectInstance, links map[string]LinkInstance) *Snapshot {
	s := &Snapshot{
		Schema:   schema,
		Objects:  objects,
		Links:    links,
		outCount: make(map[[2]string]int),
		inCount:  make(map[[2]string]int),
	}
	for _, l := range links {
		s.outCount[[2]string{l.Type, l.Source}]++
		s.inCount[[2]string{l.Type, l.Target}]++
	}
	return s
}

// ChangeKind 为变化种类。结构层变化先于实例层变化列出。
type ChangeKind int

const (
	// 结构层变化
	AddObjectType ChangeKind = iota
	RemoveObjectType
	AddProperty
	RemoveProperty
	SetPropertyType
	AddLinkType
	RemoveLinkType
	SetLinkConstraint
	// 实例层变化
	CreateObject
	UpdateObject
	DeleteObject
	CreateLink
	DeleteLink
)

func (k ChangeKind) String() string {
	switch k {
	case AddObjectType:
		return "AddObjectType"
	case RemoveObjectType:
		return "RemoveObjectType"
	case AddProperty:
		return "AddProperty"
	case RemoveProperty:
		return "RemoveProperty"
	case SetPropertyType:
		return "SetPropertyType"
	case AddLinkType:
		return "AddLinkType"
	case RemoveLinkType:
		return "RemoveLinkType"
	case SetLinkConstraint:
		return "SetLinkConstraint"
	case CreateObject:
		return "CreateObject"
	case UpdateObject:
		return "UpdateObject"
	case DeleteObject:
		return "DeleteObject"
	case CreateLink:
		return "CreateLink"
	case DeleteLink:
		return "DeleteLink"
	}
	return "Unknown"
}

// IsSchema 报告该变化是否为结构层变化。
func (k ChangeKind) IsSchema() bool { return k <= SetLinkConstraint }

// Change 为差异记录中的一条变化。采用扁平结构，按 Kind 取用相应字段。
type Change struct {
	Kind ChangeKind

	// 结构层负载
	TypeName           string      // 涉及的对象类型或链接类型名
	Property           string      // 属性级变化涉及的属性名
	DeclaredObjectType *ObjectType // AddObjectType/RemoveObjectType 声明的类型定义
	DeclaredLinkType   *LinkType   // AddLinkType/RemoveLinkType 声明的类型定义
	BeforePropType     string      // RemoveProperty/SetPropertyType 声明的变化前属性类型
	AfterPropType      string      // AddProperty/SetPropertyType 声明的变化后属性类型
	BeforeConstraint   *Constraint // SetLinkConstraint 声明的变化前约束
	AfterConstraint    *Constraint // SetLinkConstraint 声明的变化后约束

	// 实例层负载（声明的变化前/后状态）
	ObjectBefore *ObjectInstance
	ObjectAfter  *ObjectInstance
	LinkBefore   *LinkInstance
	LinkAfter    *LinkInstance
}

// DeclaredTarget 为差异记录声明的目标状态在被触及投影上的取值。
// 键为被差异记录触及的类型名 / 对象 ID / 链接 ID，值为 nil 表示目标状态下应不存在。
// 未被差异记录触及的部分由重放语义保证与快照一致，无需声明（见设计文档）。
type DeclaredTarget struct {
	ObjectTypes map[string]*ObjectType
	LinkTypes   map[string]*LinkType
	Objects     map[string]*ObjectInstance
	Links       map[string]*LinkInstance
}

// Delta 为一份结构与实例差异记录。
type Delta struct {
	Changes []Change
	Target  DeclaredTarget
}

// Category 为判定结论类别，按固定优先级排列。
type Category int

const (
	// Valid 表示重放结果被确证为与声明目标等价。
	Valid Category = iota
	// DeltaInconsistent 表示差异记录自身内部不自洽。
	DeltaInconsistent
	// PreconditionUnmet 表示重放所需前提环境（良好快照）不满足。
	PreconditionUnmet
	// NotEquivalent 表示重放结果与差异记录声明的目标状态不等价。
	NotEquivalent
)

func (c Category) String() string {
	switch c {
	case Valid:
		return "Valid"
	case DeltaInconsistent:
		return "DeltaInconsistent"
	case PreconditionUnmet:
		return "PreconditionUnmet"
	case NotEquivalent:
		return "NotEquivalent"
	}
	return "Unknown"
}

// Location 为不等价的位置类别。
type Location int

const (
	NoLocation Location = iota
	SchemaLocation
	ObjectLocation
	LinkLocation
)

func (l Location) String() string {
	switch l {
	case SchemaLocation:
		return "schema"
	case ObjectLocation:
		return "object"
	case LinkLocation:
		return "link"
	}
	return "none"
}

// Stats 记录一次校验对快照的读取次数，用于复核
// “校验开销只与差异记录规模相关、与快照未被触及部分无关”。
type Stats struct {
	SchemaReads int
	ObjectReads int
	LinkReads   int
}

// Verdict 为一次校验的完整结论。
type Verdict struct {
	Category Category
	Location Location // 仅 NotEquivalent 时有意义
	// ChangeIndex 为命中问题时的变化下标，无关联变化时为 -1。
	ChangeIndex int
	Reason      string // 判定依据（人类可读）
	Stats       Stats
}
