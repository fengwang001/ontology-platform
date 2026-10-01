package mvcc

import "errors"

var (
	ErrTxIDNotPositive         = errors.New("mvcc: transaction id must be positive")
	ErrTxExists                = errors.New("mvcc: transaction id already exists")
	ErrParentNotFound          = errors.New("mvcc: parent transaction does not exist")
	ErrParentNotRunning        = errors.New("mvcc: parent transaction is not running")
	ErrTxNotFound              = errors.New("mvcc: transaction does not exist")
	ErrTxNotRunning            = errors.New("mvcc: transaction is not running")
	ErrTxTypeMismatch          = errors.New("mvcc: commit type does not match transaction type")
	ErrTxHasRunningDescendants = errors.New("mvcc: transaction has running descendants")
	ErrNegativeCommandID       = errors.New("mvcc: command id must be non-negative")
	ErrCommandIDTooSmall       = errors.New("mvcc: command id is smaller than the tree's max used command id")
	ErrTupleExists             = errors.New("mvcc: tuple already exists")
	ErrTupleNotFound           = errors.New("mvcc: tuple does not exist")
	ErrTupleDeleted            = errors.New("mvcc: tuple already has a non-aborted delete marker")
	ErrUnknownSnapshot         = errors.New("mvcc: unknown snapshot id")
)
