package replication

import "errors"

var (
	// ErrInvalidRecord 表示追加的日志记录非法（未知类型、LSN 非递增、
	// XID 为 0、未以 Begin 开始、重复 Begin 等）。
	ErrInvalidRecord = errors.New("replication: invalid log record")
	// ErrInvalidConfirm 表示确认位点没有恰好落在某个已发出事务的提交位点上。
	ErrInvalidConfirm = errors.New("replication: confirm LSN is not a committed transaction boundary")
	// ErrConfirmRewound 表示确认位点回退（小于当前确认位点）。
	ErrConfirmRewound = errors.New("replication: confirm LSN rewound")
	// ErrTooManyInProgress 表示进行中事务数超过配置上限。
	ErrTooManyInProgress = errors.New("replication: too many in-progress transactions")
	// ErrSlotClosed 表示复制槽已关闭。
	ErrSlotClosed = errors.New("replication: slot closed")
	// ErrCorrupted 表示持久化的日志或元数据损坏，无法恢复。
	ErrCorrupted = errors.New("replication: persisted state corrupted")
)
