package mvgc

// RejectKind 表示操作被拒绝的原因类别。
type RejectKind int

const (
	ErrInvalidArgument  RejectKind = iota + 1 // 参数非法
	ErrExpired                                // 过期（ts 小于/不大于安全点）
	ErrDuplicate                              // 同键同 ts 重复
	ErrFull                                   // 记录总数已达上限
	ErrRollback                               // 安全点回退
	ErrSnapshotBlocked                        // 安全点被打开的快照阻塞
	ErrSnapshotNotFound                       // 快照不存在
)

// OpError 携带可区分的拒绝原因，所有失败操作均返回 *OpError。
type OpError struct {
	Kind RejectKind
	Msg  string
}

func (e *OpError) Error() string { return e.Msg }

func reject(kind RejectKind, msg string) *OpError {
	return &OpError{Kind: kind, Msg: msg}
}
