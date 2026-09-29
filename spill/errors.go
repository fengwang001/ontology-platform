package spill

// Reason 描述一次被整体拒绝的追加/提交/回滚请求的可区分原因。
type Reason int

const (
	ReasonOK Reason = iota
	ReasonInvalidArgument
	ReasonTxnNotFound
	ReasonTxnDuplicate
	ReasonSpillStorageFull
)

func (r Reason) String() string {
	switch r {
	case ReasonOK:
		return "ok"
	case ReasonInvalidArgument:
		return "invalid argument"
	case ReasonTxnNotFound:
		return "transaction not found"
	case ReasonTxnDuplicate:
		return "transaction already exists"
	case ReasonSpillStorageFull:
		return "spill storage full"
	default:
		return "unknown"
	}
}

// Error 是本包所有被拒绝操作返回的错误类型，携带互不相同、可区分的原因码。
type Error struct {
	Reason Reason
	Op     string
	Msg    string
}

func (e *Error) Error() string {
	return "spill: " + e.Op + ": " + e.Reason.String() + ": " + e.Msg
}

func newError(op string, reason Reason, msg string) *Error {
	return &Error{Reason: reason, Op: op, Msg: msg}
}
