package antientropy

import "errors"

// 非法输入的可区分错误类别。任何一次拒绝都不得改变双方日志或向量。
var (
	// ErrUnregisteredReplica：同步对端或变更来源为未注册副本。
	ErrUnregisteredReplica = errors.New("antientropy: unregistered replica")
	// ErrNonContiguousChange：变更序号缺失、重复、乱序或与目标已见序号不衔接。
	ErrNonContiguousChange = errors.New("antientropy: non-contiguous change sequence")
	// ErrInvalidVector：版本向量含负数等非法条目。
	ErrInvalidVector = errors.New("antientropy: invalid version vector")
	// ErrLogLimitExceeded：本次同步差集条数超过配置上限。
	ErrLogLimitExceeded = errors.New("antientropy: change batch exceeds log size limit")
)
