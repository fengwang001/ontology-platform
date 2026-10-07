package linkrepair

// ObjectID 是对象实例在一次恢复过程中的稳定标识。
type ObjectID string

// LinkTypeID 是链接类型的标识。
type LinkTypeID string

// Unbounded 表示该端不设数量上限。
const Unbounded = -1

// Cardinality 描述一种链接类型在两个方向上的基数约束。
//
// MaxFrom：单个 From 实例最多关联的不同 To 实例数量（To 端基数）。
// MaxTo：单个 To 实例最多关联的不同 From 实例数量（From 端基数）。
// 值为 Unbounded 表示不设上限；任何小于等于 0 的其它取值均非法。
type Cardinality struct {
	MaxFrom int
	MaxTo   int
}

// SingletonCardinality 是“一端最多一个对方实例”的常见便捷构造。
func SingletonCardinality() Cardinality { return Cardinality{MaxFrom: 1, MaxTo: 1} }

// LinkType 是一种链接类型的声明。
type LinkType struct {
	ID          LinkTypeID
	Cardinality Cardinality
}

// RawRecord 是快照中恢复出的一条原始链接记录。
//
// Offset 是该记录在原始损坏快照中的位置序号，作为所有裁决规则
// 最终、唯一的平局打破依据（记录位置不会随重复执行而改变）。
// Type/From/To 为空字符串分别表示链接类型或某一端对象信息丢失、
// 不可解析；不允许用默认值猜测补全。
type RawRecord struct {
	Offset int
	Type   LinkTypeID
	From   ObjectID
	To     ObjectID
}

// Link 是一条通过结构与引用核对的完整链接。
type Link struct {
	Type LinkTypeID
	From ObjectID
	To   ObjectID
}

// ScoredLink 携带其来源记录位置，供基数裁决做确定性排序。
type ScoredLink struct {
	Link
	Offset int
}

// Snapshot 是一次修复裁决的完整输入。
//
// AvailableObjects 给出同一次恢复过程中已判定为可用的对象实例集合；
// 不在集合内的对象一律视为不可用，引用它的完好记录也必须舍弃。
// 裁决过程只读该快照，绝不修改任何输入。
type Snapshot struct {
	LinkTypes        map[LinkTypeID]LinkType
	AvailableObjects map[ObjectID]bool
	Records          []RawRecord
}

// Reason 是一条记录被舍弃的四类互斥原因。
type Reason string

const (
	// ReasonMalformed：记录自身结构损坏，无法解析为完整链接。
	ReasonMalformed Reason = "malformed_structure"
	// ReasonRefUnavailable：记录完整，但其引用的对象实例在本次恢复中不可用。
	ReasonRefUnavailable Reason = "reference_unavailable"
	// ReasonCardinality：在单一（或带上限的）基数约束下竞争失败被舍弃。
	ReasonCardinality Reason = "cardinality_conflict"
	// ReasonDuplicate：与另一条保留记录指向完全相同的一对对象。
	ReasonDuplicate Reason = "duplicate_record"
)

// DroppedRecord 记录一条被舍弃记录及其可复核的裁决依据。
type DroppedRecord struct {
	Record RawRecord
	Reason Reason
	// Detail 人类可读说明；GroupKey 标识发生重复/基数竞争的分组。
	Detail   string
	GroupKey string
	// KeptOffsets 是该组最终保留记录的来源 Offset，便于复核。
	KeptOffsets []int
}

// Stats 是一次裁决的计数与工作量度量。
type Stats struct {
	Input          int
	Kept           int
	Malformed      int
	RefUnavailable int
	Cardinality    int
	Duplicate      int
	// ConflictGroups 是真正发生过重复或基数竞争的分组数量。
	ConflictGroups int
	// ConflictGroupWork 是这些冲突分组内候选记录总数。
	// 无冲突的分组只做 O(1) 哈希登记，不计入此度量，
	// 因此总工作量只与“真正发生冲突的局部范围”相关。
	ConflictGroupWork int
}

// Result 是修复裁决的最终结果。Kept 按 (Type,From,To) 确定性排序。
type Result struct {
	Kept    []Link
	Dropped []DroppedRecord
	Stats   Stats
}
