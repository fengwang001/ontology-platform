package surge

import "fmt"

// ErrorKind 以程序化方式区分各类拒绝原因。
type ErrorKind int

const (
	KindInvalidParam ErrorKind = iota
	KindClockRollback
	KindAreaNotFound
	KindRiderNotFound
	KindOrderNotFound
	KindRiderAlreadyOnline
	KindRiderAlreadyOffline
	KindRiderBusy
	KindNoNeedToMove
	KindOrderAlreadyDispatched
	KindOrderCompleted
	KindOrderCancelled
	KindRiderOffline
	KindRiderWrongArea
	KindRiderAtCapacity
	KindEvaluationTooFrequent
)

// Error 是所有被拒绝操作返回的错误，携带可判定的 ErrorKind。
type Error struct {
	Kind ErrorKind
	msg  string
}

func (e *Error) Error() string { return e.msg }

func errf(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, msg: fmt.Sprintf(format, args...)}
}
