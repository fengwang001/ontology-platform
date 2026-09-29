package scheduler

// 可区分的哨兵错误，errors.Is 可直接判定。
var (
	ErrInvalidNodes  = &RejectError{Kind: KindInvalidNodes, Reason: "cluster nodes must be positive"}
	ErrInvalidJob    = &RejectError{Kind: KindInvalidJob, Reason: "job nodes must be positive and no more than cluster nodes"}
	ErrInvalidDur    = &RejectError{Kind: KindInvalidDuration, Reason: "job duration must be positive"}
	ErrDuplicateID   = &RejectError{Kind: KindDuplicateID, Reason: "duplicate job id"}
	ErrJobNotRunning = &RejectError{Kind: KindJobNotRunning, Reason: "job is not running"}
	ErrClockRewind   = &RejectError{Kind: KindClockRewind, Reason: "clock cannot move backwards"}
)

// ErrorKind 以可区分的枚举说明操作被拒绝的原因。
type ErrorKind int

const (
	KindInvalidNodes    ErrorKind = iota + 1 // 集群节点数非正
	KindInvalidJob                           // 作业节点数非正或超过 N
	KindInvalidDuration                      // 预估时长非正
	KindDuplicateID                          // 作业标识重复
	KindJobNotRunning                        // 结束的作业不在运行集合中
	KindClockRewind                          // 注入时钟被回拨
)

// RejectError 携带可区分的拒绝原因。
type RejectError struct {
	Kind   ErrorKind
	Reason string
}

func (e *RejectError) Error() string { return e.Reason }
