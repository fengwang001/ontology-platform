package ontology

import "errors"

// ObjectID 唯一标识一个对象实例。
type ObjectID string

// RecordID 唯一标识一条属性历史记录（全局唯一）。
type RecordID string

// Value 是属性历史记录承载的取值，演示系统使用 string。
type Value = string

// InvisibleReason 是「当前可见取值」查询三层条件未通过时的互斥归类。
type InvisibleReason string

const (
	// ReasonObjectDeleted 第一层未通过：对象处于整体逻辑删除状态。
	ReasonObjectDeleted InvisibleReason = "OBJECT_DELETED"
	// ReasonRecordDeleted 第二层未通过：该条历史记录被单独逻辑删除。
	ReasonRecordDeleted InvisibleReason = "RECORD_DELETED"
	// ReasonNotCurrent 第三层未通过：该时间点上不存在生效的历史记录。
	ReasonNotCurrent InvisibleReason = "NOT_CURRENT"
)

// 四类互斥错误，判定次序固定如下（只报第一类）。
var (
	// ErrObjectNotFound 对象实例不存在。
	ErrObjectNotFound = errors.New("ontology: object not found")
	// ErrRecordNotFound 目标属性历史记录不存在。
	ErrRecordNotFound = errors.New("ontology: history record not found")
	// ErrObjectDeleted 对象处于整体删除状态，拒绝属性历史的单独删除/撤销。
	ErrObjectDeleted = errors.New("ontology: object is deleted")
	// ErrRecordAlreadyDeleted 对已被单独删除的记录重复发起单独删除。
	ErrRecordAlreadyDeleted = errors.New("ontology: history record already deleted")
)

var (
	// ErrObjectExists 重复创建同一对象。
	ErrObjectExists = errors.New("ontology: object already exists")
	// ErrNotMonotonic 同一属性的 ValidFrom 未严格递增。
	ErrNotMonotonic = errors.New("ontology: validFrom must be strictly increasing per property")
)

// HistoryRecord 是一条属性历史记录。
type HistoryRecord struct {
	ID        RecordID
	ObjectID  ObjectID
	Property  string
	Value     Value
	ValidFrom int64 // 逻辑时间戳，单调语义由调用方保证
	Deleted   bool  // 单独逻辑删除标记（独立于对象整体删除标记存储）
}

// QueryResult 是「当前可见取值」查询的结果。
type QueryResult struct {
	Visible bool
	Value   Value
	Reason  InvisibleReason // Visible 为 false 时有效
	Record  RecordID        // 被判定为最新有效记录的 ID（若存在）
	Err     error           // 对象实例不存在时为 ErrObjectNotFound
}
