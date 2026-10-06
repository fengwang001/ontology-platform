package scrub

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

// Service 是多副本块存储的巡检与修复仲裁服务。
// 所有方法可并发调用，内部以互斥锁串行化，
// 结果等价于某个串行顺序。
type Service struct {
	mu       sync.Mutex
	blocks   map[uint64]*block
	due      *dueHeap
	lastTime int64 // 上一次被接受操作的时刻
	hasTime  bool  // 是否已有被接受的操作
	alerts   []Alert
}

type block struct {
	id          uint64
	minInterval int64
	replicas    map[uint64]*Replica // nodeID -> replica
	scrubbed    bool
	lastScrub   int64
}

// NewService 创建一个空服务。
func NewService() *Service {
	return &Service{
		blocks: make(map[uint64]*block),
		due:    newDueHeap(),
	}
}

// CreateBlock 创建块：2 到 5 个副本，分布在编号不同的节点上，
// 初始版本与摘要一致（全部自洽），minInterval 为最小巡检间隔。
func (s *Service) CreateBlock(id uint64, nodes []uint64, version uint64, digest string, minInterval int64, now int64) error {
	if now < 0 || version == 0 || digest == "" || minInterval < 0 ||
		len(nodes) < 2 || len(nodes) > 5 || !distinctNodes(nodes) {
		return fmt.Errorf("%w: create block %d", ErrInvalidParam, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClockLocked(now); err != nil {
		return err
	}
	if _, ok := s.blocks[id]; ok {
		return fmt.Errorf("%w: block %d already exists", ErrInvalidParam, id)
	}
	b := &block{id: id, minInterval: minInterval, replicas: make(map[uint64]*Replica, len(nodes))}
	for _, n := range nodes {
		b.replicas[n] = &Replica{NodeID: n, Version: version, Stored: digest, Actual: digest}
	}
	s.blocks[id] = b
	s.due.upsert(id, dueKey(b))
	s.acceptLocked(now)
	return nil
}

// Write 注入外部写入事件：把 (version, digest) 写到若干节点，
// 写入成功的副本立即自洽（Stored 与 Actual 均为 digest）。
// 目标节点必须持有该块副本，否则报参数非法；任一节点不合法则整次不生效。
func (s *Service) Write(id uint64, version uint64, digest string, nodes []uint64, now int64) error {
	if now < 0 || version == 0 || digest == "" || len(nodes) == 0 || !distinctNodes(nodes) {
		return fmt.Errorf("%w: write block %d", ErrInvalidParam, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClockLocked(now); err != nil {
		return err
	}
	b, ok := s.blocks[id]
	if !ok {
		return fmt.Errorf("%w: %d", ErrBlockNotFound, id)
	}
	for _, n := range nodes {
		if _, ok := b.replicas[n]; !ok {
			return fmt.Errorf("%w: node %d holds no replica of block %d", ErrInvalidParam, n, id)
		}
	}
	for _, n := range nodes {
		r := b.replicas[n]
		r.Version = version
		r.Stored = digest
		r.Actual = digest
	}
	s.acceptLocked(now)
	return nil
}

// InjectBitrot 模拟位腐：改变某节点副本的实际摘要。
func (s *Service) InjectBitrot(id, nodeID uint64, actualDigest string, now int64) error {
	if now < 0 || actualDigest == "" {
		return fmt.Errorf("%w: bitrot block %d", ErrInvalidParam, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClockLocked(now); err != nil {
		return err
	}
	r, err := s.replicaLocked(id, nodeID)
	if err != nil {
		return err
	}
	r.Actual = actualDigest
	s.acceptLocked(now)
	return nil
}

// DropReplica 丢弃某节点上的副本；副本数降到零时块视为不存在。
// 法定数与已提交版本在此后的巡检中按当前副本数重新推断。
func (s *Service) DropReplica(id, nodeID uint64, now int64) error {
	if now < 0 {
		return fmt.Errorf("%w: drop replica of block %d", ErrInvalidParam, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClockLocked(now); err != nil {
		return err
	}
	b, ok := s.blocks[id]
	if !ok {
		return fmt.Errorf("%w: %d", ErrBlockNotFound, id)
	}
	if _, ok := b.replicas[nodeID]; !ok {
		return fmt.Errorf("%w: node %d holds no replica of block %d", ErrInvalidParam, nodeID, id)
	}
	delete(b.replicas, nodeID)
	if len(b.replicas) == 0 {
		delete(s.blocks, id)
		s.due.remove(id)
	}
	s.acceptLocked(now)
	return nil
}

// Scrub 对块执行一次巡检仲裁与修复。failNodes 为调用方注入的写失败节点。
//
// 成功完成、部分修复与三类不可修复的巡检都记录巡检时刻并推进时钟；
// 被拒绝（报错）的巡检不改变任何状态与时钟。
// 三类不可修复结果追加到告警列表，不修改任何副本。
func (s *Service) Scrub(id uint64, now int64, failNodes map[uint64]bool) (ScrubResult, error) {
	if now < 0 {
		return ScrubResult{}, fmt.Errorf("%w: scrub block %d", ErrInvalidParam, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClockLocked(now); err != nil {
		return ScrubResult{}, err
	}
	b, ok := s.blocks[id]
	if !ok {
		return ScrubResult{}, fmt.Errorf("%w: %d", ErrBlockNotFound, id)
	}
	if b.scrubbed && now-b.lastScrub < b.minInterval {
		return ScrubResult{}, fmt.Errorf("%w: block %d interval %d elapsed %d",
			ErrTooFrequent, id, b.minInterval, now-b.lastScrub)
	}

	snap := make([]Replica, 0, len(b.replicas))
	for _, r := range b.replicas {
		snap = append(snap, *r)
	}
	v := Arbitrate(snap)

	res := ScrubResult{
		Outcome:          v.Outcome,
		CommittedVersion: v.CommittedVersion,
		AuthVersion:      v.AuthVersion,
		AuthDigest:       v.AuthDigest,
	}
	switch {
	case v.Outcome.Unrepairable():
		s.alerts = append(s.alerts, Alert{
			Seq:                      len(s.alerts),
			BlockID:                  id,
			Time:                     now,
			Outcome:                  v.Outcome,
			CommittedVersion:         v.CommittedVersion,
			HasSelfConsistent:        v.Outcome != OutcomeNoSource,
			MaxSelfConsistentVersion: v.AuthVersion,
		})
	case len(v.RepairTargets) > 0:
		for _, n := range v.RepairTargets {
			if failNodes[n] {
				res.Failed = append(res.Failed, n)
				continue
			}
			r := b.replicas[n]
			r.Version = v.AuthVersion
			r.Stored = v.AuthDigest
			r.Actual = v.AuthDigest
			res.Repaired = append(res.Repaired, n)
		}
		if len(res.Failed) > 0 {
			res.Outcome = OutcomePartialRepair
		}
	}

	b.scrubbed = true
	b.lastScrub = now
	s.due.upsert(id, dueKey(b))
	s.acceptLocked(now)
	return res, nil
}

// SelectDue 返回已到期的块（只读，不改变状态，但受时钟回退检查）：
// 从未巡检的块优先，其次按上次巡检时刻从早到晚，并列取块号小者。
//
// 实现：从到期堆中弹出全部已到期条目（开销随已到期块数增长），
// 按规则排序后取前 limit 个，再把弹出的条目放回堆中。
func (s *Service) SelectDue(now int64, limit int) ([]uint64, error) {
	if now < 0 || limit <= 0 {
		return nil, fmt.Errorf("%w: select due limit %d", ErrInvalidParam, limit)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClockLocked(now); err != nil {
		return nil, err
	}

	var popped []*dueEntry
	for {
		top := s.due.peek()
		if top == nil || top.key > now {
			break
		}
		popped = append(popped, s.due.pop())
	}
	defer func() {
		for _, e := range popped {
			s.due.pushBack(e)
		}
	}()

	sort.Slice(popped, func(i, j int) bool {
		bi, bj := s.blocks[popped[i].blockID], s.blocks[popped[j].blockID]
		if bi.scrubbed != bj.scrubbed {
			return !bi.scrubbed // 从未巡检的块优先
		}
		if bi.scrubbed && bi.lastScrub != bj.lastScrub {
			return bi.lastScrub < bj.lastScrub
		}
		return bi.id < bj.id
	})

	n := limit
	if len(popped) < n {
		n = len(popped)
	}
	ids := make([]uint64, 0, n)
	for _, e := range popped[:n] {
		ids = append(ids, e.blockID)
	}
	return ids, nil
}

// Alerts 按发生次序返回全部告警的副本。
func (s *Service) Alerts() []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Alert, len(s.alerts))
	copy(out, s.alerts)
	return out
}

// Inspect 返回块的只读快照。
func (s *Service) Inspect(id uint64) (BlockInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blocks[id]
	if !ok {
		return BlockInfo{}, fmt.Errorf("%w: %d", ErrBlockNotFound, id)
	}
	info := BlockInfo{
		ID:          b.id,
		MinInterval: b.minInterval,
		Scrubbed:    b.scrubbed,
		LastScrub:   b.lastScrub,
		Replicas:    make([]Replica, 0, len(b.replicas)),
	}
	for _, r := range b.replicas {
		info.Replicas = append(info.Replicas, *r)
	}
	sort.Slice(info.Replicas, func(i, j int) bool { return info.Replicas[i].NodeID < info.Replicas[j].NodeID })
	return info, nil
}

// LastAcceptedTime 返回上一次被接受操作的时刻。
func (s *Service) LastAcceptedTime() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTime, s.hasTime
}

// checkClockLocked 时钟回退检查：时刻不得小于上一次被接受操作的时刻。
func (s *Service) checkClockLocked(now int64) error {
	if s.hasTime && now < s.lastTime {
		return fmt.Errorf("%w: now %d < last accepted %d", ErrClockRegression, now, s.lastTime)
	}
	return nil
}

func (s *Service) acceptLocked(now int64) {
	s.lastTime = now
	s.hasTime = true
}

func (s *Service) replicaLocked(id, nodeID uint64) (*Replica, error) {
	b, ok := s.blocks[id]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrBlockNotFound, id)
	}
	r, ok := b.replicas[nodeID]
	if !ok {
		return nil, fmt.Errorf("%w: node %d holds no replica of block %d", ErrInvalidParam, nodeID, id)
	}
	return r, nil
}

// dueKey 计算块的到期堆键：未巡检为最小键（总是最先到期），
// 否则为 lastScrub + minInterval（饱和加法防溢出）。
func dueKey(b *block) int64 {
	if !b.scrubbed {
		return math.MinInt64
	}
	if b.lastScrub > math.MaxInt64-b.minInterval {
		return math.MaxInt64
	}
	return b.lastScrub + b.minInterval
}

func distinctNodes(nodes []uint64) bool {
	seen := make(map[uint64]struct{}, len(nodes))
	for _, n := range nodes {
		if _, ok := seen[n]; ok {
			return false
		}
		seen[n] = struct{}{}
	}
	return true
}
