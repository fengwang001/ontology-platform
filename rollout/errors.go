package rollout

import "errors"

// 配置与操作错误。被拒绝的操作不改变任何状态。
var (
	ErrInvalidConfig = errors.New("rollout: invalid config")
	ErrInvalidArg    = errors.New("rollout: invalid argument")
	ErrClockRewind   = errors.New("rollout: clock moved backwards")
	ErrNoUnreadyNew  = errors.New("rollout: not enough unready new instances")
	ErrNoReadyNew    = errors.New("rollout: not enough ready new instances")
	ErrNoReadyOld    = errors.New("rollout: not enough ready old instances")
)
