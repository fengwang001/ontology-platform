package replication

import "strconv"

// RejectReason 是整批被拒绝的可区分原因。
type RejectReason string

// 原因哨兵：可直接传给 errors.Is 与返回的 *RejectError 比较（仅按 Reason 匹配）。
// 冲突不经过该机制——它是 BatchResult.Conflicts 中的正常结果，不是错误。
var (
	// ErrIllegalEvent 非法事件：操作未知、主键为空、序号非正或前后像不符合该操作的要求。
	ErrIllegalEvent = &RejectError{Reason: RejectIllegalEvent}
	// ErrSequenceGap 序号不连续：批内事件序号未从已处理序号 +1 开始严格递增。
	ErrSequenceGap = &RejectError{Reason: RejectSequenceGap}
	// ErrReplicaFull 副本行数超限：接受本批插入会使副本行数超过配置上限。
	ErrReplicaFull = &RejectError{Reason: RejectReplicaFull}
)

const (
	// RejectIllegalEvent 非法事件原因。
	RejectIllegalEvent RejectReason = "ILLEGAL_EVENT"
	// RejectSequenceGap 序号不连续原因。
	RejectSequenceGap RejectReason = "SEQUENCE_GAP"
	// RejectReplicaFull 副本行数超限原因。
	RejectReplicaFull RejectReason = "REPLICA_FULL"
)

// RejectError 表示整批被拒绝。被拒绝的批不会改变副本、冲突日志或已处理序号。
// 通过 Reason 字段（或 errors.Is 比较原因哨兵）区分拒绝原因；
// 冲突（Conflict）不是错误，与拒绝在类型上即可区分。
type RejectError struct {
	// Reason 拒绝原因。
	Reason RejectReason
	// EventIndex 触发拒绝的事件在批内的下标（-1 表示与具体事件无关）。
	EventIndex int
	// Seq 触发拒绝的事件序号。
	Seq int64
	// ExpectedSeq 序号不连续时期望的序号；其余原因为 0。
	ExpectedSeq int64
	// Detail 人类可读的细节说明。
	Detail string
}

// Error 实现 error，输出包含原因、批内下标、序号与依据。
func (e *RejectError) Error() string {
	msg := "batch rejected: " + string(e.Reason)
	if e.EventIndex >= 0 {
		msg += " at event[" + strconv.Itoa(e.EventIndex) + "]"
	}
	if e.Seq != 0 {
		msg += " seq=" + strconv.FormatInt(e.Seq, 10)
	}
	if e.ExpectedSeq != 0 {
		msg += " expected_seq=" + strconv.FormatInt(e.ExpectedSeq, 10)
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

// Is 支持 errors.Is：target 为同 Reason 的 *RejectError（含上面的原因哨兵）即匹配，
// 便于调用方用 errors.Is(err, ErrIllegalEvent) 区分拒绝原因。
func (e *RejectError) Is(target error) bool {
	t, ok := target.(*RejectError)
	if !ok {
		return false
	}
	return e.Reason == t.Reason
}
