package sticky

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// Op 是变更批中的单个操作。Join=true 表示加入，否则表示离开。
type Op struct {
	Member string
	Join   bool
}

// BatchResult 是一次成功提交的结果。
type BatchResult struct {
	Generation int
	Assignment map[string][]int
	// Migrations 为本批迁移数：批前有主且批后换主的分区个数。
	Migrations int
}

// Assignor 是线程安全的粘性分区分配器。
type Assignor struct {
	mu         sync.RWMutex
	partitions assignment
	members    map[string]struct{}
	generation int
	maxMembers int
}

// NewAssignor 创建分配器。partitions 为分区总数，编号 0..partitions-1；
// maxMembers 为成员数上限，<=0 表示不限制。
func NewAssignor(partitions, maxMembers int) *Assignor {
	if partitions < 0 {
		partitions = 0
	}
	return &Assignor{
		partitions: make(assignment, partitions),
		members:    make(map[string]struct{}),
		maxMembers: maxMembers,
	}
}

// Apply 以原子批方式提交变更并执行一次重分配；失败不留痕。
func (a *Assignor) Apply(ctx context.Context, ops []Op) (BatchResult, error) {
	if err := ctx.Err(); err != nil {
		return BatchResult{}, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.validate(ops); err != nil {
		return BatchResult{}, err
	}

	changed := false
	for _, op := range ops {
		if op.Join {
			if _, exists := a.members[op.Member]; !exists {
				a.members[op.Member] = struct{}{}
				changed = true
			}
		} else {
			if _, exists := a.members[op.Member]; exists {
				delete(a.members, op.Member)
				changed = true
			}
		}
	}

	if !changed {
		return BatchResult{
			Generation: a.generation,
			Assignment: a.snapshotLocked(),
		}, nil
	}

	next, migrations := rebalance(a.partitions, a.members)
	a.partitions = next
	a.generation++

	return BatchResult{
		Generation: a.generation,
		Assignment: a.snapshotLocked(),
		Migrations: migrations,
	}, nil
}

// Assignment 返回当前每个成员持有的分区列表（按编号升序）的快照。
func (a *Assignor) Assignment() map[string][]int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.snapshotLocked()
}

// Generation 返回当前代数。
func (a *Assignor) Generation() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.generation
}

// Check 执行自检：确定性持有者、成员不超限、持有数差不大于一。
func (a *Assignor) Check() error {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.maxMembers > 0 && len(a.members) > a.maxMembers {
		return ErrMemberLimit
	}

	counts := make(map[string]int, len(a.members))
	for _, owner := range a.partitions {
		if owner == "" {
			if len(a.members) > 0 {
				return errors.New("sticky: unassigned partition while members exist")
			}
			continue
		}
		if _, ok := a.members[owner]; !ok {
			return errors.New("sticky: partition owned by non-member")
		}
		counts[owner]++
	}

	if len(a.members) > 0 {
		min, max := len(a.partitions)+1, -1
		for id := range a.members {
			c := counts[id]
			if c < min {
				min = c
			}
			if c > max {
				max = c
			}
		}
		if max-min > 1 {
			return errors.New("sticky: partition counts differ by more than one")
		}
	}
	return nil
}

// validate 对整批做预检。任何错误都在状态变更之前返回，保证失败不留痕。
// 校验与批内顺序无关：以批前成员集合为准计算净效果。
func (a *Assignor) validate(ops []Op) error {
	leftInBatch := make(map[string]bool)
	joinCount := make(map[string]int)
	for i, op := range ops {
		if op.Join {
			if op.Member == "" {
				return &BatchError{Reason: ErrEmptyMemberID, Index: i, Member: op.Member}
			}
			joinCount[op.Member]++
		} else if op.Member != "" {
			leftInBatch[op.Member] = true
		}
	}

	// 构造批后候选成员集合（顺序无关的净效果）。
	candidate := make(map[string]struct{}, len(a.members))
	for id := range a.members {
		candidate[id] = struct{}{}
	}
	for _, op := range ops {
		if op.Join {
			candidate[op.Member] = struct{}{}
		} else {
			delete(candidate, op.Member)
		}
	}

	// 按批内位置顺序报告重复加入，保证错误位置确定：
	// 同一成员加入两次，或加入批前在组且批内未离开的成员。
	seenJoin := make(map[string]bool)
	for i, op := range ops {
		if !op.Join {
			continue
		}
		_, current := a.members[op.Member]
		switch {
		case joinCount[op.Member] > 1 && seenJoin[op.Member]:
			return &BatchError{Reason: ErrDuplicateJoin, Index: i, Member: op.Member}
		case current && !leftInBatch[op.Member]:
			return &BatchError{Reason: ErrDuplicateJoin, Index: i, Member: op.Member}
		}
		seenJoin[op.Member] = true
	}

	// 离开目标必须批前在组，或批内出现过加入（先加后离的净效果场景）。
	for i, op := range ops {
		if op.Join {
			continue
		}
		_, current := a.members[op.Member]
		if !current && joinCount[op.Member] == 0 {
			return &BatchError{Reason: ErrLeaveUnknown, Index: i, Member: op.Member}
		}
		if op.Member == "" {
			return &BatchError{Reason: ErrLeaveUnknown, Index: i, Member: op.Member}
		}
	}

	if a.maxMembers > 0 && len(candidate) > a.maxMembers {
		return &BatchError{Reason: ErrMemberLimit, Index: -1}
	}
	return nil
}

func (a *Assignor) snapshotLocked() map[string][]int {
	out := make(map[string][]int, len(a.members))
	for id := range a.members {
		out[id] = []int{}
	}
	for i, owner := range a.partitions {
		if owner != "" {
			out[owner] = append(out[owner], i)
		}
	}
	for id := range out {
		sort.Ints(out[id])
	}
	return out
}
