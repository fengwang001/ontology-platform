package asyncbuf

import (
	"errors"
	"fmt"
)

// 运行期输入错误的类别。每一种都对应一个可区分的哨兵错误，
// 调用方可以用 errors.Is 判定具体原因。
type ErrorKind int

const (
	// ErrCapacityFull 占用已达容量上限，元素被拒绝。
	ErrCapacityFull ErrorKind = iota + 1
	// ErrEmptyID 元素标识为空。
	ErrEmptyID
	// ErrDuplicateID 元素标识与缓冲中已有元素重复。
	ErrDuplicateID
	// ErrUnknownID 完成声明指向缓冲中不存在的标识。
	ErrUnknownID
	// ErrAlreadyCompleted 对已完成的元素重复声明完成。
	ErrAlreadyCompleted
	// ErrWatermarkNotMonotonic 水位线未严格递增。
	ErrWatermarkNotMonotonic
	// ErrInvalidArgument 构造参数非法（容量或模式）。
	ErrInvalidArgument
)

// sentinel 错误，便于 errors.Is 区分原因。
var (
	errCapacityFull          = errors.New("asyncbuf: capacity full")
	errEmptyID               = errors.New("asyncbuf: empty element id")
	errDuplicateID           = errors.New("asyncbuf: duplicate element id")
	errUnknownID             = errors.New("asyncbuf: unknown element id")
	errAlreadyCompleted      = errors.New("asyncbuf: element already completed")
	errWatermarkNotMonotonic = errors.New("asyncbuf: watermark must be strictly increasing")
	errInvalidArgument       = errors.New("asyncbuf: invalid argument")
)

// Error 携带类别与上下文（标识、水位线等），并可通过 errors.Is 匹配哨兵错误。
type Error struct {
	Kind      ErrorKind
	ID        string
	Watermark int64
	Detail    string
}

func (e *Error) Error() string {
	switch e.Kind {
	case ErrCapacityFull:
		return fmt.Sprintf("asyncbuf: submit element id=%q rejected: %s", e.ID, e.Detail)
	case ErrEmptyID:
		return "asyncbuf: submit element rejected: empty id"
	case ErrDuplicateID:
		return fmt.Sprintf("asyncbuf: submit element id=%q rejected: duplicate id", e.ID)
	case ErrUnknownID:
		return fmt.Sprintf("asyncbuf: complete id=%q rejected: unknown id", e.ID)
	case ErrAlreadyCompleted:
		return fmt.Sprintf("asyncbuf: complete id=%q rejected: already completed", e.ID)
	case ErrWatermarkNotMonotonic:
		return fmt.Sprintf("asyncbuf: submit watermark=%d rejected: not strictly increasing", e.Watermark)
	case ErrInvalidArgument:
		return fmt.Sprintf("asyncbuf: invalid argument: %s", e.Detail)
	default:
		return "asyncbuf: unknown error"
	}
}

func (e *Error) Unwrap() error {
	switch e.Kind {
	case ErrCapacityFull:
		return errCapacityFull
	case ErrEmptyID:
		return errEmptyID
	case ErrDuplicateID:
		return errDuplicateID
	case ErrUnknownID:
		return errUnknownID
	case ErrAlreadyCompleted:
		return errAlreadyCompleted
	case ErrWatermarkNotMonotonic:
		return errWatermarkNotMonotonic
	case ErrInvalidArgument:
		return errInvalidArgument
	default:
		return nil
	}
}
