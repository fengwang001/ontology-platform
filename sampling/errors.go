package sampling

import "errors"

// 采样器拒绝一次操作时可能返回的、彼此可区分的错误原因。
var (
	ErrRateOutOfRange = errors.New("sampling: rate out of range [0, 10000]")
	ErrEmptyKey       = errors.New("sampling: empty key")
	ErrTooManyKeys    = errors.New("sampling: known key limit exceeded")
)

// InvalidInput 携带拒绝原因，便于调用方按类别区分处理。
type InvalidInput struct {
	Reason error
}

func (e *InvalidInput) Error() string {
	if e == nil || e.Reason == nil {
		return "sampling: invalid input"
	}
	return e.Reason.Error()
}

func (e *InvalidInput) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Reason
}
