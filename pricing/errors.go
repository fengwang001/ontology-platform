package pricing

type ErrorKind string

const (
	KindInvalid  ErrorKind = "参数非法"
	KindClosed   ErrorKind = "月份已封账"
	KindOrder    ErrorKind = "时序错误"
	KindRewind   ErrorKind = "读数倒退"
	KindShortage ErrorKind = "读数不足"
)

type Error struct {
	kind ErrorKind
	msg  string
}

func (e Error) Error() string   { return string(e.kind) + ": " + e.msg }
func (e Error) Kind() ErrorKind { return e.kind }

func newError(kind ErrorKind, msg string) error {
	return Error{kind: kind, msg: msg}
}

var (
	errInvalid  = func(msg string) error { return newError(KindInvalid, msg) }
	errClosed   = newError(KindClosed, "月份已封账")
	errOrder    = newError(KindOrder, "时序错误")
	errRewind   = newError(KindRewind, "读数倒退")
	errShortage = newError(KindShortage, "读数不足")
)
