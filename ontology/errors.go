package ontology

// Code 是三类彼此互斥的终态拒绝原因。
type Code string

const (
	// CodeVersionConflict 基线落后导致的版本冲突（非重试策略或首次即落后且不重试时）。
	CodeVersionConflict Code = "VERSION_CONFLICT"
	// CodeCardinality 基于最新读取重新校验后基数约束仍不满足（业务拒绝）。
	CodeCardinality Code = "CARDINALITY_VIOLATION"
	// CodeRetriesExhausted 达到内部重试次数上限仍未成功（重试耗尽）。
	CodeRetriesExhausted Code = "RETRIES_EXHAUSTED"
)

// ErrNotFound 表示目标实例或链接类型不存在。
type ErrNotFound struct{ What string }

func (e *ErrNotFound) Error() string { return e.What + " not found" }

// ErrInvalid 表示请求非法（未设置基数等输入问题）。
type ErrInvalid struct{ Msg string }

func (e *ErrInvalid) Error() string { return "invalid request: " + e.Msg }
