package ontology

import "fmt"

// ErrKind 是可区分的错误类别，数值越小汇报优先级越高。
type ErrKind int

const (
	ErrMissingRef         ErrKind = iota // 属性或策略引用不存在
	ErrVisibilityConflict                // 可见性策略之间无法调和的直接冲突
	ErrMaskingCycle                      // 脱敏依赖关系构成循环
	ErrTypeViolation                     // 脱敏结果违反属性声明类型
)

// ClassifiedError 携带类别、相关属性与策略依据。
type ClassifiedError struct {
	Kind     ErrKind
	Attr     string
	Policies []string
	Detail   string
}

func (e ClassifiedError) Error() string {
	return fmt.Sprintf("[%s] attr=%q policies=%v: %s", e.Kind, e.Attr, e.Policies, e.Detail)
}

func (k ErrKind) rank() int { return int(k) }

func (k ErrKind) String() string {
	switch k {
	case ErrMissingRef:
		return "missing-reference"
	case ErrVisibilityConflict:
		return "visibility-conflict"
	case ErrMaskingCycle:
		return "masking-cycle"
	case ErrTypeViolation:
		return "type-violation"
	}
	return "unknown"
}
