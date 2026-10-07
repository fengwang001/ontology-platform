// Package link 实现本体平台中两个对象类型之间链接类型的实例层
// 创建、删除与去重仲裁，覆盖双方向基数约束与并发确定性。
package link

// ObjectTypeID 标识对象类型。
type ObjectTypeID string

// ObjectInstanceID 标识对象实例。
type ObjectInstanceID string

// LinkTypeID 标识链接类型。
type LinkTypeID string

// Direction 表示链接的有序方向。
type Direction int

const (
	// DirectionForward 为链接类型声明的源类型 -> 目标类型方向。
	DirectionForward Direction = iota + 1
	// DirectionBackward 为目标类型 -> 源类型方向。
	DirectionBackward
)

func (d Direction) String() string {
	switch d {
	case DirectionForward:
		return "forward"
	case DirectionBackward:
		return "backward"
	default:
		return "unknown"
	}
}

// Cardinality 是某一方向的基数上限。零表示不允许任何链接；
// Unlimited 表示不设上限。
type Cardinality struct {
	Limit     int
	Unlimited bool
}

// Unlimited 构造一个不限数量的基数上限。
func Unlimited() Cardinality { return Cardinality{Unlimited: true} }

// Limited 构造一个值为 n 的基数上限（n 必须非负）。
func Limited(n int) Cardinality { return Cardinality{Limit: n} }

// LinkType 是一个链接类型的声明。源、目标对象类型可以相同。
// 两个方向的基数上限相互独立，可以不同，也可以任意一侧不限。
type LinkType struct {
	ID         LinkTypeID
	SourceType ObjectTypeID
	TargetType ObjectTypeID
	// ForwardCap 为 SourceType -> TargetType 方向的上限。
	ForwardCap Cardinality
	// BackwardCap 为 TargetType -> SourceType 方向的上限。
	BackwardCap Cardinality
}

// Link 是一条已登记的链接实例。
type Link struct {
	// ID 是链接实例的唯一标识；重新占用被撤销的区分属性组合时分配新 ID。
	ID            uint64
	TypeID        LinkTypeID
	SourceID      ObjectInstanceID
	TargetID      ObjectInstanceID
	Discriminator map[string]string
}

// CreateRequest 是一次创建链接的请求。
type CreateRequest struct {
	TypeID        LinkTypeID
	SourceID      ObjectInstanceID
	TargetID      ObjectInstanceID
	Discriminator map[string]string
}

// DecisionResult 记录一次仲裁的结果类别。
type DecisionResult string

const (
	ResultCreated   DecisionResult = "created"
	ResultDuplicate DecisionResult = "duplicate"
	ResultDeleted   DecisionResult = "deleted"
	ResultNotFound  DecisionResult = "not_found"
	ResultRejected  DecisionResult = "rejected"
)
