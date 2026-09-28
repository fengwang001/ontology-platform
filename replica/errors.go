package replica

import "errors"

// 输入被整体拒绝时返回的哨兵错误，彼此互不相同，可用 errors.Is 精确区分。
var (
	// ErrInvalidArgument：参数为零值或明显非法（空副本名、nil 配置等）。
ErrInvalidArgument = errors.New("replica: invalid argument")
	// ErrUnknownReplica：副本既不在同步副本集，也未曾登记。
ErrUnknownReplica = errors.New("replica: unknown replica")
	// ErrLeaderReserved：以领导者副本名注册跟随者，或对领导者执行跟随者操作。
ErrLeaderReserved = errors.New("replica: leader id is reserved")
	// ErrInvalidOffset：位点为负，或拉取位点超过当前已知最大位点。
ErrInvalidOffset = errors.New("replica: offset out of range")
	// ErrClockMovedBack：同一执行体传入的时间戳早于其上一次观测时间。
ErrClockMovedBack = errors.New("replica: clock moved backwards")
	// ErrStaleConfig：容忍阈值非正，或时间戳早于系统已观测时间（见 Now 注入）。
ErrStaleConfig = errors.New("replica: stale or illegal configuration")
	// ErrReplicaEvicted：副本已被移出同步副本集，必须先 Rejoin。
ErrReplicaEvicted = errors.New("replica: replica evicted from sync set")
	// ErrAlreadyMember：已在同步副本集中的副本重复加入。
ErrAlreadyMember = errors.New("replica: replica already a sync member")
)
