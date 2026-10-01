package liveness

// ErrorCode 以可区分的字符串标识每一类被拒绝的操作原因。
type ErrorCode string

const (
	ErrNegativeBlockID  ErrorCode = "negative_block_id"
	ErrDuplicateBlockID ErrorCode = "duplicate_block_id"
	ErrAlreadySealed    ErrorCode = "already_sealed"
	ErrSealedTwice      ErrorCode = "sealed_twice"
	ErrNoBlocks         ErrorCode = "no_blocks"
	ErrMissingSuccessor ErrorCode = "missing_successor"
	ErrNotSealed        ErrorCode = "not_sealed"
	ErrNoSuchBlock      ErrorCode = "no_such_block"
)

// AnalysisError 携带可程序化判别的错误码与上下文细节。
type AnalysisError struct {
	Code ErrorCode
	// Detail 给出判定所需的定位信息，例如缺失后继的块编号。
	Detail string
}

func (e *AnalysisError) Error() string {
	if e.Detail == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Detail
}
