package replication

import "errors"

// 各类被拒绝操作的可区分原因，调用方可用 errors.Is 判定。
var (
	// ErrInvalidRecord 日志记录非法：LSN 未递增、类型未知、
	// 事务未开启就出现数据/提交/中止、或重复开启同一事务。
	ErrInvalidRecord = errors.New("invalid record")
	// ErrConfirmNotOnBoundary 确认位点未落在某个已发出事务的提交位点上。
	ErrConfirmNotOnBoundary = errors.New("confirm lsn not on a commit boundary")
	// ErrConfirmRegression 确认位点未前进（小于或等于当前确认位点）。
	ErrConfirmRegression = errors.New("confirm lsn regression")
	// ErrTooManyInProgress 进行中事务数量超过上限。
	ErrTooManyInProgress = errors.New("too many in-progress transactions")
)
