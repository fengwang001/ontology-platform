package outbox

import "errors"

var (
	// ErrTxNotFound 表示事务不存在（未开启、已提交或已中止）。
	ErrTxNotFound = errors.New("outbox: transaction not found")
	// ErrTxAlreadyExists 表示同名事务已经处于开启状态。
	ErrTxAlreadyExists = errors.New("outbox: transaction already exists")
	// ErrInvalidMessage 表示消息标识为空或载荷非法。
	ErrInvalidMessage = errors.New("outbox: invalid message")
	// ErrBacklogLimit 表示提交后待投积压超过上限。
	ErrBacklogLimit = errors.New("outbox: committed backlog over limit")
)

// OpError 携带被拒绝操作的可区分原因。
type OpError struct {
	Op  string
	Err error
}

func (e *OpError) Error() string { return e.Op + ": " + e.Err.Error() }

func (e *OpError) Unwrap() error { return e.Err }
