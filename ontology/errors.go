package ontology

import "errors"

// 以下错误互不相同、可用 errors.Is 区分，分别对应不同的拒绝原因。
var (
	// ErrInvalidRetention 构造参数：墓碑保留期必须为正数。
	ErrInvalidRetention = errors.New("invalid retention: must be > 0")

	ErrBatchEmpty         = errors.New("invalid batch: empty")
	ErrNilBatch           = errors.New("invalid batch: nil events slice is not allowed")
	ErrEntryLimitExceeded = errors.New("invalid batch: entry count would exceed MaxEntries")

	ErrEmptyKey      = errors.New("invalid event: empty key")
	ErrBadVersion    = errors.New("invalid event: version must be > 0")
	ErrUnknownOp     = errors.New("invalid event: unknown op")
	ErrValueOnDelete  = errors.New("invalid event: delete must not carry a value")
	ErrMissingValue   = errors.New("invalid event: upsert requires a non-empty value")
)

// RejectError 携带可机读的拒绝原因及批内位置。
type RejectError struct {
	Cause error
	// Index 为出错事件在批内的下标；批次级错误时为 -1。
	Index int
}

func (e *RejectError) Error() string {
	if e.Index < 0 {
		return e.Cause.Error()
	}
	return e.Cause.Error() + " (event index " + itoa(e.Index) + ")"
}

func (e *RejectError) Unwrap() error { return e.Cause }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
