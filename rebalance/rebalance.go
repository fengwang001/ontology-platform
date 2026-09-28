// Package rebalance 实现消费组的增量协作式再均衡（incremental cooperative
// rebalancing）。
//
// 再均衡分两轮推进：
//   - 撤销轮：成员集合变化后，凡是当前持有者与新目标不一致的“消费中”
//     分区进入“撤销中”，等待持有者确认；“撤销中/无主”分区不受目标再次
//     变化的影响（已撤销的不会取消）。
//   - 分配轮：成员确认后其“撤销中”分区变“无主”；仅当全组不存在任何
//     “撤销中”分区（组静止）时，才把“无主”分区按当前目标分配出去并置
//     回“消费中”。
//
// 这样目标未发生变化的消费中分区全程不中断消费，且同一组变化序列无论
// 由多少执行体并发触发，最终结果都确定、可复现。
package rebalance

import (
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"sync"
)

// PartitionState 是单个分区的生命周期状态。
type PartitionState int

const (
	// Consuming 消费中：有持有者成员，该成员正在消费此分区。
	Consuming PartitionState = iota
	// Revoking 撤销中：持有者正在被要求放弃此分区，等待该成员确认。
	Revoking
	// Orphaned 无主：分区没有持有者，等待组静止后分配给当前目标。
	Orphaned
)

// ErrorCode 为每一类非法输入提供可区分的错误原因。
type ErrorCode int

const (
	// ErrInvalidArgument：参数本身非法（负的成员号、负的分区数等）。
	ErrInvalidArgument ErrorCode = iota
	// ErrDuplicateJoin：成员已在组中却再次加入。
	ErrDuplicateJoin
	// ErrMemberNotFound：操作引用的成员当前不在组中。
	ErrMemberNotFound
	// ErrNothingToConfirm：成员没有任何“撤销中”分区需要确认。
	ErrNothingToConfirm
)

// Error 携带稳定的错误码与可读说明。
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return e.Message }

// PartitionStatus 描述一个分区的当前状态与归属。
type PartitionStatus struct {
	State    PartitionState
	Owner    int  // 持有者成员；无主时为 -1
	Target   int  // 当前期望归属成员；成员集合为空时为 -1
	HasOwner bool // 是否存在持有者
}

func (s PartitionStatus) String() string {
	return fmt.Sprintf("{state:%s owner:%s target:%s}", stateName(s.State), targetName(s.Owner), targetName(s.Target))
}

type partition struct {
	state  PartitionState
	owner  int // -1 表示无主
	target int // -1 表示当前无目标
}

// Group 是一个消费组，维护成员集合与全部分区状态。
// 所有导出方法都可被多个执行体并发调用，内部以单一互斥锁串行化。
type Group struct {
	mu         sync.Mutex
	partitions []partition
	members    map[int]struct{}
	logger     *log.Logger
}

// NewGroup 创建消费组，n 为分区总数。n 为负属于编程错误，直接 panic。
func NewGroup(n int) *Group {
	if n < 0 {
		panic(&Error{Code: ErrInvalidArgument, Message: fmt.Sprintf("rebalance: partition count must be non-negative, got %d", n)})
	}
	g := &Group{
		partitions: make([]partition, n),
		members:    make(map[int]struct{}),
		logger:     log.New(os.Stderr, "rebalance: ", log.LstdFlags|log.Lmicroseconds),
	}
	for id := range g.partitions {
		g.partitions[id] = partition{state: Orphaned, owner: -1, target: -1}
	}
	return g
}

// SetLogOutput 重定向步骤日志（默认 os.Stderr）；传 io.Discard 可关闭日志。
func (g *Group) SetLogOutput(w io.Writer) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.logger.SetOutput(w)
}

// Join 让成员 member 加入消费组。
func (g *Group) Join(member int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if member < 0 {
		return &Error{Code: ErrInvalidArgument, Message: fmt.Sprintf("rebalance: invalid member id %d (must be non-negative)", member)}
	}
	g.logger.Printf("Join 输入: member=%d 当前成员=%v 当前分区=%s", member, g.sortedMembersLocked(), g.snapshotLocked())
	if _, exists := g.members[member]; exists {
		g.logger.Printf("Join 拒绝: member=%d 已在组中 (ErrDuplicateJoin)，状态不变", member)
		return &Error{Code: ErrDuplicateJoin, Message: fmt.Sprintf("rebalance: member %d already joined", member)}
	}
	g.members[member] = struct{}{}
	g.reconcileLocked("Join", member)
	return nil
}

// Leave 让成员 member 离开消费组。离开成员持有的消费中/撤销中分区都会
// 立刻变为无主；其他成员正在进行的撤销不受影响。
func (g *Group) Leave(member int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if member < 0 {
		return &Error{Code: ErrInvalidArgument, Message: fmt.Sprintf("rebalance: invalid member id %d (must be non-negative)", member)}
	}
	g.logger.Printf("Leave 输入: member=%d 当前成员=%v 当前分区=%s", member, g.sortedMembersLocked(), g.snapshotLocked())
	if _, exists := g.members[member]; !exists {
		g.logger.Printf("Leave 拒绝: member=%d 不在组中 (ErrMemberNotFound)，状态不变", member)
		return &Error{Code: ErrMemberNotFound, Message: fmt.Sprintf("rebalance: member %d not found", member)}
	}
	delete(g.members, member)
	for id := range g.partitions {
		p := &g.partitions[id]
		if p.owner == member {
			g.logger.Printf("Leave 判定: 分区=%d 离开成员持有(状态=%s)，直接置为无主", id, stateName(p.state))
			p.state = Orphaned
			p.owner = -1
		}
	}
	g.reconcileLocked("Leave", member)
	return nil
}

// ConfirmRevocations 由成员 member 确认撤销其全部撤销中分区。
// 确认后这些分区变无主；若确认使全组进入静止（无任何撤销中分区），
// 随即把所有无主分区按当前目标重新分配。
func (g *Group) ConfirmRevocations(member int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if member < 0 {
		return &Error{Code: ErrInvalidArgument, Message: fmt.Sprintf("rebalance: invalid member id %d (must be non-negative)", member)}
	}
	g.logger.Printf("ConfirmRevocations 输入: member=%d 当前成员=%v 当前分区=%s", member, g.sortedMembersLocked(), g.snapshotLocked())
	if _, exists := g.members[member]; !exists {
		g.logger.Printf("ConfirmRevocations 拒绝: member=%d 不在组中 (ErrMemberNotFound)，状态不变", member)
		return &Error{Code: ErrMemberNotFound, Message: fmt.Sprintf("rebalance: member %d not found", member)}
	}
	count := 0
	for id := range g.partitions {
		if p := &g.partitions[id]; p.state == Revoking && p.owner == member {
			count++
		}
	}
	if count == 0 {
		g.logger.Printf("ConfirmRevocations 拒绝: member=%d 无撤销中分区 (ErrNothingToConfirm)，状态不变", member)
		return &Error{Code: ErrNothingToConfirm, Message: fmt.Sprintf("rebalance: member %d has no revoking partitions to confirm", member)}
	}
	for id := range g.partitions {
		p := &g.partitions[id]
		if p.state == Revoking && p.owner == member {
			g.logger.Printf("ConfirmRevocations 判定: 分区=%d 经 member=%d 确认撤销，置为无主", id, member)
			p.state = Orphaned
			p.owner = -1
		}
	}
	g.assignIfQuiescentLocked("ConfirmRevocations")
	return nil
}

// Statuses 返回所有分区的确定性快照（按分区号升序）。
func (g *Group) Statuses() []PartitionStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	snap := g.snapshotLocked()
	out := make([]PartitionStatus, len(snap))
	copy(out, snap)
	return out
}

// Target 计算给定成员集合下分区 partition 的朴素目标成员。
//
// 规则：在不小于分区号的成员中取最小者；若不存在这样的成员，则在全部
// 成员中环形回绕取最小者。成员为空（或分区号为负）时第二个返回值为
// false。members 内部使用副本排序，调用方切片不会被修改。
func Target(partition int, members []int) (int, bool) {
	if partition < 0 || len(members) == 0 {
		return -1, false
	}
	sorted := append([]int(nil), members...)
	sort.Ints(sorted)
	for _, member := range sorted {
		if member >= partition {
			return member, true
		}
	}
	return sorted[0], true
}

// reconcileLocked 依据当前成员集合重算每个分区的目标，并在全组静止时
// 分配无主分区。调用方必须持有 g.mu，且刚刚完成一次成员集合变更。
func (g *Group) reconcileLocked(op string, changedMember int) {
	members := g.sortedMembersLocked()
	for id := range g.partitions {
		p := &g.partitions[id]
		oldTarget := p.target
		if target, ok := Target(id, members); ok {
			p.target = target
		} else {
			p.target = -1
		}
		if p.target != oldTarget {
			g.logger.Printf("%s 判定: 分区=%d 目标 %s -> %s (成员集合变化由 member=%d 触发)",
				op, id, targetName(oldTarget), targetName(p.target), changedMember)
		}
		switch p.state {
		case Consuming:
			// 仅“消费中”分区会因目标改变进入撤销；持有者已离组的情形
			// 已由 Leave 直接转为无主。
			if p.target != -1 && p.target != p.owner {
				g.logger.Printf("%s 判定: 分区=%d 持有者=%d 与新目标=%d 不一致，Consuming -> Revoking",
					op, id, p.owner, p.target)
				p.state = Revoking
			}
		case Revoking:
			// 已撤销的不因目标再变而取消：保持撤销中，等待原持有者确认。
			g.logger.Printf("%s 判定: 分区=%d 已在撤销中(持有者=%d)，目标变为 %s 也不取消",
				op, id, p.owner, targetName(p.target))
		case Orphaned:
			g.logger.Printf("%s 判定: 分区=%d 无主，等待全组静止后按目标=%s 分配", op, id, targetName(p.target))
		}
	}
	g.assignIfQuiescentLocked(op)
}

// assignIfQuiescentLocked 仅在全组不存在撤销中分区时，把无主分区分配给
// 当前目标并置为消费中。成员集合为空则不分配。
func (g *Group) assignIfQuiescentLocked(op string) {
	for id := range g.partitions {
		if g.partitions[id].state == Revoking {
			g.logger.Printf("%s 判定: 分区=%d 仍在撤销中，组未静止，本轮不分配任何无主分区", op, id)
			return
		}
	}
	if len(g.members) == 0 {
		g.logger.Printf("%s 判定: 组已静止但成员为空，无主分区保持无主，不分配", op)
		return
	}
	for id := range g.partitions {
		p := &g.partitions[id]
		if p.state != Orphaned {
			continue
		}
		if p.target == -1 {
			if target, ok := Target(id, g.sortedMembersLocked()); ok {
				p.target = target
			}
		}
		if p.target == -1 {
			continue
		}
		g.logger.Printf("%s 判定: 组已静止，无主分区=%d 分配给目标=%d，Orphaned -> Consuming", op, id, p.target)
		p.state = Consuming
		p.owner = p.target
	}
}

func (g *Group) sortedMembersLocked() []int {
	members := make([]int, 0, len(g.members))
	for member := range g.members {
		members = append(members, member)
	}
	sort.Ints(members)
	return members
}

func (g *Group) snapshotLocked() []PartitionStatus {
	out := make([]PartitionStatus, len(g.partitions))
	for id, p := range g.partitions {
		out[id] = PartitionStatus{
			State:    p.state,
			Owner:    p.owner,
			Target:   p.target,
			HasOwner: p.owner != -1,
		}
	}
	return out
}

func stateName(s PartitionState) string {
	switch s {
	case Consuming:
		return "Consuming"
	case Revoking:
		return "Revoking"
	default:
		return "Orphaned"
	}
}

func targetName(t int) string {
	if t == -1 {
		return "none"
	}
	return fmt.Sprintf("%d", t)
}
