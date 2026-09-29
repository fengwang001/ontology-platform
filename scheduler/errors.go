package scheduler

import "errors"

// 可区分的拒绝原因。被拒绝的操作不会改变任何计数或槽位。
var (
	// ErrInvalidSkew 允许偏斜 S 小于 1。
	ErrInvalidSkew = errors.New("scheduler: allowed skew must be >= 1")
	// ErrInvalidSlots 节点槽位容量非正。
	ErrInvalidSlots = errors.New("scheduler: node slots must be > 0")
	// ErrDuplicateNode 节点标识重复。
	ErrDuplicateNode = errors.New("scheduler: duplicate node id")
	// ErrEmptyZone 节点的可用区标签为空。
	ErrEmptyZone = errors.New("scheduler: zone label must not be empty")
	// ErrNoMatchingNode 标签要求无任何节点满足（不存在合格区）。
	ErrNoMatchingNode = errors.New("scheduler: no node satisfies required labels")
	// ErrDuplicateGroup 副本组标识重复。
	ErrDuplicateGroup = errors.New("scheduler: duplicate group id")
	// ErrUnknownGroup 副本组不存在。
	ErrUnknownGroup = errors.New("scheduler: unknown group")
	// ErrDuplicateReplica 副本已存在（重复调度）。
	ErrDuplicateReplica = errors.New("scheduler: duplicate replica id")
	// ErrUnknownReplica 删除的副本不存在。
	ErrUnknownReplica = errors.New("scheduler: unknown replica")
	// ErrUnschedulable 没有任何满足偏斜约束的可放置区/节点。
	ErrUnschedulable = errors.New("scheduler: no eligible placement")
	// ErrReservationReleased 预留已被释放（绑定失败后），不能再绑定。
	ErrReservationReleased = errors.New("scheduler: reservation already released")
	// ErrReservationBound 预留已绑定，不能重复绑定。
	ErrReservationBound = errors.New("scheduler: reservation already bound")
	// ErrBindingInFlight 预留正在异步绑定中，不能并发再绑定。
	ErrBindingInFlight = errors.New("scheduler: reservation binding in flight")
)
