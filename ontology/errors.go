package ontology

import "fmt"

// ErrorKind 对错误进行固定分类，报告时按固定优先级从高到低选取。
type ErrorKind int

const (
	KindTypeMismatch ErrorKind = iota + 1
	KindInstanceNotFound
	KindCycleRejected
	KindMaintenanceRollback
)

// OntoError 携带错误类别与描述，调用方可以用 Kind 区分错误类型。
type OntoError struct {
	Kind ErrorKind
	Msg  string
}

func (e *OntoError) Error() string {
	switch e.Kind {
	case KindTypeMismatch:
		return "type mismatch: " + e.Msg
	case KindInstanceNotFound:
		return "instance not found: " + e.Msg
	case KindCycleRejected:
		return "cycle rejected at runtime: " + e.Msg
	case KindMaintenanceRollback:
		return "maintenance failure, rolled back: " + e.Msg
	default:
		return e.Msg
	}
}

func errf(kind ErrorKind, format string, args ...any) error {
	return &OntoError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}
