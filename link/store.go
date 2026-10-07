package link

import (
	"fmt"
	"sync"
)

// ObjectChecker 报告对象实例当前是否仍有效（未被逻辑删除）。
type ObjectChecker interface {
	// Lookup 返回实例的对象类型；第二个返回值为 false 表示实例不存在
	// 或已被逻辑删除。
	Lookup(instanceID ObjectInstanceID) (ObjectTypeID, bool)
}

const shardCount = 256

// pairKey 以无向方式标识同一对实例，保证两个方向的互斥落在同一分片。
type pairKey struct {
	typeID LinkTypeID
	lo, hi ObjectInstanceID
}

// degreeKey 标识某实例在某链接类型某一方向上的度数槽。
type degreeKey struct {
	typeID     LinkTypeID
	instanceID ObjectInstanceID
	direction  Direction
}

// pairState 保存一对实例上两个方向各自的在库链接，键为规范化区分属性。
// 不保留任何已撤销条目，因此占用与历史无关。
type pairState struct {
	forward  map[string]*Link
	backward map[string]*Link
}

type shard struct {
	mu     sync.Mutex
	pairs  map[pairKey]*pairState
	degree map[degreeKey]int
}

// Store 是链接实例的并发安全仲裁存储。
type Store struct {
	typesMu sync.RWMutex
	types   map[LinkTypeID]*LinkType
	objects ObjectChecker

	shards [shardCount]*shard

	logMu     sync.Mutex
	decisions []DecisionLog
	seq       int64
	nextID    uint64
}

// NewStore 创建一个空的仲裁存储。
func NewStore(objects ObjectChecker) *Store {
	s := &Store{
		types:   make(map[LinkTypeID]*LinkType),
		objects: objects,
	}
	for i := range s.shards {
		s.shards[i] = &shard{
			pairs:  make(map[pairKey]*pairState),
			degree: make(map[degreeKey]int),
		}
	}
	return s
}

// RegisterType 登记（或替换）一个链接类型声明。
func (s *Store) RegisterType(t LinkType) error {
	if t.SourceType == "" || t.TargetType == "" {
		return fmt.Errorf("link: link type %q missing endpoint types", t.ID)
	}
	if !t.ForwardCap.Unlimited && t.ForwardCap.Limit < 0 {
		return fmt.Errorf("link: link type %q has negative forward cap", t.ID)
	}
	if !t.BackwardCap.Unlimited && t.BackwardCap.Limit < 0 {
		return fmt.Errorf("link: link type %q has negative backward cap", t.ID)
	}
	s.typesMu.Lock()
	defer s.typesMu.Unlock()
	cp := t
	s.types[t.ID] = &cp
	return nil
}

// Create 仲裁一次创建请求。
//
// 失败优先级（对相同输入保持稳定）：
//  1. 任一对象实例不存在/已逻辑删除（优先于一切）；
//  2. 链接类型不允许当前对象类型按该方向建立链接（含未知链接类型）；
//  3. 区分属性组合与在库链接冲突（重复，不消耗基数名额）；
//  4. 目标方向基数已满。
func (s *Store) Create(req CreateRequest) (*Link, error) {
	t := s.typeByID(req.TypeID)

	srcType, srcAlive := s.objects.Lookup(req.SourceID)
	tgtType, tgtAlive := s.objects.Lookup(req.TargetID)
	if !srcAlive || !tgtAlive {
		reason := "source instance missing or deleted"
		if srcAlive {
			reason = "target instance missing or deleted"
		}
		s.record("create", req, reason, ResultRejected, 0)
		return nil, ErrObjectInstanceDeleted
	}

	dir, err := resolveDirection(t, srcType, tgtType, req.SourceID, req.TargetID)
	if err != nil {
		s.record("create", req, err.Error(), ResultRejected, 0)
		return nil, err
	}

	disc := canonicalDiscriminator(req.Discriminator)
	pk := makePairKey(req.TypeID, req.SourceID, req.TargetID)
	srcKey := degreeKey{req.TypeID, req.SourceID, dir}

	idxs := s.lockIndices(pairShard(pk), degreeShard(srcKey))
	s.lockAll(idxs)
	defer s.unlockAll(idxs)

	sh := s.shards[pairShard(pk)]
	ps := sh.pairs[pk]

	if existing := lookupLink(ps, dir, disc); existing != nil {
		s.recordLocked("create", req,
			fmt.Sprintf("duplicate of link #%d; cardinality unchanged", existing.ID),
			ResultDuplicate, existing.ID)
		return existing, ErrDuplicateLink
	}

	cap := forwardCap(t, dir)
	current := dirCount(ps, dir)
	if !cap.Unlimited && current >= cap.Limit {
		s.recordLocked("create", req,
			fmt.Sprintf("direction %s full: current=%d cap=%d", dir, current, cap.Limit),
			ResultRejected, 0)
		return nil, ErrCardinalityExceeded
	}

	if ps == nil {
		ps = &pairState{
			forward:  make(map[string]*Link),
			backward: make(map[string]*Link),
		}
		sh.pairs[pk] = ps
	}

	s.nextID++
	link := &Link{
		ID:            s.nextID,
		TypeID:        req.TypeID,
		SourceID:      req.SourceID,
		TargetID:      req.TargetID,
		Discriminator: cloneDiscriminator(req.Discriminator),
	}
	dirMap(ps, dir)[disc] = link

	s.shards[degreeShard(srcKey)].degree[srcKey]++

	s.recordLocked("create", req,
		fmt.Sprintf("created in direction %s: new count=%d", dir, current+1),
		ResultCreated, link.ID)
	return link, nil
}

// Delete 删除一条链接；链接不存在时返回 ErrLinkNotFound 风格结果。
//
// 删除与针对同一对实例的创建在同一把 pair 临界区内串行，因此并发结果
// 必然等价于“删除在前”或“创建在前”两种全序之一：名额要么已释放、
// 要么尚未占用，不存在任何不对应串行序的中间状态。
func (s *Store) Delete(typeID LinkTypeID, sourceID, targetID ObjectInstanceID, discriminator map[string]string) (*Link, error) {
	req := CreateRequest{TypeID: typeID, SourceID: sourceID, TargetID: targetID, Discriminator: discriminator}
	t := s.typeByID(typeID)

	srcType, srcAlive := s.objects.Lookup(sourceID)
	tgtType, tgtAlive := s.objects.Lookup(targetID)
	if !srcAlive || !tgtAlive {
		reason := "source instance missing or deleted"
		if srcAlive {
			reason = "target instance missing or deleted"
		}
		s.record("delete", req, reason, ResultRejected, 0)
		return nil, ErrObjectInstanceDeleted
	}

	dir, err := resolveDirection(t, srcType, tgtType, sourceID, targetID)
	if err != nil {
		s.record("delete", req, err.Error(), ResultRejected, 0)
		return nil, err
	}

	disc := canonicalDiscriminator(discriminator)
	pk := makePairKey(typeID, sourceID, targetID)
	srcKey := degreeKey{typeID, sourceID, dir}

	idxs := s.lockIndices(pairShard(pk), degreeShard(srcKey))
	s.lockAll(idxs)
	defer s.unlockAll(idxs)

	sh := s.shards[pairShard(pk)]
	ps := sh.pairs[pk]
	link := lookupLink(ps, dir, disc)
	if link == nil {
		s.recordLocked("delete", req, "link not found; nothing changed", ResultNotFound, 0)
		return nil, ErrLinkNotFound
	}

	delete(dirMap(ps, dir), disc)
	releaseDegree(s.shards[degreeShard(srcKey)], srcKey)

	if len(ps.forward) == 0 && len(ps.backward) == 0 {
		delete(sh.pairs, pk)
	}

	s.recordLocked("delete", req,
		fmt.Sprintf("link #%d revoked; slot released", link.ID),
		ResultDeleted, link.ID)
	return link, nil
}

// CountDirection 返回某对象实例在给定方向上已登记的有效链接数量。
// 该操作只访问单个度数槽：O(1) 时间与 O(1) 额外空间，与历史上创建/
// 撤销过多少链接无关——空槽在删除时即被回收。
func (s *Store) CountDirection(typeID LinkTypeID, instanceID ObjectInstanceID, direction Direction) int {
	k := degreeKey{typeID, instanceID, direction}
	sh := s.shards[degreeShard(k)]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return sh.degree[k]
}

// Decisions 返回截至目前所有仲裁判定的只读快照。
// 每条记录包含输入、判定依据与结果；Seq 即系统承认的串行化全序。
func (s *Store) Decisions() []DecisionLog {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	out := make([]DecisionLog, len(s.decisions))
	copy(out, s.decisions)
	return out
}
