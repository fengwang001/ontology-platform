package rebalance

import (
	"io"
	"log"
	"os"
	"sort"
	"sync"
)

// State 是单个分区的再均衡状态。
type State int

const (
	// Consuming 消费中：分区有持有者且正在消费。
	Consuming State = iota
	// Revoking 撤销中：持有者已被要求交出，等待该成员确认。
	Revoking
	// Orphan 无主：已撤销、等待下一次静止点分配。
	Orphan
)

// PartitionStatus 是一个分区在某一时刻的可观测快照。
type PartitionStatus struct {
	Partition int
	State     State
	// Holder 为当前持有者；无主时为 -1。
	Holder int
	// Target 为按当前成员集合计算出的目标成员；成员为空时为 -1。
	Target int
}

// Group 是一个支持并发调用的增量协作式再均衡消费组。
type Group struct {
	mu        sync.Mutex
	members   map[int]struct{}
	partitions map[int]*partition
	logger    *log.Logger
	step      int
}

type partition struct {
	id     int
	state  State
	holder int // -1 表示无主
}

// NewGroup 创建消费组。partitions 为分区编号集合，初始全部无主。
func NewGroup(partitions []int, logger *log.Logger) *Group {
	return nil
}

// Join 成员加入；返回错误时不改变任何状态。
func (g *Group) Join(member int) error {
	return nil
}

// Leave 成员离开；返回错误时不改变任何状态。
func (g *Group) Leave(member int) error {
	return nil
}

// AckRevoke 成员确认其全部撤销中分区；返回错误时不改变任何状态。
func (g *Group) AckRevoke(member int) error {
	return nil
}

// Snapshot 返回全部成员与分区状态的确定性快照（成员、分区均按编号升序）。
func (g *Group) Snapshot() (members []int, statuses []PartitionStatus) {
	return nil, nil
}

// SetOutput 替换每步判定日志的输出（默认 stderr；传 io.Discard 可静默）。
func (g *Group) SetOutput(w io.Writer) {}

// TargetOf 返回指定分区按当前成员集合计算出的朴素目标；成员为空时为 -1。
func (g *Group) TargetOf(partitionID int) (int, error) {
	return 0, nil
}

// defaultLogger 与内部辅助函数将在后续实现。
var _ = os.Stderr
var _ = sort.Ints
