package scrub

import (
	"sort"
	"sync"
)

// blockState 是单个块在内存中的全部状态。
type blockState struct {
	replicas      map[int]*Replica // 节点 -> 副本；节点编号各不相同
	minInterval   Time
	lastPatrol    Time
	neverScrubbed bool
}

func (b *blockState) orderedReplicas() []Replica {
	out := make([]Replica, 0, len(b.replicas))
	for _, r := range b.replicas {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

// dueAt 返回该块在到期索引中的下一次到期时刻。
// 从未巡检的块到期时刻为逻辑时间原点，天然排在最前。
func (b *blockState) dueAt() Time {
	if b.neverScrubbed {
		return 0
	}
	return b.lastPatrol + b.minInterval
}

// Store 是多副本块存储的巡检仲裁服务。所有方法可并发调用，
// 单一互斥锁串行化全部操作，因此结果严格等价于某个全局串行顺序；
// 巡检仲裁与修复写在同一个临界区内完成，并发的新版本写入要么
// 完全在仲裁之前生效、要么完全在之后，绝不会被看到一半。
type Store struct {
	mu     sync.Mutex
	clock  Time
	blocks map[int]*blockState
	due    *dueIndex
	alerts []Alert
}

// NewStore 创建空服务，时钟从 0 开始（尚无被接受操作）。
func NewStore() *Store {
	return &Store{blocks: make(map[int]*blockState), due: newDueIndex()}
}

// checkClock 校验逻辑时钟并在通过后推进。调用方须持有锁。
func (s *Store) checkClock(at Time) error {
	if at < s.clock {
		return ErrClockBack
	}
	return nil
}

func (s *Store) acceptTime(at Time) { s.clock = at }

// validateReplicaSpec 校验 2..5 个不同节点、版本为正、摘要非空。
func validateReplicaSpec(replicas []Replica) error {
	n := len(replicas)
	if n < 2 || n > 5 {
		return ErrInvalid
	}
	seen := make(map[int]bool, n)
	for _, r := range replicas {
		if r.Version <= 0 || seen[r.Node] {
			return ErrInvalid
		}
		seen[r.Node] = true
	}
	return nil
}

// CreateBlock 以 2..5 个不同节点的初始副本创建块。
func (s *Store) CreateBlock(at Time, blockID int, replicas []Replica, minInterval Time) error {
	if minInterval < 0 {
		return ErrInvalid
	}
	if err := validateReplicaSpec(replicas); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	if s.blocks[blockID] != nil {
		return ErrInvalid
	}
	b := &blockState{
		replicas:      make(map[int]*Replica, len(replicas)),
		minInterval:   minInterval,
		neverScrubbed: true,
	}
	for i := range replicas {
		r := replicas[i]
		b.replicas[r.Node] = &r
	}
	s.blocks[blockID] = b
	s.due.upsert(dueEntry{dueAt: b.dueAt(), block: blockID})
	s.acceptTime(at)
	return nil
}

// Write 向若干节点写入同一新版本；副本不存在则在该节点重新创建。
// 不要求写全部节点，也不校验“至少法定数”——提交与否由版本仲裁推断。
func (s *Store) Write(at Time, blockID int, version int64, digest string, nodes []int) error {
	if version <= 0 || len(nodes) == 0 || digest == "" {
		return ErrInvalid
	}
	uniq := make(map[int]bool, len(nodes))
	for _, n := range nodes {
		if uniq[n] {
			return ErrInvalid
		}
		uniq[n] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	b := s.blocks[blockID]
	if b == nil {
		return ErrNotFound
	}
	for _, node := range nodes {
		b.replicas[node] = &Replica{
			Node:         node,
			Version:      version,
			SavedDigest:  digest,
			ActualDigest: digest,
		}
	}
	s.acceptTime(at)
	return nil
}

// Rot 在某节点副本上模拟位腐：实际摘要被改成 corruptDigest。
func (s *Store) Rot(at Time, blockID, node int, corruptDigest string) error {
	if corruptDigest == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	b := s.blocks[blockID]
	if b == nil {
		return ErrNotFound
	}
	r := b.replicas[node]
	if r == nil || corruptDigest == r.SavedDigest {
		return ErrInvalid
	}
	r.ActualDigest = corruptDigest
	s.acceptTime(at)
	return nil
}

// Discard 丢弃某节点上的副本；副本数降到 0 时块视为不存在。
func (s *Store) Discard(at Time, blockID, node int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	b := s.blocks[blockID]
	if b == nil {
		return ErrNotFound
	}
	if b.replicas[node] == nil {
		return ErrInvalid
	}
	delete(b.replicas, node)
	if len(b.replicas) == 0 {
		delete(s.blocks, blockID)
		s.due.remove(blockID)
	}
	s.acceptTime(at)
	return nil
}

// Patrol 对块执行一次巡检仲裁与修复。
// writeFail 注入修复写失败节点：失败节点保持原状，结果为部分修复；
// writeFail 中不是修复目标的节点一律忽略。
func (s *Store) Patrol(at Time, blockID int, writeFail map[int]bool) (PatrolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return PatrolResult{}, err
	}
	b := s.blocks[blockID]
	if b == nil {
		return PatrolResult{}, ErrNotFound
	}
	if !b.neverScrubbed && at < b.lastPatrol+b.minInterval {
		return PatrolResult{}, ErrTooFrequent
	}

	// 临界区内快照仲裁：并发写不可能出现在仲裁与修复之间。
	arb := Arbitrate(b.orderedReplicas())
	res := PatrolResult{Arbitration: arb, Time: at}

	if arb.Outcome == OutcomeRepaired {
		for _, t := range arb.Targets {
			if writeFail[t.Node] {
				res.FailedNodes = append(res.FailedNodes, t.Node)
				continue
			}
			r := b.replicas[t.Node]
			r.Version = arb.AuthorityVersion
			r.SavedDigest = arb.AuthorityDigest
			r.ActualDigest = arb.AuthorityDigest
			res.Repaired = append(res.Repaired, t.Node)
		}
		if len(res.FailedNodes) > 0 {
			res.Outcome = OutcomePartial
		}
	}

	// 成功完成、部分修复与三类不可修复均记录巡检时刻与（若有）告警；
	// 能走到这里说明未被时钟/间隔拒绝，时钟同步推进。
	b.neverScrubbed = false
	b.lastPatrol = at
	s.due.upsert(dueEntry{dueAt: b.dueAt(), block: blockID})
	if arb.Outcome == OutcomeNoSource ||
		arb.Outcome == OutcomeCommittedLost ||
		arb.Outcome == OutcomeConflict {
		s.alerts = append(s.alerts, Alert{
			Seq:     len(s.alerts) + 1,
			Time:    at,
			BlockID: blockID,
			Outcome: arb.Outcome,
			Detail:  alertDetail(arb),
		})
	}
	s.acceptTime(at)
	return res, nil
}

func alertDetail(a Arbitration) string {
	switch a.Outcome {
	case OutcomeNoSource:
		return "no intact replica can serve as authority source"
	case OutcomeCommittedLost:
		return "max intact version below committed version"
	case OutcomeConflict:
		return "divergent digests at max intact version"
	default:
		return ""
	}
}

// Due 只读到期待巡检的块：从未巡检块优先，其次上次巡检从早到晚，
// 并列取块号小者。不改变任何状态；仍受时钟回退检查。
func (s *Store) Due(at Time, limit int) ([]int, error) {
	if limit <= 0 {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return nil, err
	}

	var due []dueEntry
	s.due.enumerate(at, func(e dueEntry) bool {
		due = append(due, e)
		return true
	})

	never := make([]int, 0)
	patrolled := make([]int, 0)
	for _, e := range due {
		b := s.blocks[e.block]
		if b == nil {
			continue
		}
		if b.neverScrubbed {
			never = append(never, e.block)
		} else {
			patrolled = append(patrolled, e.block)
		}
	}
	sort.Ints(never)
	sort.Slice(patrolled, func(i, j int) bool {
		bi, bj := s.blocks[patrolled[i]], s.blocks[patrolled[j]]
		if bi.lastPatrol != bj.lastPatrol {
			return bi.lastPatrol < bj.lastPatrol
		}
		return patrolled[i] < patrolled[j]
	})

	out := append(never, patrolled...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Alerts 返回全部告警的按序副本（同一块重复告警不合并）。
func (s *Store) Alerts() []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Alert, len(s.alerts))
	copy(out, s.alerts)
	return out
}
