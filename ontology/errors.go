package ontology

import "fmt"

// ErrorKind 标识系统明确区分的错误类别。
// 各类别的汇报优先级固定且唯一：数值越小优先级越高。
//
//	UnknownAttribute  >  CyclicDependency  >  SnapshotExpired
type ErrorKind int

const (
	// ErrKindUnknownAttribute 判定规则或写操作引用了对象类型中不存在的属性。
	ErrKindUnknownAttribute ErrorKind = iota
	// ErrKindCyclicDependency 判定规则之间通过 TagRef 构成循环依赖。
	ErrKindCyclicDependency
	// ErrKindSnapshotExpired 可重复读声明的快照已超出保留窗口，不再可用。
	ErrKindSnapshotExpired
)

// Error 是模块内所有错误的统一载体。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// KindOf 返回 err 的错误类别；非模块错误返回 ok=false。
func KindOf(err error) (ErrorKind, bool) {
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return 0, false
}

// prioritize 按固定优先顺序从候选错误中选出唯一应汇报的错误。
// 优先级：UnknownAttribute > CyclicDependency > SnapshotExpired。
func prioritize(errs ...error) error {
	var best error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if best == nil {
			best = err
			continue
		}
		bk, bok := KindOf(best)
		ck, cok := KindOf(err)
		if cok && bok && ck < bk {
			best = err
		}
	}
	return best
}
