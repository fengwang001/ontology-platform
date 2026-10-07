// Package ontology 实现带审计溯源的属性索引重建与独立复核。
package ontology

// Value 是可比较的属性值（按值查找要求其支持相等比较）。
type Value string

type TypeID string
type ObjectID string
type AttrName string

// OpKind 区分属性写入与属性删除（墓碑）。
type OpKind uint8

const (
	OpPut    OpKind = 1
	OpDelete OpKind = 2
)

// Write 是对象存储上一条已生效的写入记录，Seq 为全局单调序号。
type Write struct {
	Seq    int64
	Type   TypeID
	Object ObjectID
	Attr   AttrName
	Value  Value
	Op     OpKind
}

// AttrCell 是某对象某属性的当前单元：当前值、墓碑标记，
// 以及最后一次修改该属性的写入序号（复核定位用的版本指针）。
type AttrCell struct {
	Value   Value
	Deleted bool
	Version int64
}

// ErrorKind 覆盖可区分的四类拒绝/判定结果。
type ErrorKind int

const (
	// KindOK 无错误。
	KindOK ErrorKind = 0
	// KindIndexNotDeclared 对象类型未声明该索引（“索引不存在”）。
	KindIndexNotDeclared ErrorKind = 1
	// KindRebuildingUnavailable 索引正在重建或上次重建失败，尚无完整审计记录，不可用。
	KindRebuildingUnavailable ErrorKind = 2
	// KindInconsistencyDeclared 索引可用，但复核曾发现不一致并已标记，查询仍返回但带声明。
	KindInconsistencyDeclared ErrorKind = 3
	// KindEntryMismatch 复核过程发现的条目级不一致。
	KindEntryMismatch ErrorKind = 4
	// KindRejectedRebuild 重建请求被拒绝（如重复发起），不改变任何状态。
	KindRejectedRebuild ErrorKind = 5
)

// Error 携带可区分的错误类别与具体说明。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }
