package scheduler

import (
	"log/slog"
	"sync"
)

// Node 是带区标签与槽位容量的目标节点。
type Node struct {
	// ID 为节点唯一标识。
	ID string
	// Zone 为节点所在可用区标签。
	Zone string
	// Slots 为节点总槽位容量。
	Slots int
	// Labels 为节点携带的标签集合。
	Labels map[string]string
}

// Group 声明副本组的允许偏斜与必需节点标签集合。
type Group struct {
	// ID 为副本组唯一标识。
	ID string
	// Skew 为允许偏斜 S，必须 >= 1。
	Skew int
	// RequiredLabels 为节点必须全部满足的标签键值对。
	RequiredLabels map[string]string
}

// replicaState 描述单个副本在预留/绑定生命周期中的状态。
type replicaState string

const (
	stateReserved replicaState = "reserved"
	stateBinding  replicaState = "binding"
	stateBound    replicaState = "bound"
	stateReleased replicaState = "released"
)

// replica 记录一个副本的预留位置与生命周期状态。
type replica struct {
	id     string
	group  string
	node   string
	zone   string
	state  replicaState
	bindMu sync.Mutex
}

// nodeState 是节点在调度器内部的可变状态。
type nodeState struct {
	id     string
	zone   string
	slots  int
	labels map[string]string
	used   int
}

// groupState 是副本组在调度器内部的可变状态。
type groupState struct {
	id             string
	skew           int
	requiredLabels map[string]string
}

// Binder 执行异步绑定；返回非 nil 即视为绑定失败，预留被释放。
// 可在测试中注入故障。
type Binder func(groupID, replicaID, nodeID string) error

// Config 用于构造 Scheduler。
type Config struct {
	// Binder 为实际绑定函数；为 nil 时绑定总是成功。
	Binder Binder
	// Logger 用于打印输入、输出与判定依据；为 nil 时使用 slog 默认 logger。
	Logger *slog.Logger
}

// Scheduler 是并发安全的按可用区均匀打散的副本调度器。
type Scheduler struct {
	impl *schedulerImpl
}

var _ = slog.Default
