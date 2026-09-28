package weightedsample

import "fmt"

// ErrorKind 以稳定的字符串标识抽样器的错误类别，便于调用方区分处理。
type ErrorKind string

const (
	// ErrInvalidSampleSize 样本数 k 非法（非正整数）。
	ErrInvalidSampleSize ErrorKind = "invalid_sample_size"
	// ErrZeroWeight 元素权重为零。
	ErrZeroWeight ErrorKind = "zero_weight"
	// ErrInvalidWeight 元素权重非法（负数或非有限值）。
	ErrInvalidWeight ErrorKind = "invalid_weight"
	// ErrEmptyID 元素标识为空字符串。
	ErrEmptyID ErrorKind = "empty_id"
	// ErrDuplicateID 元素标识与已接受元素重复。
	ErrDuplicateID ErrorKind = "duplicate_id"
	// ErrRandomExhausted 随机源已用尽，无法为被接受元素取数。
	ErrRandomExhausted ErrorKind = "random_exhausted"
	// ErrInvalidRandom 随机源给出的数不在 [0,1) 内或随机源本身非法。
	ErrInvalidRandom ErrorKind = "invalid_random"
)

// Error 携带稳定的错误类别与可读信息。
type Error struct {
	Kind ErrorKind
	detail string
}

func (e *Error) Error() string {
	if e.detail == "" {
		return string(e.Kind)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.detail)
}

// Is 支持按错误类别比较（errors.Is）。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

func kindError(kind ErrorKind, detail string) *Error {
	return &Error{Kind: kind, detail: detail}
}
