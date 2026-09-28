// Package rebalance 实现消费组的增量协作式再均衡（两轮协议）。
package rebalance

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
)

// State 表示分区的三种状态。
type State int

const (
	// Consuming 消费中：分区有唯一持有者，正在消费。
	Consuming State = iota
	// Revoking 撤销中：目标已改变，等待当前持有者确认撤销。
	Revoking
	// Unowned 无主：撤销已完成，等待全组无撤销中分区后被分配。
	Unowned
)

func (s State) String() string {
	switch s {
	case Consuming:
		return "Consuming"
	case Revoking:
		return "Revoking"
	case Unowned:
		return "Unowned"
	}
	return "Unknown"
}

// 错误类别：互不相同、可区分，调用方可用 errors.Is 判定。
var (
	// ErrInvalidArgument 非法参数（如负数成员号/分区号、空或重复的分区列表）。
	ErrInvalidArgument = errors.New("rebalance: invalid argument")
	// ErrDuplicateMember 重复加入：成员已存在。
	ErrDuplicateMember = errors.New("rebalance: duplicate member")
	// ErrMemberNotFound 成员不存在。
	ErrMemberNotFound = errors.New("rebalance: member not found")
	// ErrNoPendingRevocation 无待确认撤销：该成员没有撤销中的分区。
	ErrNoPendingRevocation = errors.New("rebalance: no pending revocation")
)

// PartitionView 是分区状态的只读快照。
type PartitionView struct {
	Partition int
	State     State
	Owner     int // 仅 Consuming/Revoking 时有效，Unowned 时为 -1
}

// Assignor 增量协作式再均衡器，所有方法可并发调用。
type Assignor struct {
	mu        sync.Mutex
	members   map[int]struct{}
	sorted    []int // 升序成员缓存，与 members 同步维护
	partition map[int]*partitionState
	order     []int // 升序分区号缓存
	logger    *log.Logger
}

type partitionState struct {
	state State
	owner int
}

// NewAssignor 以给定分区集合创建再均衡器；logger 为 nil 时不输出日志。
func NewAssignor(partitions []int, logger *log.Logger) (*Assignor, error) {
	if len(partitions) == 0 {
		return nil, fmt.Errorf("%w: partition list is empty", ErrInvalidArgument)
	}
	seen := make(map[int]struct{}, len(partitions))
	for _, p := range partitions {
		if p < 0 {
			return nil, fmt.Errorf("%w: negative partition %d", ErrInvalidArgument, p)
		}
		if _, dup := seen[p]; dup {
			return nil, fmt.Errorf("%w: duplicate partition %d", ErrInvalidArgument, p)
		}
		seen[p] = struct{}{}
	}
	a := &Assignor{
		members:   make(map[int]struct{}),
		partition: make(map[int]*partitionState, len(partitions)),
		logger:    logger,
	}
	for _, p := range partitions {
		a.partition[p] = &partitionState{state: Unowned, owner: -1}
		a.order = append(a.order, p)
	}
	sort.Ints(a.order)
	a.logf("init partitions=%v states=%s", a.order, a.statesLocked())
	return a, nil
}

// Join 成员加入，触发第一轮（撤销）。
func (a *Assignor) Join(member int) error {
	if member < 0 {
		return fmt.Errorf("%w: negative member %d", ErrInvalidArgument, member)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.members[member]; ok {
		return fmt.Errorf("%w: member %d already joined", ErrDuplicateMember, member)
	}
	a.logf("join input member=%d", member)
	a.members[member] = struct{}{}
	a.sorted = insertSorted(a.sorted, member)
	a.revokeChangedLocked("join")
	a.assignIfSettledLocked()
	a.logf("join done members=%v states=%s", a.sorted, a.statesLocked())
	return nil
}

// Leave 成员离开，触发第一轮（撤销）。
func (a *Assignor) Leave(member int) error {
	if member < 0 {
		return fmt.Errorf("%w: negative member %d", ErrInvalidArgument, member)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.members[member]; !ok {
		return fmt.Errorf("%w: member %d cannot leave", ErrMemberNotFound, member)
	}
	a.logf("leave input member=%d", member)
	delete(a.members, member)
	a.sorted = removeSorted(a.sorted, member)
	// 离开者的分区立即无主：持有者已不存在，无人可确认其撤销。
	for _, p := range a.order {
		ps := a.partition[p]
		if ps.owner == member {
			a.logf("leave: partition %d owner %d left -> Unowned", p, member)
			ps.state = Unowned
			ps.owner = -1
		}
	}
	a.revokeChangedLocked("leave")
	a.assignIfSettledLocked()
	a.logf("leave done members=%v states=%s", a.sorted, a.statesLocked())
	return nil
}

// Confirm 成员确认其全部撤销中分区，触发第二轮（分配）。
func (a *Assignor) Confirm(member int) error {
	if member < 0 {
		return fmt.Errorf("%w: negative member %d", ErrInvalidArgument, member)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.members[member]; !ok {
		return fmt.Errorf("%w: member %d cannot confirm", ErrMemberNotFound, member)
	}
	pending := 0
	for _, p := range a.order {
		if ps := a.partition[p]; ps.state == Revoking && ps.owner == member {
			pending++
		}
	}
	if pending == 0 {
		return fmt.Errorf("%w: member %d", ErrNoPendingRevocation, member)
	}
	a.logf("confirm input member=%d pending=%d", member, pending)
	for _, p := range a.order {
		ps := a.partition[p]
		if ps.state == Revoking && ps.owner == member {
			a.logf("confirm: partition %d revoked by %d -> Unowned", p, member)
			ps.state = Unowned
			ps.owner = -1
		}
	}
	a.assignIfSettledLocked()
	a.logf("confirm done members=%v states=%s", a.sorted, a.statesLocked())
	return nil
}

// Snapshot 返回所有分区状态的确定有序快照。
func (a *Assignor) Snapshot() []PartitionView {
	a.mu.Lock()
	defer a.mu.Unlock()
	views := make([]PartitionView, 0, len(a.order))
	for _, p := range a.order {
		ps := a.partition[p]
		views = append(views, PartitionView{Partition: p, State: ps.state, Owner: ps.owner})
	}
	return views
}

// Members 返回升序成员列表快照。
func (a *Assignor) Members() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int(nil), a.sorted...)
}

// targetLocked 计算分区当前目标成员，成员为空返回 -1。调用方须持锁。
func (a *Assignor) targetLocked(partition int) int {
	for _, m := range a.sorted {
		if m >= partition {
			return m
		}
	}
	if len(a.sorted) == 0 {
		return -1
	}
	return a.sorted[0]
}

// revokeChangedLocked 第一轮：目标改变的消费中分区进入撤销中；
// 已撤销中的分区不因目标再变而取消。调用方须持锁。
func (a *Assignor) revokeChangedLocked(reason string) {
	for _, p := range a.order {
		ps := a.partition[p]
		if ps.state != Consuming {
			continue
		}
		target := a.targetLocked(p)
		if target != ps.owner {
			a.logf("%s: partition %d target %d != owner %d -> Revoking", reason, p, target, ps.owner)
			ps.state = Revoking
		} else {
			a.logf("%s: partition %d target %d == owner %d, keep Consuming", reason, p, target, ps.owner)
		}
	}
}

// assignIfSettledLocked 第二轮：全组无撤销中分区时，把无主分区
// 分配给当前目标并置为消费中；成员为空则不分配。调用方须持锁。
func (a *Assignor) assignIfSettledLocked() {
	for _, p := range a.order {
		if a.partition[p].state == Revoking {
			a.logf("assign: partition %d still Revoking, skip round 2", p)
			return
		}
	}
	if len(a.sorted) == 0 {
		a.logf("assign: no members, skip round 2")
		return
	}
	for _, p := range a.order {
		ps := a.partition[p]
		if ps.state != Unowned {
			continue
		}
		target := a.targetLocked(p)
		a.logf("assign: partition %d Unowned -> owner %d Consuming", p, target)
		ps.state = Consuming
		ps.owner = target
	}
}

func (a *Assignor) statesLocked() string {
	s := ""
	for _, p := range a.order {
		ps := a.partition[p]
		s += fmt.Sprintf(" [%d]=%s(%d)", p, ps.state, ps.owner)
	}
	return s
}

func (a *Assignor) logf(format string, args ...interface{}) {
	if a.logger != nil {
		a.logger.Printf(format, args...)
	}
}

func insertSorted(s []int, v int) []int {
	i := sort.SearchInts(s, v)
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func removeSorted(s []int, v int) []int {
	i := sort.SearchInts(s, v)
	if i < len(s) && s[i] == v {
		return append(s[:i], s[i+1:]...)
	}
	return s
}

// NaiveTarget 按朴素规则计算分区目标成员：
// 不小于分区号的成员取最小者，否则取最小成员环形回绕；成员为空返回 -1。
func NaiveTarget(members []int, partition int) int {
	if len(members) == 0 {
		return -1
	}
	sorted := append([]int(nil), members...)
	sort.Ints(sorted)
	for _, m := range sorted {
		if m >= partition {
			return m
		}
	}
	return sorted[0]
}
