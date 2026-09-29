package replication

// 本包对外暴露的、可互相区分的拒绝原因。
var (
	errInvalidArgument = sentinelError("replication: invalid argument")
	errUnknownReplica  = sentinelError("replication: unknown replica")
	errUnknownEpoch    = sentinelError("replication: unknown epoch")
	errLogDiverged     = sentinelError("replication: log diverged from leader")
)

// ErrInvalidArgument 参数非法（空 id、负位点、未知状态等）。
func ErrInvalidArgument() error { return errInvalidArgument }

// ErrUnknownReplica 请求引用了集群中不存在的副本。
func ErrUnknownReplica() error { return errUnknownReplica }

// ErrUnknownEpoch 请求的世代在该副本的世代缓存中不存在。
func ErrUnknownEpoch() error { return errUnknownEpoch }

// ErrLogDiverged 同位点的条目标识不一致，日志已分叉。
func ErrLogDiverged() error { return errLogDiverged }

type sentinelError string

func (e sentinelError) Error() string { return string(e) }
