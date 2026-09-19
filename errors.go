package ontology

import "fmt"

// ViolationKind 区分基数违约的具体类别。
type ViolationKind int

const (
	// ViolationOneToOneSource ONE_TO_ONE 的源端已有链。
	ViolationOneToOneSource ViolationKind = iota
	// ViolationOneToOneTarget ONE_TO_ONE 的目标端已被占用。
	ViolationOneToOneTarget
	// ViolationOneToManyTarget ONE_TO_MANY 的目标端已归属其他源。
	ViolationOneToManyTarget
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationOneToOneSource:
		return "ONE_TO_ONE_SOURCE_OCCUPIED"
	case ViolationOneToOneTarget:
		return "ONE_TO_ONE_TARGET_OCCUPIED"
	case ViolationOneToManyTarget:
		return "ONE_TO_MANY_TARGET_OCCUPIED"
	default:
		return "UNKNOWN"
	}
}

// CardinalityError 表示基数约束违约，可通过 errors.As 判定并读取 Kind。
type CardinalityError struct {
	LinkType string
	Source   ObjectKey
	Target   ObjectKey
	Kind     ViolationKind
}

func (e *CardinalityError) Error() string {
	return fmt.Sprintf("cardinality violation on %s (%s -> %s): %s",
		e.LinkType, e.Source, e.Target, e.Kind)
}

// EndpointTypeError 表示端点 ObjectType 与 LinkType 声明不符。
type EndpointTypeError struct {
	LinkType       string
	Source         ObjectKey
	Target         ObjectKey
	WantSourceType string
	WantTargetType string
}

func (e *EndpointTypeError) Error() string {
	return fmt.Sprintf("endpoint type mismatch on %s: got (%s, %s), want (%s, %s)",
		e.LinkType, e.Source.Type, e.Target.Type, e.WantSourceType, e.WantTargetType)
}

// ObjectNotFoundError 表示端点对象不存在；Side 为 "source" / "target" / "object"。
type ObjectNotFoundError struct {
	LinkType string
	Source   ObjectKey
	Target   ObjectKey
	Side     string
}

func (e *ObjectNotFoundError) Error() string {
	return fmt.Sprintf("object not found on %s: %s side (%s, %s)",
		e.LinkType, e.Side, e.Source, e.Target)
}

// DuplicateLinkError 表示重复建立同一条链。
type DuplicateLinkError struct {
	LinkType string
	Source   ObjectKey
	Target   ObjectKey
}

func (e *DuplicateLinkError) Error() string {
	return fmt.Sprintf("duplicate link on %s (%s -> %s)", e.LinkType, e.Source, e.Target)
}

// LinkNotFoundError 表示断开的链不存在。
type LinkNotFoundError struct {
	LinkType string
	Source   ObjectKey
	Target   ObjectKey
}

func (e *LinkNotFoundError) Error() string {
	return fmt.Sprintf("link not found on %s (%s -> %s)", e.LinkType, e.Source, e.Target)
}

// RestrictError 表示级联删除被 RESTRICT 链拒绝；Path 为从被删对象到该链的完整路径。
type RestrictError struct {
	LinkType string
	Source   ObjectKey
	Target   ObjectKey
	Path     []PathStep
}

func (e *RestrictError) Error() string {
	return fmt.Sprintf("delete restricted by %s (%s -> %s), path length %d",
		e.LinkType, e.Source, e.Target, len(e.Path))
}

// RequiredViolation 描述一条缺失的必选关系。
type RequiredViolation struct {
	LinkType string
	Side     string // "source" 或 "target"
	Object   ObjectKey
}

// RequiredError 在提交时一次报出全部必选性违约。
type RequiredError struct {
	Violations []RequiredViolation
}

func (e *RequiredError) Error() string {
	return fmt.Sprintf("required link violations: %d missing", len(e.Violations))
}

// BatchError 包装批内失败的操作，Index 为操作在批内的序号。
type BatchError struct {
	Index int
	Op    BatchOp
	Err   error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("batch op #%d (%s on %s) failed: %v",
		e.Index, e.Op.Kind, e.Op.LinkType, e.Err)
}

func (e *BatchError) Unwrap() error { return e.Err }
