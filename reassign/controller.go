package reassign

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

// Reason 标识操作被拒绝的可区分原因。
type Reason string

const (
	ReasonPartitionNotFound Reason = "partition_not_found"
	ReasonAlreadyRunning    Reason = "already_running"
	ReasonEmptyTarget       Reason = "empty_target"
	ReasonDuplicateReplica  Reason = "duplicate_replica"
	ReasonUnknownNode       Reason = "unknown_node"
	ReasonNodeDown          Reason = "node_down"
	ReasonTargetUnchanged   Reason = "target_unchanged"
	ReasonConcurrencyLimit  Reason = "concurrency_limit"
	ReasonNodeNotReplica    Reason = "node_not_replica"
	ReasonAlreadyInISR      Reason = "already_in_isr"
	ReasonNotRunning        Reason = "not_running"
)

// ReassignError 携带可区分的拒绝原因。
type ReassignError struct {
	Reason Reason
}

func (e *ReassignError) Error() string { return string(e.Reason) }

func fail(reason Reason) *ReassignError { return &ReassignError{Reason: reason} }

// PartitionView 是分区对外可见的快照。
type PartitionView struct {
	Replicas []string
	ISR      []string
	Leader   string // 空字符串表示无领导者
}

// partition 是分区的内部状态。所有字段只能在持锁时访问。
type partition struct {
	replicas []string // 有序副本列表
	isr      map[string]bool
	leader   string // 空字符串表示无领导者

	running bool
	orig    []string // 重分配开始前的副本列表（原列表）
	target  []string
}

func (p *partition) isrOrdered() []string {
	out := make([]string, 0, len(p.isr))
	for _, node := range p.replicas {
		if p.isr[node] {
			out = append(out, node)
		}
	}
	return out
}

func (p *partition) view() PartitionView {
	return PartitionView{
		Replicas: append([]string(nil), p.replicas...),
		ISR:      p.isrOrdered(),
		Leader:   p.leader,
	}
}

// firstInISR 按 order 次序返回第一个在 ISR 中的节点，无则返回空串。
func firstInISR(isr map[string]bool, order []string) string {
	for _, node := range order {
		if isr[node] {
			return node
		}
	}
	return ""
}

func sameMembers(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, node := range a {
		set[node] = true
	}
	for _, node := range b {
		if !set[node] {
			return false
		}
	}
	return true
}

func contains(list []string, node string) bool {
	for _, item := range list {
		if item == node {
			return true
		}
	}
	return false
}

// Controller 管理节点存活状态、分区副本状态与重分配流程。
// 单个互斥锁串行化所有状态变更，使并发调用等价于某个串行顺序。
type Controller struct {
	mu            sync.Mutex
	nodes         map[string]bool // node -> alive
	partitions    map[string]*partition
	maxConcurrent int
	runningCount  int
	logger        *log.Logger
}

// New 创建控制器，maxConcurrent 为全局同时进行中的重分配上限（必须 > 0）。
// 判定与输入输出日志写入标准错误。
func New(maxConcurrent int) *Controller {
	return NewWithLogger(maxConcurrent, os.Stderr)
}

// NewWithLogger 创建控制器并指定日志输出；w 为 io.Discard 可关闭日志。
func NewWithLogger(maxConcurrent int, w io.Writer) *Controller {
	if maxConcurrent <= 0 {
		panic(fmt.Sprintf("reassign: maxConcurrent must be positive, got %d", maxConcurrent))
	}
	if w == nil {
		w = io.Discard
	}
	return &Controller{
		nodes:         make(map[string]bool),
		partitions:    make(map[string]*partition),
		maxConcurrent: maxConcurrent,
		logger:        log.New(w, "[reassign] ", log.LstdFlags|log.Lmicroseconds),
	}
}

func (c *Controller) logf(format string, args ...any) {
	c.logger.Output(2, fmt.Sprintf(format, args...))
}

func viewString(v PartitionView) string {
	return fmt.Sprintf("replicas=%v isr=%v leader=%q", v.Replicas, v.ISR, v.Leader)
}

var (
	errEmptyReplicas   = errors.New("reassign: replicas must not be empty")
	errDuplicateNode   = errors.New("reassign: duplicate node in replicas")
	errNodeUnknown     = errors.New("reassign: unknown node in replicas")
	errNodeExists      = errors.New("reassign: node already exists")
	errPartitionExists = errors.New("reassign: partition already exists")
)
