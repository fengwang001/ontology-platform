// Package group 实现一个消费组协调者的加入与同步状态机。
//
// 组有四种状态：空（Empty）、准备中（Preparing）、等待分配
// （AwaitingAssignment）与稳定（Stable）。时钟由调用方以毫秒传入，
// 相同的调用序列重放得到完全相同的状态序列、领导者与代数。
package group

import (
	"sort"
	"sync"
)

// State 是组的状态。
type State int

const (
	// StateEmpty 组内没有任何成员。
	StateEmpty State = iota
	// StatePreparing 再均衡准备中，等待已知成员在本轮重新加入。
	StatePreparing
	// StateAwaitingAssignment 本轮加入完成，等待领导者提交分配表。
	StateAwaitingAssignment
	// StateStable 分配表已提交，成员可查询自己的分配。
	StateStable
)

func (s State) String() string {
	switch s {
	case StateEmpty:
		return "Empty"
	case StatePreparing:
		return "Preparing"
	case StateAwaitingAssignment:
		return "AwaitingAssignment"
	case StateStable:
		return "Stable"
	default:
		return "Unknown"
	}
}

// Assignment 是分配表：成员 ID -> 该成员的分区列表。
type Assignment map[string][]string

// Coordinator 是消费组协调者，所有方法可并发调用，
// 结果等价于某个串行顺序。
type Coordinator struct {
	mu         sync.Mutex
	timeoutMs  int64
	state      State
	generation int
	members    map[string]struct{}
	joinOrder  []string
	joined     map[string]struct{}
	leader     string
	startMs    int64
	assignment Assignment
	lastNow    int64
	hasNow     bool
}

// New 创建一个协调者，rebalanceTimeoutMs 为再均衡超时 T（毫秒），
// 初始代数为 0，初始状态为空。
func New(rebalanceTimeoutMs int64) *Coordinator {
	return &Coordinator{
		timeoutMs: rebalanceTimeoutMs,
		state:     StateEmpty,
		members:   make(map[string]struct{}),
		joined:    make(map[string]struct{}),
	}
}

// Join 处理成员加入。
//
// 空、稳定或等待分配状态下，新成员加入或已知成员再次加入使组进入
// 准备中并记开始时刻为 now；已处于准备中则开始时刻不变。准备中成员
// 首次加入记入本轮加入序，重复加入幂等且不改变其次序。若加入后所有
// 已知成员都已在本轮加入，则在本次调用内立即完成本轮再均衡。
func (c *Coordinator) Join(member string, nowMs int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.advanceClockLocked(nowMs); err != nil {
		return err
	}
	switch c.state {
	case StateEmpty, StateStable, StateAwaitingAssignment:
		c.state = StatePreparing
		c.startMs = nowMs
		c.assignment = nil
		c.joinOrder = nil
		c.joined = make(map[string]struct{})
		c.addJoinLocked(member)
	case StatePreparing:
		c.addJoinLocked(member)
	}
	c.maybeCompleteLocked()
	return nil
}

// Leave 处理成员离开。对未知成员幂等，不产生任何影响。
//
// 成员被移除；稳定或等待分配状态下组进入准备中，开始时刻为 now；
// 准备中移除后若剩余已知成员都已加入则立即完成；成员清空则进入空
// 状态，代数不变。准备中移除后若没有任何成员已加入本轮，则成员全部
// 移除、组进入空状态且代数不变。
func (c *Coordinator) Leave(member string, nowMs int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.advanceClockLocked(nowMs); err != nil {
		return err
	}
	if _, ok := c.members[member]; !ok {
		return nil
	}
	delete(c.members, member)
	c.removeJoinLocked(member)
	switch c.state {
	case StateStable, StateAwaitingAssignment:
		c.assignment = nil
		if len(c.members) == 0 {
			c.toEmptyLocked()
			return nil
		}
		c.state = StatePreparing
		c.startMs = nowMs
		c.joinOrder = nil
		c.joined = make(map[string]struct{})
	case StatePreparing:
		switch {
		case len(c.members) == 0:
			c.toEmptyLocked()
		case len(c.joinOrder) == 0:
			// 本轮已无任何加入，无法选出领导者，清空组。
			c.toEmptyLocked()
		default:
			c.maybeCompleteLocked()
		}
	}
	return nil
}

// Tick 推进时钟。准备中且 now 不小于开始时刻加 T 时本轮超时：
// 未加入的已知成员被移除；若本轮没有任何成员加入，则成员全部移除、
// 组进入空状态且代数不变，否则完成本轮再均衡。其他状态下为空操作。
func (c *Coordinator) Tick(nowMs int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.advanceClockLocked(nowMs); err != nil {
		return err
	}
	if c.state != StatePreparing || nowMs < c.startMs+c.timeoutMs {
		return nil
	}
	if len(c.joinOrder) == 0 {
		c.toEmptyLocked()
		return nil
	}
	members := make(map[string]struct{}, len(c.joinOrder))
	for _, m := range c.joinOrder {
		members[m] = struct{}{}
	}
	c.members = members
	c.completeLocked()
	return nil
}

// Sync 处理成员同步，返回该成员自己的分配。
//
// 依次检查未知成员、代数过期；准备中报需重新加入（空状态不可达，
// 因为空状态没有任何已知成员）。分配表为空表示仅查询：稳定状态返回
// 自己的分配；等待分配状态下无论是否领导者都返回尚未就绪。非空分配表
// 视为提交：等待分配状态下仅领导者可提交，且分配表的成员集合必须恰
// 等于本代成员集合，提交后状态变为稳定。
func (c *Coordinator) Sync(member string, generation int, assignment Assignment) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.members[member]; !ok {
		return nil, ErrUnknownMember
	}
	if generation != c.generation {
		return nil, ErrStaleGeneration
	}
	switch c.state {
	case StatePreparing:
		return nil, ErrRejoinNeeded
	case StateAwaitingAssignment:
		if len(assignment) == 0 {
			return nil, ErrNotReady
		}
		if member != c.leader {
			return nil, ErrNotLeader
		}
		if !sameMemberSet(assignment, c.members) {
			return nil, ErrAssignmentMismatch
		}
		c.assignment = cloneAssignment(assignment)
		c.state = StateStable
		return cloneSlice(c.assignment[member]), nil
	default: // StateStable（StateEmpty 不可达：成员已知则组非空）
		return cloneSlice(c.assignment[member]), nil
	}
}

// Heartbeat 处理成员心跳。按未知成员、代数不等、准备中的顺序检查，
// 只报告第一个错误；全部通过则返回 nil。
func (c *Coordinator) Heartbeat(member string, generation int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.members[member]; !ok {
		return ErrUnknownMember
	}
	if generation != c.generation {
		return ErrStaleGeneration
	}
	if c.state == StatePreparing {
		return ErrRejoinNeeded
	}
	return nil
}

// State 返回当前状态。
func (c *Coordinator) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Generation 返回当前代数（只增不减）。
func (c *Coordinator) Generation() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

// Leader 返回当前领导者，空串表示无。
func (c *Coordinator) Leader() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.leader
}

// Members 返回当前已知成员集合（按字典序排序的副本）。
func (c *Coordinator) Members() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.membersLocked()
}

// JoinOrder 返回本轮加入序的副本。
func (c *Coordinator) JoinOrder() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.joinOrder...)
}

func (c *Coordinator) membersLocked() []string {
	out := make([]string, 0, len(c.members))
	for m := range c.members {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// advanceClockLocked 校验并推进时钟。now 小于此前任一次调用传入的
// now 时报时钟倒退，且不改变任何状态。
func (c *Coordinator) advanceClockLocked(nowMs int64) error {
	if c.hasNow && nowMs < c.lastNow {
		return ErrClockBackwards
	}
	c.lastNow = nowMs
	c.hasNow = true
	return nil
}

// addJoinLocked 将成员记入已知成员集合与本轮加入序；重复加入幂等。
func (c *Coordinator) addJoinLocked(member string) {
	c.members[member] = struct{}{}
	if _, ok := c.joined[member]; ok {
		return
	}
	c.joined[member] = struct{}{}
	c.joinOrder = append(c.joinOrder, member)
}

// removeJoinLocked 将成员从本轮加入序中移除。
func (c *Coordinator) removeJoinLocked(member string) {
	if _, ok := c.joined[member]; !ok {
		return
	}
	delete(c.joined, member)
	for i, m := range c.joinOrder {
		if m == member {
			c.joinOrder = append(c.joinOrder[:i], c.joinOrder[i+1:]...)
			break
		}
	}
}

// maybeCompleteLocked 在所有已知成员都已在本轮加入时完成本轮再均衡。
func (c *Coordinator) maybeCompleteLocked() {
	if c.state != StatePreparing || len(c.members) == 0 {
		return
	}
	if len(c.joinOrder) != len(c.members) {
		return
	}
	c.completeLocked()
}

// completeLocked 完成本轮再均衡：代数加一，领导者为本轮加入序最早的
// 成员，状态变为等待分配。
func (c *Coordinator) completeLocked() {
	c.generation++
	c.leader = c.joinOrder[0]
	c.state = StateAwaitingAssignment
	c.assignment = nil
}

// toEmptyLocked 清空组：成员全部移除、进入空状态，代数不变。
func (c *Coordinator) toEmptyLocked() {
	c.state = StateEmpty
	c.members = make(map[string]struct{})
	c.joinOrder = nil
	c.joined = make(map[string]struct{})
	c.leader = ""
	c.assignment = nil
}

// sameMemberSet 报告分配表的成员集合是否恰等于已知成员集合。
func sameMemberSet(a Assignment, members map[string]struct{}) bool {
	if len(a) != len(members) {
		return false
	}
	for m := range a {
		if _, ok := members[m]; !ok {
			return false
		}
	}
	return true
}

func cloneAssignment(a Assignment) Assignment {
	out := make(Assignment, len(a))
	for m, partitions := range a {
		out[m] = cloneSlice(partitions)
	}
	return out
}

func cloneSlice(s []string) []string {
	return append([]string(nil), s...)
}
