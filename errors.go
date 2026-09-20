package ontology

import (
	"errors"
	"strconv"
)

var (
	// ErrReleased 表示快照已经被归还，归还后的快照不得再读取。
	ErrReleased = errors.New("ontology: snapshot already released")
	// ErrVersionReclaimed 表示快照指向的版本已经被回收。
	ErrVersionReclaimed = errors.New("ontology: snapshot version has been reclaimed")
	// ErrFieldNotFound 表示读取了未声明的字段。
	ErrFieldNotFound = errors.New("ontology: field not declared")
)

// ValidationFailure 标识校验失败的类别。
type ValidationFailure int

const (
	// FailureUnknownField 字段名未声明。
	FailureUnknownField ValidationFailure = iota + 1
	// FailureTypeMismatch 值类型与字段声明不匹配。
	FailureTypeMismatch
	// FailureOutOfRange 整数字段的值超出声明的上下界。
	FailureOutOfRange
)

func (f ValidationFailure) String() string {
	switch f {
	case FailureUnknownField:
		return "unknown field"
	case FailureTypeMismatch:
		return "type mismatch"
	case FailureOutOfRange:
		return "integer out of range"
	default:
		return "unknown failure"
	}
}

// ValidationError 描述一次更新中某个字段的校验失败原因。
type ValidationError struct {
	Field  string
	Kind   ValidationFailure
	Detail string
}

func (e *ValidationError) Error() string {
	return "ontology: field " + strconv.Quote(e.Field) + ": " + e.Kind.String() + ": " + e.Detail
}

// ValidationErrors 聚合同一次更新里所有字段的校验失败。
type ValidationErrors []*ValidationError

func (es ValidationErrors) Error() string {
	if len(es) == 0 {
		return "ontology: validation failed"
	}
	return es[0].Error() + " (and " + strconv.Itoa(len(es)-1) + " more)"
}

// IsUnknownField 判断错误是否为“字段未声明”。
func IsUnknownField(err error) bool { return failureKind(err) == FailureUnknownField }

// IsTypeMismatch 判断错误是否为“类型不匹配”。
func IsTypeMismatch(err error) bool { return failureKind(err) == FailureTypeMismatch }

// IsOutOfRange 判断错误是否为“整数越界”。
func IsOutOfRange(err error) bool { return failureKind(err) == FailureOutOfRange }

func failureKind(err error) ValidationFailure {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve.Kind
	}
	return 0
}
