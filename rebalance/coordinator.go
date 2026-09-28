// Package rebalance 实现消费组的增量协作式再均衡（两轮协议）。
//
// 第一轮：成员变化时，目标发生改变的“消费中”分区进入“撤销中”，
// 等待旧持有者确认撤销；无需迁移的分区保持消费不中断。
// 第二轮：当全组不再存在“撤销中”分区时，把所有“无主”分区
// 按当前目标分配并置为“消费中”。
package rebalance

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 错误类别：互不相同、可区分；任何一次被拒的操作都不会改变状态。
var (
	// ErrInvalidArgument 非法参数（分区数非正、成员 ID 非正、分区号越界等）。
	ErrInvalidArgument = errors.New("rebalance: invalid argument")
	// ErrMemberExists 重复加入：成员已在组内。
	ErrMemberExists = errors.New("rebalance: member already exists")
	// ErrMemberNotFound 成员不存在：离开或确认时找不到该成员。
	ErrMemberNotFound = errors.New("rebalance: member not found")
	// ErrNoPendingRevocation 无待确认撤销：该成员当前没有“撤销中”分区。
	ErrNoPendingRevocation = errors.New("rebalance: no pending revocation")
)

// State 是单个分区的生命周期状态。
type State int

const (
	// Unassigned 无主：暂无持有者，等待第二轮分配。
	Unassigned State = iota
	// Consuming 消费中：被某个成员稳定持有。
	Consuming
	// Revoking 撤销中：等待当前持有者确认撤销。
	Revoking
)

func (s State) String() string {
	switch s {
	case Unassigned:
		return "unassigned"
	case Consuming:
		return "consuming"
	case Revoking:
		return "revoking"
	default:
		return "unknown"
	}
}

// Partition 描述一个分区的当前状态与持有者（无主时 Owner 为 -1）。
type Partition struct {
	ID    int
	State State
	Owner int
}

// Logger 接收协调器的判定依据日志（每步输入、分区状态、决策原因）。
type Logger func(format string, args ...any)

// Coordinator 是并发安全的增量协作式再均衡协调器。
type Coordinator struct {
	mu         sync.Mutex
	partitions []Partition
	members    map[int]struct{}
	sorted     []int // members 的升序缓存，保证目标计算确定可复现
	logf       Logger
}

// NewCoordinator 创建拥有 n 个分区（编号 0..n-1）的协调器。
// n <= 0 时返回 ErrInvalidArgument。
func NewCoordinator(n int, logf Logger) (*Coordinator, error) {
	if n <= 0 {
		return nil, fmt.Errorf("%w: partition count must be positive, got %d", ErrInvalidArgument, n)
	}
	c := &Coordinator{
		partitions: make([]Partition, n),
		members:    make(map[int]struct{}),
		logf:       logf,
	}
	for i := range c.partitions {
		c.partitions[i] = Partition{ID: i, State: Unassigned, Owner: -1}
	}
	return c, nil
}

// Join 将成员加入消费组，触发第一轮（必要时进入撤销中）。
// 成员 ID 必须为正整数；重复加入返回 ErrMemberExists。
func (c *Coordinator) Join(memberID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if memberID <= 0 {
		return fmt.Errorf("%w: member id must be positive, got %d", ErrInvalidArgument, memberID)
	}
	if _, ok := c.members[memberID]; ok {
		return fmt.Errorf("%w: member %d already in group", ErrMemberExists, memberID)
	}
	c.log("join member=%d: accepted, members=%v", memberID, c.sorted)
	c.members[memberID] = struct{}{}
	c.rebuildSorted()
	c.rebalance("join", memberID)
	return nil
}

// Leave 将成员移出消费组，其持有的分区立即变为无主，并触发第一轮。
// 成员不存在返回 ErrMemberNotFound。
func (c *Coordinator) Leave(memberID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if memberID <= 0 {
		return fmt.Errorf("%w: member id must be positive, got %d", ErrInvalidArgument, memberID)
	}
	if _, ok := c.members[memberID]; !ok {
		return fmt.Errorf("%w: cannot leave, member %d", ErrMemberNotFound, memberID)
	}
	c.log("leave member=%d: accepted, members=%v", memberID, c.sorted)
	delete(c.members, memberID)
	c.rebuildSorted()
	for i := range c.partitions {
		if c.partitions[i].Owner == memberID {
			c.log("leave member=%d: partition %d was %s owned by leaver, becomes unassigned",
				memberID, i, c.partitions[i].State)
			c.partitions[i].State = Unassigned
			c.partitions[i].Owner = -1
		}
	}
	c.rebalance("leave", memberID)
	return nil
}

// ConfirmRevocation 成员确认其全部“撤销中”分区，这些分区变为无主；
// 若此后全组无“撤销中”分区，则触发第二轮分配。
// 成员不存在返回 ErrMemberNotFound；无待确认撤销返回 ErrNoPendingRevocation。
func (c *Coordinator) ConfirmRevocation(memberID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if memberID <= 0 {
		return fmt.Errorf("%w: member id must be positive, got %d", ErrInvalidArgument, memberID)
	}
	if _, ok := c.members[memberID]; !ok {
		return fmt.Errorf("%w: cannot confirm, member %d", ErrMemberNotFound, memberID)
	}
	pending := 0
	for i := range c.partitions {
		if c.partitions[i].State == Revoking && c.partitions[i].Owner == memberID {
			pending++
		}
	}
	if pending == 0 {
		return fmt.Errorf("%w: member %d has no revoking partition", ErrNoPendingRevocation, memberID)
	}
	c.log("confirm member=%d: accepted, %d revoking partition(s) to release", memberID, pending)
	for i := range c.partitions {
		if c.partitions[i].State == Revoking && c.partitions[i].Owner == memberID {
			c.log("confirm member=%d: partition %d revoked, becomes unassigned", memberID, i)
			c.partitions[i].State = Unassigned
			c.partitions[i].Owner = -1
		}
	}
	c.rebalance("confirm", memberID)
	return nil
}

// Partition 返回指定分区的快照；分区号越界返回 ErrInvalidArgument。
func (c *Coordinator) Partition(id int) (Partition, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id < 0 || id >= len(c.partitions) {
		return Partition{}, fmt.Errorf("%w: partition %d out of range [0,%d)", ErrInvalidArgument, id, len(c.partitions))
	}
	return c.partitions[id], nil
}

// Snapshot 返回全部分区状态与当前成员列表（升序）的只读快照。
func (c *Coordinator) Snapshot() (parts []Partition, members []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	parts = make([]Partition, len(c.partitions))
	copy(parts, c.partitions)
	members = make([]int, len(c.sorted))
	copy(members, c.sorted)
	return parts, members
}

// Members 返回当前成员列表（升序）。
func (c *Coordinator) Members() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, len(c.sorted))
	copy(out, c.sorted)
	return out
}

// rebalance 在成员集合或撤销确认变化后推进协议（调用方须持锁）。
// 第一轮：目标改变的的“消费中”分区进入“撤销中”（“撤销中”分区不因此取消）。
// 第二轮：全组无“撤销中”分区时，把“无主”分区分配给当前目标。
func (c *Coordinator) rebalance(op string, memberID int) {
	// 第一轮：仅处理“消费中”且目标改变的分区。
	for i := range c.partitions {
		p := &c.partitions[i]
		if p.State != Consuming {
			continue
		}
		t := target(c.sorted, i)
		if t != p.Owner {
			c.log("%s member=%d: partition %d target %d != owner %d, consuming -> revoking",
				op, memberID, i, t, p.Owner)
			p.State = Revoking
		} else {
			c.log("%s member=%d: partition %d target unchanged (owner %d), keep consuming",
				op, memberID, i, p.Owner)
		}
	}
	// 第二轮：全组无“撤销中”分区时才允许分配。
	revoking := 0
	for i := range c.partitions {
		if c.partitions[i].State == Revoking {
			revoking++
		}
	}
	if revoking > 0 {
		c.log("%s member=%d: %d partition(s) still revoking, assignment round deferred",
			op, memberID, revoking)
		return
	}
	if len(c.sorted) == 0 {
		c.log("%s member=%d: group empty, unassigned partitions stay unassigned", op, memberID)
		return
	}
	for i := range c.partitions {
		p := &c.partitions[i]
		if p.State != Unassigned {
			continue
		}
		t := target(c.sorted, i)
		c.log("%s member=%d: no revoking left, assign partition %d to target %d, unassigned -> consuming",
			op, memberID, i, t)
		p.State = Consuming
		p.Owner = t
	}
}

func (c *Coordinator) rebuildSorted() {
	c.sorted = c.sorted[:0]
	for m := range c.members {
		c.sorted = append(c.sorted, m)
	}
	sort.Ints(c.sorted)
}

func (c *Coordinator) log(format string, args ...any) {
	if c.logf != nil {
		c.logf(format, args...)
	}
}

// target 计算分区 p 的目标持有者：取不小于 p 的最小成员，
// 不存在则环形回绕取最小成员。members 为空返回 -1。
func target(members []int, p int) int {
	if len(members) == 0 {
		return -1
	}
	i := sort.SearchInts(members, p)
	if i == len(members) {
		return members[0]
	}
	return members[i]
}
