package ontology

// model.go 仅包含领域类型定义。实现见同包其他文件。

// ObjectTypeID / LinkTypeID / InstanceID 标识本体对象类型、链接类型与实例。
type (
	ObjectTypeID string
	LinkTypeID   string
	InstanceID   string
	SubjectID    string
)

// Operation 是动作对实例可声明的直接操作。
type Operation string

const (
	OpRead   Operation = "read"
	OpUpdate Operation = "update"
	OpDelete Operation = "delete"
	OpCreate Operation = "create"
)

// Instance 是某对象类型的一个实例。
type Instance struct {
	ID      InstanceID
	Type    ObjectTypeID
	Version int
	Attrs   map[string]string
}

// Edge 是两个实例之间一条有方向的链接关系。
type Edge struct {
	LinkType LinkTypeID
	From     InstanceID
	To       InstanceID
}

// DirectOp 是动作直接声明的目标操作。
type DirectOp struct {
	Op       Operation
	Type     ObjectTypeID
	Target   InstanceID
	NewAttrs map[string]string // OpCreate 时生效
}

// CascadeRule 描述沿某类链接的级联规则。
type CascadeRule struct {
	LinkType    LinkTypeID
	Outgoing    bool      // true: 沿 from->to；false: 沿 to->from
	Effect      Operation // 传播触及后施加的操作
	NoPropagate bool      // true: 命中即停止，不继续向外扩展
}

// OnInvisible 是不可见级联实例的互斥处理模式。
type OnInvisible int

const (
	RejectOnInvisible OnInvisible = iota
	SkipOnInvisible
)

// MergePolicy 是多层级授权结论的合并规则。
type MergePolicy int

const (
	// MergeAll：整条传播路径逐层全部放行方可通过。
	MergeAll MergePolicy = iota
	// MergeAny：路径上任一层级放行即可通过。
	MergeAny
)

// ActionDeclaration 是一次动作的声明。
type ActionDeclaration struct {
	Name          string
	Subject       SubjectID
	Direct        []DirectOp
	Cascades      []CascadeRule
	MaxDepth      int
	InvisibleMode OnInvisible
	Merge         MergePolicy
}

// LayerDecision 记录一个层级的授权结论。
type LayerDecision struct {
	Depth   int
	Allowed bool
	Abstain bool
}

// TraversalEntry 记录一个被级联触及的实例及其裁决依据。
type TraversalEntry struct {
	Instance  InstanceID
	Type      ObjectTypeID
	Depth     int
	LinkType  LinkTypeID // 经由的链接类型；深度 0 的直接目标为空
	Verdict   string     // permit / deny / invisible / blocked-edge
	Decisions []LayerDecision
}

// SkippedInstance 记录因不可见而被跳过的实例；不含任何属性信息。
type SkippedInstance struct {
	Instance InstanceID
	Depth    int
	// WithheldEffects 是未能施加的级联操作（仅操作类型，无属性值）。
	WithheldEffects []Operation
	PrunedSubtree   int // 因此被剪除的可达实例数量（含自身）
}

// CascadeEffect 是实际生效的一条级联影响。
type CascadeEffect struct {
	Target InstanceID
	Type   ObjectTypeID
	Op     Operation
	Depth  int
	Attrs  map[string]string
}

// AuditRecord 是一次已提交动作的审计记录。
type AuditRecord struct {
	Seq     int64
	Action  string
	Subject SubjectID
	Direct  []DirectOp
	Applied []CascadeEffect
	Skipped []SkippedInstance
	Checks  int
}

// Report 是一次动作执行的可观测结果。
type Report struct {
	Action     string
	Committed  bool
	Reason     string
	Checks     int
	Path       []TraversalEntry
	Skipped    []SkippedInstance
	AuditIndex int64
}

// WorldState 是可外部观测的完整状态快照。
type WorldState struct {
	Instances map[InstanceID]Instance
	Edges     []Edge
	Clock     int64
	Audit     []AuditRecord
}
