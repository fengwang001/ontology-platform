package retry

import "errors"

var (
	// ErrExhausted 表示次数用尽仍然失败。
	ErrExhausted = errors.New("retry: attempts exhausted")
	// ErrAborted 表示遇到不可重试的错误而中止。
	ErrAborted = errors.New("retry: aborted on permanent error")
)

// permanentError 标记一个不可重试的错误。
type permanentError struct{ err error }

func (e permanentError) Error() string { return "permanent: " + e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent 把 err 包装为不可重试的错误；err 为 nil 时返回 nil。
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

// isPermanent 报告 err 链上是否带有 Permanent 标记。
func isPermanent(err error) bool {
	var pe permanentError
	return errors.As(err, &pe)
}
