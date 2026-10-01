package reassign

import "sync"

// PartitionState 是分区的对外只读快照。
type PartitionState struct {
	Replicas    []string
	ISR         []string
	Leader      string // 空串表示无领导者
	Reassigning bool
	Target      []string
}

type partition struct {
	replicas []string
	isr      map[string]bool
	leader   string
	original []string // 非 nil 表示有进行中的重分配
	target   []string
}

// Controller 管理分区副本重分配，所有方法可并发调用。
type Controller struct {
	mu          sync.Mutex
	nodes       map[string]bool // 已知节点及其存活标记
	partitions  map[string]*partition
	maxInflight int
	inflight    int
}

func NewController(maxInflight int) *Controller {
	return &Controller{
		nodes:       make(map[string]bool),
		partitions:  make(map[string]*partition),
		maxInflight: maxInflight,
	}
}
