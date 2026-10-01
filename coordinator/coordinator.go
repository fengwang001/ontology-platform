// Package coordinator 实现一个消费组协调者的加入与同步状态机。
//
// 状态机包含四种状态：空（Empty）、准备中（Preparing）、
// 等待分配（AwaitingSync）与稳定（Stable）。时钟由调用方以
// 毫秒时间戳的形式传入，所有操作均可并发调用，效果等价于
// 某个串行顺序。
package coordinator

import (
	"errors"
	"slices"
	"sync"
)

// State 表示消费组的状态。
type State int

const (
	// StateEmpty 组内没有任何成员。
	StateEmpty State = iota
	// StatePreparing 再均衡准备中，等待已知成员在本轮加入。
	StatePreparing
	// StateAwaitingSync 本轮已集齐，等待领导者提交分配表。
	StateAwaitingSync
	// StateStable 分配表已提交，组处于稳定状态。
	StateStable
)

// String 返回状态的可读名称。
func (s State) String() string {
	switch s {
	case StateEmpty:
		return "Empty"
	case StatePreparing:
		return "Preparing"
	case StateAwaitingSync:
		return "AwaitingSync"
	case StateStable:
		return "Stable"
	default:
		return "Unknown"
	}
}

// 可被 errors.Is 区分的拒绝原因。
var (
	// ErrClockBackwards 时钟倒退：now 小于此前已接受的最大时刻。
	ErrClockBackwards = errors.New("coordinator: clock moved backwards")
	// ErrUnknownMember 未知成员。
	ErrUnknownMember = errors.New("coordinator: unknown member")
	// ErrStaleGeneration 代数过期：与当前代数不等。
	ErrStaleGeneration = errors.New("coordinator: stale generation")
	// ErrRejoinNeeded 准备中，需要重新加入。
	ErrRejoinNeeded = errors.New("coordinator: rebalance in progress, rejoin needed")
	// ErrNotReady 分配尚未就绪。
	ErrNotReady = errors.New("coordinator: assignment not ready")
	// ErrNotLeader 非领导者提交分配表。
	ErrNotLeader = errors.New("coordinator: non-leader assignment submission")
	// ErrAssignmentMismatch 分配表成员集与当前成员集不符。
	ErrAssignmentMismatch = errors.New("coordinator: assignment member set mismatch")
)

// Assignment 是成员 ID 到其分区列表的分配表。
type Assignment map[string][]string

// Coordinator 是消费组协调者，所有方法可并发调用。
type Coordinator struct {
	mu sync.Mutex

	timeoutMs int64

	state      State
	generation int
	leader     string

	members   map[string]bool // 已知成员（含上一代存活成员）
	joinOrder []string        // 本轮加入序
	joined    map[string]bool // 本轮已加入集合，与 joinOrder 对应

	assignment Assignment // 稳定状态下生效的分配表

	startMs int64 // 本轮准备中状态的开始时刻
	maxMs   int64 // 已接受的最大时刻（高水位）
	hasMs   bool  // 是否已接受过任何时刻
}

// New 创建一个协调者，rebalanceTimeoutMs 为再均衡超时 T（毫秒），
// 初始状态为空，初始代数为 0。
func New(rebalanceTimeoutMs int64) *Coordinator {
	return &Coordinator{
		timeoutMs: rebalanceTimeoutMs,
		state:     StateEmpty,
		members:   make(map[string]bool),
		joined:    make(map[string]bool),
	}
}

// Join 处理成员加入。
//
// 空、稳定或等待分配状态下，新成员加入或已知成员再次加入都会
// 开启新一轮再均衡：进入准备中并记开始时刻为 now；已处于准备中
// 则开始时刻不变。准备中成员首次加入记入本轮加入序，重复加入幂等。
// 加入后若所有已知成员都已在本轮加入，则在本次调用内立即完成本轮。
func (c *Coordinator) Join(memberID string, nowMs int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClockLocked(nowMs); err != nil {
		return err
	}
	if c.state != StatePreparing {
		// 空、稳定或等待分配：开启新一轮。
		c.state = StatePreparing
		c.startMs = nowMs
		c.joinOrder = nil
		clear(c.joined)
	}
	if !c.joined[memberID] {
		c.members[memberID] = true
		c.joined[memberID] = true
		c.joinOrder = append(c.joinOrder, memberID)
	}
	// 所有已知成员（含上一代存活成员）都已在本轮加入则立即完成。
	if len(c.joinOrder) == len(c.members) {
		c.completeLocked()
	}
	return nil
}

// Leave 处理成员离开。
//
// 成员被移除；稳定或等待分配状态下组进入准备中并记开始时刻为 now；
// 准备中移除后若剩余已知成员都已在本轮加入则立即完成；成员清空则
// 进入空状态且代数不变。准备中移除后若没有任何成员已加入本轮，则
// 成员全部移除、组进入空状态且代数不变。
func (c *Coordinator) Leave(memberID string, nowMs int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClockLocked(nowMs); err != nil {
		return err
	}
	if !c.members[memberID] {
		return ErrUnknownMember
	}
	wasPreparing := c.state == StatePreparing
	c.removeMemberLocked(memberID)
	switch {
	case len(c.members) == 0:
		// 成员清空：进入空状态，代数不变。
		c.resetRoundLocked()
		c.state = StateEmpty
	case wasPreparing && len(c.joinOrder) == 0:
		// 准备中离开后没有任何成员已加入本轮：成员全部移除，
		// 组进入空状态，代数不变。
		clear(c.members)
		c.resetRoundLocked()
		c.state = StateEmpty
	case wasPreparing:
		// 准备中移除后若剩余已知成员都已加入则立即完成。
		if len(c.joinOrder) == len(c.members) {
			c.completeLocked()
		}
	case c.state == StateStable || c.state == StateAwaitingSync:
		// 稳定或等待分配：开启新一轮再均衡。
		c.state = StatePreparing
		c.startMs = nowMs
		c.resetRoundLocked()
	}
	return nil
}

// Tick 推进时钟，在准备中状态下检查再均衡超时。
//
// 推进时刻不小于开始时刻加 T 时本轮完成：未加入的已知成员被移除，
// 若没有任何成员已加入本轮，则成员全部移除、组进入空状态且代数不变。
func (c *Coordinator) Tick(nowMs int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClockLocked(nowMs); err != nil {
		return err
	}
	if c.state != StatePreparing || nowMs < c.startMs+c.timeoutMs {
		return nil
	}
	// 超时：移除未在本轮加入的已知成员。
	for id := range c.members {
		if !c.joined[id] {
			delete(c.members, id)
		}
	}
	if len(c.joinOrder) == 0 {
		// 没有任何成员已加入本轮：成员全部移除，进入空状态，代数不变。
		clear(c.members)
		c.resetRoundLocked()
		c.state = StateEmpty
		return nil
	}
	c.completeLocked()
	return nil
}

// Sync 处理成员同步：提交分配表（仅领导者）或查询自己的分配。
//
// 依次检查未知成员、代数过期。准备中报需重新加入。等待分配状态下：
// 分配表为空表示仅查询，返回尚未就绪；非领导者提交非空分配表报非
// 领导者提交；领导者提交时分配表成员集合必须恰等于本代成员集合，
// 提交后状态变为稳定。稳定状态下返回成员自己的分配。
func (c *Coordinator) Sync(memberID string, generation int, submit Assignment) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.members[memberID] {
		return nil, ErrUnknownMember
	}
	if generation != c.generation {
		return nil, ErrStaleGeneration
	}
	switch c.state {
	case StatePreparing:
		return nil, ErrRejoinNeeded
	case StateAwaitingSync:
		if len(submit) == 0 {
			// 仅查询：领导者与非领导者都尚未就绪。
			return nil, ErrNotReady
		}
		if memberID != c.leader {
			return nil, ErrNotLeader
		}
		if !sameMemberSetLocked(submit, c.members) {
			return nil, ErrAssignmentMismatch
		}
		c.assignment = copyAssignment(submit)
		c.state = StateStable
		return c.assignment[memberID], nil
	case StateStable:
		return c.assignment[memberID], nil
	default:
		// 空状态下不存在已知成员，上文已报未知成员，不可达。
		return nil, ErrUnknownMember
	}
}

// Heartbeat 处理成员心跳。
//
// 按未知成员、代数不等、准备中的顺序检查，只报第一个。
func (c *Coordinator) Heartbeat(memberID string, generation int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.members[memberID] {
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

// Generation 返回当前代数。
func (c *Coordinator) Generation() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

// Leader 返回当前领导者，未知时返回空串。
func (c *Coordinator) Leader() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.leader
}

// Members 返回当前已知成员集合（按字典序排序，保证确定性）。
func (c *Coordinator) Members() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sortedMembersLocked()
}

// JoinOrder 返回本轮加入序的副本。
func (c *Coordinator) JoinOrder() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.joinOrder...)
}

func (c *Coordinator) sortedMembersLocked() []string {
	ids := make([]string, 0, len(c.members))
	for id := range c.members {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// checkClockLocked 校验调用方时钟：now 小于此前已接受的最大时刻
// 时报时钟倒退且不改变任何状态；通过后推进高水位。
func (c *Coordinator) checkClockLocked(nowMs int64) error {
	if c.hasMs && nowMs < c.maxMs {
		return ErrClockBackwards
	}
	c.maxMs = nowMs
	c.hasMs = true
	return nil
}

// completeLocked 完成本轮再均衡：代数加一，领导者为本轮加入序
// 最早的成员，状态变为等待分配。调用前须保证加入序非空。
func (c *Coordinator) completeLocked() {
	c.generation++
	c.leader = c.joinOrder[0]
	c.assignment = nil
	c.state = StateAwaitingSync
}

// resetRoundLocked 清空本轮加入序与已加入集合。
func (c *Coordinator) resetRoundLocked() {
	c.joinOrder = nil
	clear(c.joined)
}

// removeMemberLocked 移除已知成员及其本轮加入记录。
func (c *Coordinator) removeMemberLocked(memberID string) {
	delete(c.members, memberID)
	if c.joined[memberID] {
		delete(c.joined, memberID)
		c.joinOrder = slices.DeleteFunc(c.joinOrder, func(id string) bool {
			return id == memberID
		})
	}
}

// sameMemberSetLocked 判断分配表成员集合是否恰等于已知成员集合。
func sameMemberSetLocked(a Assignment, members map[string]bool) bool {
	if len(a) != len(members) {
		return false
	}
	for id := range a {
		if !members[id] {
			return false
		}
	}
	return true
}

// copyAssignment 深拷贝分配表，避免调用方后续修改影响内部状态。
func copyAssignment(a Assignment) Assignment {
	out := make(Assignment, len(a))
	for id, partitions := range a {
		out[id] = append([]string(nil), partitions...)
	}
	return out
}
