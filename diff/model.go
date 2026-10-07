package diff

// TypeKind 描述属性取值的逻辑类型族。
type TypeKind int

const (
	// KindUnknown 不是合法类型。
	KindUnknown TypeKind = iota
	KindBool
	KindInt
	KindDecimal
	KindStr
	KindEnum
	KindOpaque
)

// ValueType 描述属性声明的取值类型。
//
// Kind 为逻辑类型族；Param 为类型参数，语义随 Kind 不同而不同：
//   - KindEnum: Param 为允许取值集合（去重、按字典序规范化）。
//   - 其他 Kind: Param 必须为 nil。
//
// Opaque 表示平台无法解释的取值类型，其取值只存在原始字节，
// 无法保证跨快照的比较口径，仅可用于结构层声明。
type ValueType struct {
	Kind  TypeKind
	Param []string
}

// TypedValue 是一个带类型标注的属性取值。
type TypedValue struct {
	Kind  TypeKind
	Int   int64
	Str   string
	Bool  bool
	Bytes []byte
}

// PropertyDecl 是对象类型上的属性声明。
//
// UID 是属性在整个生命周期内的稳定标识：重命名只改 Name 不改 UID，
// 因此结构层比对以 UID 判定"同一属性"，以 Name 判定标识变化。
type PropertyDecl struct {
	UID      string
	Name     string
	Type     ValueType
	Required bool
}

// ObjectType 是对象类型的结构声明。
type ObjectType struct {
	UID        string
	Name       string
	Deprecated bool
	Properties []PropertyDecl
}

// Object 是一个对象实例。
type Object struct {
	ID      string
	TypeUID string
	Values  map[string]TypedValue // key 为属性 UID
}

// Link 是两个对象实例之间的一条链接实例。
type Link struct {
	ID       string
	TypeUID  string
	SourceID string
	TargetID string
}

// RemovalRecord 是显式删除的墓碑记录。
//
// 快照是两个时点的状态记录：普通"只存在于旧快照"无法区分
// "被显式删除"与"由于所依赖的端点/类型消失而连带失效"。
// 因此显式删除必须由采集层以墓碑形式单独声明。
type RemovalRecord struct {
	ID      string
	TypeUID string
	Kind    RemovalKind
}

// RemovalKind 区分对象与链接的墓碑。
type RemovalKind int

const (
	RemovalUnknown RemovalKind = iota
	RemovalObject
	RemovalLink
)

// Snapshot 是本体平台在某一时点产生的不可变快照。
//
// Snapshot 由 Builder 构造；比对组件从不修改其中任何内容，
// 所需索引在构造期一次性计算完成，比对过程零写入。
type Snapshot struct {
	types    []ObjectType
	objects  []Object
	links    []Link
	removals []RemovalRecord

	index *snapshotIndex
}

func (s *Snapshot) Types() []ObjectType       { return s.types }
func (s *Snapshot) Objects() []Object         { return s.objects }
func (s *Snapshot) Links() []Link             { return s.links }
func (s *Snapshot) Removals() []RemovalRecord { return s.removals }

// DiffKind 标识差异大类。
type DiffKind int

const (
	DiffUnknown DiffKind = iota
	// 结构层
	DiffTypeAdded
	DiffTypeRemoved
	DiffTypeDeprecated
	DiffTypeRenamed
	DiffPropertyAdded
	DiffPropertyRemoved
	DiffPropertyRenamed
	DiffPropertyTypeChanged
	// 实例层 - 对象
	DiffObjectCreated
	DiffObjectDeleted
	DiffObjectValueChanged
	// 实例层 - 链接
	DiffLinkCreated
	DiffLinkDeleted
	DiffLinkEndpointDeleted
)

// DeleteReason 是删除类差异的归因。
type DeleteReason int

const (
	DeleteReasonUnspecified DeleteReason = iota
	// DeleteReasonExplicit 实例被显式删除（快照中存在墓碑）。
	DeleteReasonExplicit
	// DeleteReasonTypeDeprecated 对象类型被整体废弃。
	DeleteReasonTypeDeprecated
	// DeleteReasonEndpointDeleted 链接所链接的端点对象被删除。
	DeleteReasonEndpointDeleted
)

// TypeDiff 是对象类型层面的一条结构差异。
type TypeDiff struct {
	Kind    DiffKind
	TypeUID string
	OldName string
	NewName string
}

// PropertyDiff 是属性层面的一条结构差异。
//
// 同一条属性可以同时携带多个相互正交的维度：
// 例如先重命名、后收紧类型时，RenamedFrom 与 OldType/NewType 同时出现，
// 它们是两个独立可分辨的维度，绝不合并成一种笼统变化。
type PropertyDiff struct {
	TypeUID     string
	PropertyUID string
	OldName     string
	NewName     string
	OldType     *ValueType
	NewType     *ValueType
	Renamed     bool
	TypeChanged bool
}

// StructuralDiff 是结构层差异结果。
type StructuralDiff struct {
	Types      []TypeDiff
	Properties []PropertyDiff
}

// PropertyValueChange 是对象上单个属性的取值变化。
type PropertyValueChange struct {
	PropertyUID  string
	PropertyName string
	OldName      string // 重命名前的名字（未重命名时为空）
	// Incompatible 为 true 表示旧取值按新类型解读不再有效，
	// 属于"类型不兼容"类别，与 ValueChanged 互斥。
	Incompatible bool
	OldValue     *TypedValue
	NewValue     *TypedValue
}

// ObjectDiff 是对象实例层的一条差异。
type ObjectDiff struct {
	Kind     DiffKind
	ObjectID string
	TypeUID  string
	Reason   DeleteReason
	Changes  []PropertyValueChange // 仅 ValueChanged 时使用，且只含真正变化的属性
}

// LinkDiff 是链接实例层的一条差异。
type LinkDiff struct {
	Kind    DiffKind
	LinkID  string
	TypeUID string
	Reason  DeleteReason
}

// InstanceDiff 是实例层差异结果。
type InstanceDiff struct {
	Objects []ObjectDiff
	Links   []LinkDiff
}

// Diff 是一次完整比对的结果：先结构层，后实例层。
type Diff struct {
	Structural StructuralDiff
	Instance   InstanceDiff
}
