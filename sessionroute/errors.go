package sessionroute

// Reason 表示一次操作被拒绝的可区分原因。
type Reason string

const (
	// ReasonUnknownSession 表示会话尚未注册。
	ReasonUnknownSession Reason = "unknown_session"
	// ReasonUnknownReplica 表示推进操作引用了不存在的副本。
	ReasonUnknownReplica Reason = "unknown_replica"
	// ReasonReplicaBehind 表示没有任何副本已应用到会话令牌要求的序号。
	ReasonReplicaBehind Reason = "replica_behind"
	// ReasonProgressRollback 表示推进序号小于副本当前已应用序号。
	ReasonProgressRollback Reason = "progress_rollback"
	// ReasonProgressAhead 表示推进序号超过主库当前最大序号。
	ReasonProgressAhead Reason = "progress_ahead"
)

// RejectError 描述一次因业务规则被拒绝的操作及其原因。
type RejectError struct {
	Reason Reason
	msg    string
}

func (e *RejectError) Error() string { return e.msg }

func reject(reason Reason, msg string) *RejectError {
	return &RejectError{Reason: reason, msg: msg}
}
