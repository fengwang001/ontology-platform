// Package group 实现消费组的粘性分区分配（sticky partition assignment）。
package group

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"sync"
)

// MemberID 是成员标识，空串非法。
type MemberID string

// ChangeType 标识批内变更的种类。
type ChangeType int

const (
	Join  ChangeType = iota + 1 // 成员加入
	Leave                       // 成员离开
)

// Change 是批中的一条变更。
type Change struct {
	Type   ChangeType
	Member MemberID
}

// Result 是一次成功重分配后的结果快照。
type Result struct {
	Generation int64
	Joined     []MemberID
	Left       []MemberID
	Migrations int
	OwnerOf    []MemberID
	Assignment map[MemberID][]int32
}

var (
	ErrEmptyMemberID     = errors.New("group: empty member id")
	ErrDuplicateJoin     = errors.New("group: duplicate join")
	ErrMemberNotFound    = errors.New("group: member not found")
	ErrConflictingChange = errors.New("group: member joins and leaves in same batch")
	ErrTooManyMembers    = errors.New("group: member count exceeds limit")
	ErrUnknownChangeType = errors.New("group: unknown change type")
	ErrInvalidConfig     = errors.New("group: invalid config")
)

const DefaultMaxMembers = 64

// Group 是线程安全的消费组分区分配器。
type Group struct {
	mu sync.RWMutex

	partitionCount int
	maxMembers     int
	logger         *slog.Logger

	generation int64
	members    map[MemberID]struct{}
	ownerOf    []MemberID
	held       map[MemberID][]int32
}

// New 创建一个所有分区均无主、代数为 0 的消费组。
func New(partitionCount, maxMembers int, logger *slog.Logger) (*Group, error) {
	if partitionCount < 0 {
		return nil, fmt.Errorf("%w: partition count %d", ErrInvalidConfig, partitionCount)
	}
	if maxMembers <= 0 {
		maxMembers = DefaultMaxMembers
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Group{
		partitionCount: partitionCount,
		maxMembers:     maxMembers,
		logger:         logger,
		members:        map[MemberID]struct{}{},
		ownerOf:        make([]MemberID, partitionCount),
		held:           map[MemberID][]int32{},
	}, nil
}

// Apply 原子地提交一批变更并只执行一次重分配。
func (g *Group) Apply(changes []Change) (Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.logger.Info("apply batch: received input",
		slog.Int64("generation_before", g.generation),
		slog.Any("changes", changes),
		slog.Any("members_before", g.sortedMembersLocked()),
	)

	joined, left, newMembers, err := g.validateLocked(changes)
	if err != nil {
		g.logger.Info("apply batch: rejected, state unchanged",
			slog.Int64("generation", g.generation),
			slog.String("reason", err.Error()),
		)
		return Result{}, err
	}

	newOwnerOf, newHeld, migrations := g.rebalanceLocked(newMembers, left)

	g.members = newMembers
	g.ownerOf = newOwnerOf
	g.held = newHeld
	g.generation++

	res := Result{
		Generation: g.generation,
		Joined:     joined,
		Left:       left,
		Migrations: migrations,
		OwnerOf:    slices.Clone(newOwnerOf),
		Assignment: cloneHeld(newHeld),
	}
	g.logger.Info("apply batch: committed",
		slog.Int64("generation", res.Generation),
		slog.Any("joined", res.Joined),
		slog.Any("left", res.Left),
		slog.Int("migrations", res.Migrations),
		slog.Any("assignment", res.Assignment),
	)
	return res, nil
}

// validateLocked 校验整批变更，返回批后成员集合，不修改任何状态。
func (g *Group) validateLocked(changes []Change) (joined, left []MemberID, newMembers map[MemberID]struct{}, err error) {
	joinSet := map[MemberID]struct{}{}
	leaveSet := map[MemberID]struct{}{}
	joinCount := map[MemberID]int{}
	leaveCount := map[MemberID]int{}

	for _, c := range changes {
		if c.Member == "" {
			return nil, nil, nil, ErrEmptyMemberID
		}
		switch c.Type {
		case Join:
			joinSet[c.Member] = struct{}{}
			joinCount[c.Member]++
		case Leave:
			leaveSet[c.Member] = struct{}{}
			leaveCount[c.Member]++
		default:
			return nil, nil, nil, fmt.Errorf("%w: type=%d member=%q", ErrUnknownChangeType, c.Type, c.Member)
		}
	}

	// 顺序无关的固定错误优先级：同批既加入又离开 > 重复加入 > 离开不存在。
	conflict := []MemberID{}
	for m := range joinSet {
		if _, ok := leaveSet[m]; ok {
			conflict = append(conflict, m)
		}
	}
	if len(conflict) > 0 {
		sort.Slice(conflict, func(i, j int) bool { return conflict[i] < conflict[j] })
		return nil, nil, nil, fmt.Errorf("%w: %q joins and leaves in same batch", ErrConflictingChange, conflict[0])
	}
	dupJoin := []MemberID{}
	for m, n := range joinCount {
		if n > 1 {
			dupJoin = append(dupJoin, m)
			continue
		}
		if _, ok := g.members[m]; ok {
			dupJoin = append(dupJoin, m)
		}
	}
	if len(dupJoin) > 0 {
		sort.Slice(dupJoin, func(i, j int) bool { return dupJoin[i] < dupJoin[j] })
		m := dupJoin[0]
		if _, inGroup := g.members[m]; inGroup {
			return nil, nil, nil, fmt.Errorf("%w: %q already in group", ErrDuplicateJoin, m)
		}
		return nil, nil, nil, fmt.Errorf("%w: %q appears twice in batch", ErrDuplicateJoin, m)
	}
	missing := []MemberID{}
	for m, n := range leaveCount {
		if n > 1 {
			missing = append(missing, m)
			continue
		}
		if _, ok := g.members[m]; !ok {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
		m := missing[0]
		if leaveCount[m] > 1 {
			return nil, nil, nil, fmt.Errorf("%w: %q appears twice in batch", ErrMemberNotFound, m)
		}
		return nil, nil, nil, fmt.Errorf("%w: %q not in group", ErrMemberNotFound, m)
	}

	newMembers = maps.Clone(g.members)
	for m := range joinSet {
		newMembers[m] = struct{}{}
		joined = append(joined, m)
	}
	for m := range leaveSet {
		delete(newMembers, m)
		left = append(left, m)
	}
	sort.Slice(joined, func(i, j int) bool { return joined[i] < joined[j] })
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })

	if len(newMembers) > g.maxMembers {
		return nil, nil, nil, fmt.Errorf("%w: %d > %d", ErrTooManyMembers, len(newMembers), g.maxMembers)
	}
	return joined, left, newMembers, nil
}

// rebalanceLocked 依据批后成员集合执行唯一一次重分配，返回新属主表、新持有表与迁移数。
func (g *Group) rebalanceLocked(newMembers map[MemberID]struct{}, left []MemberID) ([]MemberID, map[MemberID][]int32, int) {
	n := g.partitionCount
	members := make([]MemberID, 0, len(newMembers))
	for m := range newMembers {
		members = append(members, m)
	}
	sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })

	leavers := map[MemberID]struct{}{}
	for _, m := range left {
		leavers[m] = struct{}{}
	}

	// 工作集：留存成员复制批前持有；新成员为空。
	held := make(map[MemberID][]int32, len(members))
	for _, m := range members {
		held[m] = slices.Clone(g.held[m])
	}

	// 无主分区池：原本无主、或原属主本批离开的分区。
	pool := make([]bool, n)
	for p := 0; p < n; p++ {
		owner := g.ownerOf[p]
		if owner == "" {
			pool[p] = true
		} else if _, gone := leavers[owner]; gone {
			pool[p] = true
		}
	}

	// 配额：整除得到基础配额；余数名额按批前持有数降序、并列按标识升序分配。
	m := len(members)
	quota := make(map[MemberID]int, m)
	if m > 0 {
		base, rem := n/m, n%m
		for _, member := range members {
			quota[member] = base
		}
		remainderOrder := slices.Clone(members)
		sort.SliceStable(remainderOrder, func(i, j int) bool {
			a, b := remainderOrder[i], remainderOrder[j]
			ha, hb := len(g.held[a]), len(g.held[b])
			if ha != hb {
				return ha > hb
			}
			return a < b
		})
		for i := 0; i < rem; i++ {
			quota[remainderOrder[i]] = base + 1
		}
		g.logger.Info("rebalance: quota decided",
			slog.Int("partitions", n),
			slog.Int("members", m),
			slog.Int("base", base),
			slog.Int("remainder", rem),
			slog.Any("remainder_order", remainderOrder),
			slog.Any("quota", quota),
		)
	}

	// 释放：超配额成员按分区编号从大到小释放超出部分（held 始终升序）。
	for _, member := range members {
		excess := len(held[member]) - quota[member]
		if excess <= 0 {
			continue
		}
		released := make([]int32, 0, excess)
		for i := 0; i < excess; i++ {
			p := held[member][len(held[member])-1-i]
			pool[p] = true
			released = append(released, p)
		}
		held[member] = held[member][:len(held[member])-excess]
		g.logger.Info("rebalance: over-quota release",
			slog.String("member", string(member)),
			slog.Int("quota", quota[member]),
			slog.Any("released_desc", released),
			slog.Any("kept", held[member]),
		)
	}

	// 补齐：无主分区按编号升序，逐个交给当前配额缺口最大者，并列取标识最小者。
	pooled := make([]int32, 0, n)
	for p := 0; p < n; p++ {
		if pool[p] {
			pooled = append(pooled, int32(p))
		}
	}
	for _, p := range pooled {
		var best MemberID
		bestGap := -1
		if m == 0 {
			g.logger.Info("rebalance: no members, partition stays unassigned",
				slog.Int("partition", int(p)),
			)
			continue
		}
		for _, member := range members {
			gap := quota[member] - len(held[member])
			if gap > bestGap {
				best, bestGap = member, gap
			}
		}
		held[best] = append(held[best], p)
		g.logger.Info("rebalance: fill unassigned partition",
			slog.Int("partition", int(p)),
			slog.String("to", string(best)),
			slog.Int("gap_before", bestGap),
			slog.Int("held_after", len(held[best])),
		)
	}

	newOwnerOf := make([]MemberID, n)
	for member, parts := range held {
		sort.Slice(parts, func(i, j int) bool { return parts[i] < parts[j] })
		held[member] = parts
		for _, p := range parts {
			newOwnerOf[p] = member
		}
	}

	// 迁移数：批前有主且批后换主（变为无主也算换主）。
	migrations := 0
	moved := make([]int32, 0)
	for p := 0; p < n; p++ {
		before := g.ownerOf[p]
		if before != "" && before != newOwnerOf[p] {
			migrations++
			moved = append(moved, int32(p))
		}
	}
	g.logger.Info("rebalance: migration counted",
		slog.Any("moved_partitions", moved),
		slog.Int("migrations", migrations),
	)
	return newOwnerOf, held, migrations
}

func (g *Group) sortedMembersLocked() []MemberID {
	out := make([]MemberID, 0, len(g.members))
	for m := range g.members {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func cloneHeld(in map[MemberID][]int32) map[MemberID][]int32 {
	out := make(map[MemberID][]int32, len(in))
	for m, parts := range in {
		out[m] = slices.Clone(parts)
	}
	return out
}

// Assignment 返回批后分配的深拷贝。
func (g *Group) Assignment() map[MemberID][]int32 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return cloneHeld(g.held)
}

// Generation 返回当前代数。
func (g *Group) Generation() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.generation
}

// Members 返回当前成员标识（升序）。
func (g *Group) Members() []MemberID {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.sortedMembersLocked()
}

// Owner 返回指定分区的当前属主；无主时 ok 为 false。
func (g *Group) Owner(partition int32) (MemberID, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if int(partition) < 0 || int(partition) >= g.partitionCount {
		return "", false
	}
	owner := g.ownerOf[partition]
	return owner, owner != ""
}

// OwnerOf 返回所有分区属主的拷贝。
func (g *Group) OwnerOf() []MemberID {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return slices.Clone(g.ownerOf)
}

// Snapshot 返回当前状态的只读深拷贝。
func (g *Group) Snapshot() Result {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return Result{
		Generation: g.generation,
		OwnerOf:    slices.Clone(g.ownerOf),
		Assignment: cloneHeld(g.held),
	}
}

// SelfCheck 校验内部不变量。
func (g *Group) SelfCheck() error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.ownerOf) != g.partitionCount {
		return fmt.Errorf("self-check: owner table length %d != partitions %d", len(g.ownerOf), g.partitionCount)
	}
	seen := make([]MemberID, g.partitionCount)
	counts := make(map[MemberID]int, len(g.members))
	for m := range g.members {
		counts[m] = 0
	}
	for m, parts := range g.held {
		if _, ok := g.members[m]; !ok {
			return fmt.Errorf("self-check: partitions held by non-member %q", m)
		}
		for _, p := range parts {
			if int(p) < 0 || int(p) >= g.partitionCount {
				return fmt.Errorf("self-check: member %q holds out-of-range partition %d", m, p)
			}
			if seen[p] != "" {
				return fmt.Errorf("self-check: partition %d held by both %q and %q", p, seen[p], m)
			}
			seen[p] = m
			counts[m]++
		}
	}
	for p, owner := range g.ownerOf {
		if owner != seen[p] {
			return fmt.Errorf("self-check: ownerOf[%d]=%q disagrees with held %q", p, owner, seen[p])
		}
		if owner != "" {
			if _, ok := g.members[owner]; !ok {
				return fmt.Errorf("self-check: partition %d owned by non-member %q", p, owner)
			}
		}
	}
	if len(g.members) == 0 {
		for _, owner := range g.ownerOf {
			if owner != "" {
				return fmt.Errorf("self-check: empty group but partition owned by %q", owner)
			}
		}
		return nil
	}
	for p, owner := range g.ownerOf {
		if owner == "" {
			return fmt.Errorf("self-check: partition %d unassigned while group non-empty", p)
		}
	}
	min, max := g.partitionCount+1, -1
	for _, c := range counts {
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	if max-min > 1 {
		return fmt.Errorf("self-check: held counts unbalanced: min=%d max=%d", min, max)
	}
	return nil
}
