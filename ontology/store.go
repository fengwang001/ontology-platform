package ontology

import "sync"

// Store 是对象实例、链接关联与逻辑时钟的内存实现。
// 每个目标实例由一把互斥锁串行化“读取最新基线 + 基数校验 + 提交”，
// 因此这三步在单个实例上构成不可分割的线性化临界区。
type Store struct {
	mu sync.Mutex

	objects map[string]*objectState
	clock   uint64

	// commitLog 仅记录成功提交，用于测试重放与串行等价核验；
	// 不属于对外暴露的状态，不参与任何判定。
	commitLog []CommitRecord
}

type objectState struct {
	// links[约束键][对端实例ID] = true
	links map[string]map[string]bool
	// limits[约束键] = 该链接类型方向上的基数上限
	limits  map[string]int
	version int64
	mu      sync.Mutex
}

// CommitRecord 是一次成功提交的不可变记录。
type CommitRecord struct {
	Clock       uint64
	Version     int64
	ObjectID    string
	BaseVersion int64
	Ops         []LinkOp
}

func NewStore() *Store {
	return &Store{objects: make(map[string]*objectState)}
}

// AddObject 登记一个（初始版本为 0、无关联的）对象实例。
func (s *Store) AddObject(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[id]; !ok {
		s.objects[id] = &objectState{
			links:  make(map[string]map[string]bool),
			limits: make(map[string]int),
		}
	}
}

// AddCardinality 为目标实例登记一个基数约束。
// 同一约束键重复登记时以较小上限为准（只能收紧）。
func (s *Store) AddCardinality(objID string, c Cardinality) {
	s.mu.Lock()
	obj, ok := s.objects[objID]
	s.mu.Unlock()
	if !ok {
		panic("ontology: unknown object " + objID)
	}
	obj.mu.Lock()
	defer obj.mu.Unlock()
	key := c.Key()
	if existing, ok := obj.limits[key]; !ok || c.Max < existing {
		obj.limits[key] = c.Max
	}
}

// Snapshot 返回目标实例当前状态的完整深拷贝。
func (s *Store) Snapshot(id string) *Snapshot {
	s.mu.Lock()
	obj, ok := s.objects[id]
	s.mu.Unlock()
	if !ok {
		panic("ontology: unknown object " + id)
	}
	obj.mu.Lock()
	defer obj.mu.Unlock()
	return obj.snapshotLocked(id)
}

// Clock 返回当前逻辑时钟值（仅提交成功才会推进）。
func (s *Store) Clock() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

// CommitLog 返回成功提交记录的拷贝（供测试重放核验）。
func (s *Store) CommitLog() []CommitRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	log := make([]CommitRecord, len(s.commitLog))
	copy(log, s.commitLog)
	return log
}

func (o *objectState) snapshotLocked(id string) *Snapshot {
	snap := &Snapshot{
		ObjectID: id,
		Version:  o.version,
		Counts:   make(map[string]int, len(o.links)),
		Links:    make(map[string]map[string]bool, len(o.links)),
	}
	for key, set := range o.links {
		snap.Counts[key] = len(set)
		cloned := make(map[string]bool, len(set))
		for other := range set {
			cloned[other] = true
		}
		snap.Links[key] = cloned
	}
	return snap
}

// attemptOutcome 是单次“在同一把实例锁内完成读基线 → 版本判定 →
// 基数重算 → 提交”的结果。三步不可分割，因此读集合与判定依据
// 必然对应同一个线性化时点，不存在读后被他人抢先的窗口。
type attemptOutcome struct {
	read       *Snapshot
	reason     Reason
	violations []Violation
	committed  bool
	version    int64
	clock      uint64
}

// attempt 执行一次内部尝试。expectedVersion 是本次尝试开始时
// 新鲜读取到的基线：第一次尝试取调用方基线，其后每次尝试
// 都由 Submit 循环重新读取当前版本后传入，绝不沿用旧值。
//
// 调用方（Submit 循环）保证每次都重新调用本方法，因而每次
// 尝试都会在锁内重新读取最新版本与全部关联，前一次尝试读到的
// 集合或计数不会被带入本次判定。
func (s *Store) attempt(change Change, expectedVersion int64) attemptOutcome {
	s.mu.Lock()
	obj, ok := s.objects[change.ObjectID]
	s.mu.Unlock()
	if !ok {
		panic("ontology: unknown object " + change.ObjectID)
	}
	obj.mu.Lock()
	defer obj.mu.Unlock()

	read := obj.snapshotLocked(change.ObjectID)

	// 判定顺序第 1 位：版本冲突优先，且本尝试只命中这一种原因。
	// 冲突窗口只存在于“本次尝试新鲜读取”与“进入实例锁复核”之间：
	// 若有其他请求在此窗口提交，版本被推进，本次尝试立即中止，
	// 不做基数判定，也不改动任何状态。
	if read.Version != expectedVersion {
		return attemptOutcome{read: read, reason: ReasonVersionConflict}
	}

	// 判定顺序第 2 位：基于本次锁内最新读取重新计算每一个涉及的约束。
	// 计数来自 map 长度（O(1) 读取），开销只与本次变更的操作数有关，
	// 不随该链接类型当前关联总数增长。
	violations := projectViolations(obj, change.Ops)
	if len(violations) > 0 {
		return attemptOutcome{read: read, reason: ReasonCardinality, violations: violations}
	}

	// 全部约束在最新读取下均满足：真正提交。
	// 只有走到这里，版本号、关联关系与逻辑时钟才会发生变化。
	for _, op := range change.Ops {
		key := op.Key()
		set := o_links(obj, key)
		if op.Add {
			set[op.OtherID] = true
		} else {
			delete(set, op.OtherID)
		}
	}
	obj.version++

	s.mu.Lock()
	s.clock++
	clock := s.clock
	record := CommitRecord{
		Clock:       clock,
		Version:     obj.version,
		ObjectID:    change.ObjectID,
		BaseVersion: expectedVersion,
		Ops:         append([]LinkOp(nil), change.Ops...),
	}
	s.commitLog = append(s.commitLog, record)
	s.mu.Unlock()

	read.Version = obj.version
	return attemptOutcome{
		read:      read,
		reason:    ReasonCommitted,
		committed: true,
		version:   obj.version,
		clock:     clock,
	}
}

// projectViolations 在不修改任何状态的前提下，计算本次变更涉及的
// 每一个基数约束在当前最新关联集合上的投影结果并列出全部违例。
//
// 开销只与“本次变更触及的不同约束键数量 k”成正比：
//   - 当前数量取 len(链接集合)，O(1)；
//   - 每条操作对其自身约束键做一次 map 存在性查询，O(1)；
//
// 与该链接类型当前已存在的关联总数无关，且计数直接来自被存储的
// 链接集合本身，不维护也不依赖任何额外（对外暴露）的计数字段。
func projectViolations(obj *objectState, ops []LinkOp) []Violation {
	delta := make(map[string]int)
	for _, op := range ops {
		key := op.Key()
		set := o_links(obj, key)
		_, present := set[op.OtherID]
		switch {
		case op.Add && !present:
			delta[key]++
		case !op.Add && present:
			delta[key]--
		}
	}

	var violations []Violation
	for key, net := range delta {
		max, constrained := obj.limits[key]
		if !constrained {
			continue
		}
		current := len(obj.links[key])
		projected := current + net
		if projected > max || projected < 0 {
			violations = append(violations, Violation{
				LinkType:  linkTypeOf(key),
				Direction: dirOf(key),
				Current:   current,
				Projected: projected,
				Max:       max,
			})
		}
	}
	return violations
}

func o_links(obj *objectState, key string) map[string]bool {
	set, ok := obj.links[key]
	if !ok {
		set = make(map[string]bool)
		obj.links[key] = set
	}
	return set
}

func linkTypeOf(key string) string {
	if len(key) >= 4 && key[:3] == "out" {
		return key[4:]
	}
	if len(key) >= 3 && key[:2] == "in" {
		return key[3:]
	}
	return key
}

func dirOf(key string) Direction {
	if len(key) >= 3 && key[:3] == "out" {
		return Outgoing
	}
	return Incoming
}
