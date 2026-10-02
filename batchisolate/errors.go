package batchisolate

import "errors"

// ErrTransient 由 sink 返回，表示本次失败为瞬时错误，可在预算内重试。
// sink 返回的错误满足 errors.Is(err, ErrTransient) 即视为瞬时错误；
// 其余非 nil 错误一律视为永久错误。
var ErrTransient = errors.New("batchisolate: transient sink failure")

var (
	errInvalidArgs = errors.New("batchisolate: invalid arguments")
	errClosed      = errors.New("batchisolate: isolator is closed")
	errBudget      = errors.New("batchisolate: sink call budget exhausted")
)
