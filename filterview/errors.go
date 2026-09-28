package filterview

// OpError 描述一次被拒绝的变更及其可区分的原因。
// Err 字段始终是本包导出的哨兵错误之一（ErrInvalidArgument、
// ErrEmptyKey、ErrKeyMismatch、ErrDuplicateKey、ErrKeyNotFound、
// ErrPreimageMismatch），调用方可使用 errors.Is 判定具体原因。
type OpError struct {
	// Index 是导致拒绝的操作在批处理中的下标（从 0 开始）。
	Index int
	// Op 是被拒绝的操作；更新操作包含前像与后像。
	Op Op
	// Err 是可区分的拒绝原因（哨兵错误）。
	Err error
}

func (e *OpError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return "filterview: op at index " + itoa(e.Index) + " rejected: " + e.Err.Error()
}

func (e *OpError) Unwrap() error { return e.Err }

// BatchError 汇总导致整批被拒绝的所有原因。
type BatchError struct {
	// Errs 按下标顺序列出批内所有被拒绝操作的原因。
	Errs []*OpError
}

func (e *BatchError) Error() string {
	if e == nil || len(e.Errs) == 0 {
		return "filterview: batch rejected"
	}
	return "filterview: batch rejected with " + itoa(len(e.Errs)) + " invalid op(s); first: " + e.Errs[0].Error()
}

func (e *BatchError) Unwrap() []error {
	out := make([]error, len(e.Errs))
	for i, err := range e.Errs {
		out[i] = err
	}
	return out
}
