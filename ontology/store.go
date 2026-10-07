package ontology

import (
	"sort"
	"sync"
)

// muState 是单个实例的可变状态，由该实例自己的互斥锁保护。
type muState struct {
	version uint64
	links   map[string]Link
	// counts 记录本实例作为持有方时，每个约束下当前的去重关联数。
	counts map[ConstraintKey]int
}

// Store 是内存版的链接与版本存储。
// 每个实例持有独立互斥锁：一次提交的“版本检查→基数校验→写入”在锁内原子完成。
type Store struct {
	catalogMu sync.RWMutex
	types     map[string]*LinkType

	instMu    sync.RWMutex
	instances map[string]*muState
	locks     map[string]*sync.Mutex

	// 决策读开销统计（内部证据，不构成对外暴露状态）：
	// counterReads 为基数判定读取计数器的次数（与链接总数无关）；
	// scans 为遍历全量链接集合的次数，仅用于审计快照。
	statsMu      sync.Mutex
	counterReads int
	scans        int
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{
		types:     map[string]*LinkType{},
		instances: map[string]*muState{},
		locks:     map[string]*sync.Mutex{},
	}
}

// RegisterLinkType 注册链接类型声明。
func (s *Store) RegisterLinkType(lt LinkType) error {
	if lt.ID == "" {
		return &ErrInvalid{Msg: "link type id is empty"}
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	if _, exists := s.types[lt.ID]; exists {
		return &ErrInvalid{Msg: "duplicate link type: " + lt.ID}
	}
	cp := lt
	s.types[lt.ID] = &cp
	return nil
}

// CreateInstance 创建实例，初始版本为 0。
func (s *Store) CreateInstance(id string) error {
	if id == "" {
		return &ErrInvalid{Msg: "instance id is empty"}
	}
	s.instMu.Lock()
	defer s.instMu.Unlock()
	if _, exists := s.instances[id]; exists {
		return &ErrInvalid{Msg: "duplicate instance: " + id}
	}
	s.instances[id] = &muState{
		links:  map[string]Link{},
		counts: map[ConstraintKey]int{},
	}
	s.locks[id] = &sync.Mutex{}
	return nil
}

// InstanceExists 判断实例是否存在。
func (s *Store) InstanceExists(id string) bool {
	s.instMu.RLock()
	defer s.instMu.RUnlock()
	_, ok := s.instances[id]
	return ok
}

func (s *Store) lockFor(id string) *sync.Mutex {
	s.instMu.RLock()
	mu := s.locks[id]
	s.instMu.RUnlock()
	return mu
}

func (s *Store) stateFor(id string) *muState {
	s.instMu.RLock()
	st := s.instances[id]
	s.instMu.RUnlock()
	return st
}

func (s *Store) linkType(id string) (*LinkType, bool) {
	s.catalogMu.RLock()
	lt, ok := s.types[id]
	s.catalogMu.RUnlock()
	return lt, ok
}

func (s *Store) bumpCounterReads(n int) {
	s.statsMu.Lock()
	s.counterReads += n
	s.statsMu.Unlock()
}

func (s *Store) bumpScans(n int) {
	s.statsMu.Lock()
	s.scans += n
	s.statsMu.Unlock()
}

// resetReadStats 清空读开销统计。
func (s *Store) resetReadStats() {
	s.statsMu.Lock()
	s.counterReads, s.scans = 0, 0
	s.statsMu.Unlock()
}

// readStats 返回 (计数器读取次数, 全量链接扫描次数)。
func (s *Store) readStats() (counterReads, scans int) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	return s.counterReads, s.scans
}

// Counter 只读某实例在单个约束上的当前计数。基数判定只依赖该读路径（O(1)）。
func (s *Store) Counter(id string, key ConstraintKey) (int, error) {
	mu := s.lockFor(id)
	if mu == nil {
		return 0, &ErrNotFound{What: "instance " + id}
	}
	mu.Lock()
	defer mu.Unlock()
	s.bumpCounterReads(1)
	return s.stateFor(id).counts[key], nil
}

// LinkCount 读取某实例当前全部去重链接数量（用于基准中的规模 N）。
func (s *Store) LinkCount(id string) (int, error) {
	mu := s.lockFor(id)
	if mu == nil {
		return 0, &ErrNotFound{What: "instance " + id}
	}
	mu.Lock()
	defer mu.Unlock()
	return len(s.stateFor(id).links), nil
}

// LinkSnapshot 读取目标实例当前全部链接（审计/重放输入）。
// 该遍历只服务于完整记录，不参与基数是否命中的判定。
func (s *Store) LinkSnapshot(id string) ([]Link, error) {
	mu := s.lockFor(id)
	if mu == nil {
		return nil, &ErrNotFound{What: "instance " + id}
	}
	mu.Lock()
	defer mu.Unlock()
	return s.snapshotLocked(s.stateFor(id)), nil
}

func (s *Store) snapshotLocked(st *muState) []Link {
	s.bumpScans(1)
	out := make([]Link, 0, len(st.links))
	for _, lk := range st.links {
		out = append(out, lk)
	}
	sortLinks(out)
	return out
}

func sortLinks(ls []Link) {
	sort.Slice(ls, func(i, j int) bool {
		if ls[i].TypeID != ls[j].TypeID {
			return ls[i].TypeID < ls[j].TypeID
		}
		if ls[i].A != ls[j].A {
			return ls[i].A < ls[j].A
		}
		return ls[i].B < ls[j].B
	})
}

// plannedChange 是一次提交在单个链接上的净效果。
type plannedChange struct {
	key    string
	link   Link
	add    bool // 最终意图：true=新增，false=移除
	exists bool // 进入临界区时该链接是否已存在
}

// planOps 规范化、去重并合并同一链接上的多次操作，同时收集需触碰的实例与约束。
// 只收集目标实例自身承担的基数约束——一次请求只对目标实例的约束负责，
// 另一侧实例被触碰时其自身约束由以它为目标的请求校验（见设计说明）。
func (s *Store) planOps(id string, ops []Op) (map[string]*plannedChange, map[string]bool, []ConstraintKey, error) {
	changes := map[string]*plannedChange{}
	touch := map[string]bool{id: true}
	keySet := map[ConstraintKey]bool{}

	for _, op := range ops {
		lt, ok := s.linkType(op.TypeID)
		if !ok {
			return nil, nil, nil, &ErrNotFound{What: "link type " + op.TypeID}
		}
		if !s.InstanceExists(op.Other) {
			return nil, nil, nil, &ErrNotFound{What: "instance " + op.Other}
		}
		touch[op.Other] = true

		lk := Link{TypeID: op.TypeID}
		switch op.Side {
		case SideA:
			lk.A, lk.B = id, op.Other
			if c := lt.Constraint(SideA); c != nil {
				keySet[ConstraintKey{TypeID: op.TypeID, Side: SideA}] = true
			}
		case SideB:
			lk.A, lk.B = op.Other, id
			if c := lt.Constraint(SideB); c != nil {
				keySet[ConstraintKey{TypeID: op.TypeID, Side: SideB}] = true
			}
		default:
			return nil, nil, nil, &ErrInvalid{Msg: "unknown side"}
		}

		if cur, seen := changes[lk.Key()]; seen {
			cur.add = op.Add // 同一链接多次操作：以净效果（最后一次）为准
		} else {
			changes[lk.Key()] = &plannedChange{key: lk.Key(), link: lk, add: op.Add}
		}
	}

	keys := make([]ConstraintKey, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].TypeID != keys[j].TypeID {
			return keys[i].TypeID < keys[j].TypeID
		}
		return keys[i].Side < keys[j].Side
	})
	return changes, touch, keys, nil
}

// lockStates 按实例 ID 升序获取多把实例锁并返回解锁函数，避免多实例死锁。
func (s *Store) lockStates(touch map[string]bool) (map[string]*muState, func()) {
	ids := make([]string, 0, len(touch))
	for id := range touch {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	mus := make([]*sync.Mutex, len(ids))
	states := make(map[string]*muState, len(ids))
	for i, id := range ids {
		mu := s.lockFor(id)
		mu.Lock()
		mus[i] = mu
		states[id] = s.stateFor(id)
	}
	return states, func() {
		for i := len(mus) - 1; i >= 0; i-- {
			mus[i].Unlock()
		}
	}
}

// verdictFor 基于临界区内的最新计数与净增量计算单个约束的判定依据。
func verdictFor(lt *LinkType, key ConstraintKey, current int, changes map[string]*plannedChange, target string) ConstraintVerdict {
	delta := 0
	for _, ch := range changes {
		if ch.link.TypeID != key.TypeID || ch.link.holder(key.Side) != target {
			continue
		}
		switch {
		case ch.add && !ch.exists:
			delta++
		case !ch.add && ch.exists:
			delta--
		}
	}
	max := 0
	if c := lt.Constraint(key.Side); c != nil {
		max = c.Max
	}
	satisfied := max <= 0 || current+delta <= max
	return ConstraintVerdict{
		Constraint: key,
		Current:    current,
		Delta:      delta,
		Max:        max,
		Satisfied:  satisfied,
	}
}

// commitResult 汇总一次原子提交的输出。
type commitResult struct {
	committed bool
	version   uint64
	verdicts  []ConstraintVerdict
	cardKey   *ConstraintKey
}

// commitLocked 完成一次原子提交。
// force=true 时跳过版本检查（供“插队者”使用），基数校验仍然真实执行。
func (s *Store) commitLocked(target string, expected uint64, ops []Op, force bool) (*commitResult, error) {
	changes, touch, keys, err := s.planOps(target, ops)
	if err != nil {
		return nil, err
	}
	states, unlock := s.lockStates(touch)
	defer unlock()

	tgt := states[target]
	if tgt == nil {
		return nil, &ErrNotFound{What: "instance " + target}
	}

	// 判定优先级第 1 位：版本冲突。锁内确认基线落后则零变更返回。
	if !force && tgt.version != expected {
		return &commitResult{committed: false, version: tgt.version}, nil
	}

	// 填充当前存在性；全部读取均为 O(1) map 查表与计数器读。
	targetChanged := false
	for _, ch := range changes {
		_, ch.exists = tgt.links[ch.key]
		if ch.add != ch.exists {
			targetChanged = true
		}
	}

	// 判定优先级第 2 位：在最新读取上对涉及的每个约束分别重新校验。
	s.bumpCounterReads(len(keys))
	verdicts := make([]ConstraintVerdict, 0, len(keys))
	var badKey *ConstraintKey
	for _, key := range keys {
		lt, _ := s.linkType(key.TypeID)
		v := verdictFor(lt, key, tgt.counts[key], changes, target)
		verdicts = append(verdicts, v)
		if !v.Satisfied && v.Delta > 0 && badKey == nil {
			k := key
			badKey = &k
		}
	}
	if badKey != nil {
		return &commitResult{committed: false, version: tgt.version, verdicts: verdicts, cardKey: badKey}, nil
	}

	// 版本匹配、全部目标侧基数判定通过，但净增量为 0（幂等重放）：
	// 视为成功，且不得推进版本或改动任何状态。
	if !targetChanged {
		return &commitResult{committed: true, version: tgt.version, verdicts: verdicts}, nil
	}

	// 判定全部通过：应用变更。只有走到这里才会修改任何状态。
	for _, ch := range changes {
		applyOne(s, states, ch)
	}
	if targetChanged {
		tgt.version++
	}
	return &commitResult{committed: true, version: tgt.version, verdicts: verdicts}, nil
}

// applyOne 对单个链接应用净效果，并同步维护两侧实例的计数器与链接集合。
func applyOne(s *Store, states map[string]*muState, ch *plannedChange) {
	lt, ok := s.linkType(ch.link.TypeID)
	if !ok {
		return
	}
	aState, bState := states[ch.link.A], states[ch.link.B]
	// 新增
	if ch.add && !ch.exists {
		aState.links[ch.key] = ch.link
		bState.links[ch.key] = ch.link
		if lt.CardinalityA != nil {
			kA := ConstraintKey{TypeID: ch.link.TypeID, Side: SideA}
			aState.counts[kA]++
		}
		if lt.CardinalityB != nil {
			kB := ConstraintKey{TypeID: ch.link.TypeID, Side: SideB}
			bState.counts[kB]++
		}
		return
	}
	// 移除
	if !ch.add && ch.exists {
		delete(aState.links, ch.key)
		delete(bState.links, ch.key)
		if lt.CardinalityA != nil {
			kA := ConstraintKey{TypeID: ch.link.TypeID, Side: SideA}
			aState.counts[kA]--
		}
		if lt.CardinalityB != nil {
			kB := ConstraintKey{TypeID: ch.link.TypeID, Side: SideB}
			bState.counts[kB]--
		}
	}
}

// tryCommit 对目标实例执行一次带基线的乐观提交。
func (s *Store) tryCommit(id string, expected uint64, ops []Op) (*commitResult, error) {
	return s.commitLocked(id, expected, ops, false)
}

// commitRaider 是测试专用的“插队者”：在当前最新版本上原子提交外部变更，
// 用于确定性地制造版本落后与基数变化。
func (s *Store) commitRaider(id string, ops []Op) (bool, error) {
	if s.lockFor(id) == nil {
		return false, &ErrNotFound{What: "instance " + id}
	}
	cur, err := s.readVersion(id)
	if err != nil {
		return false, err
	}
	res, err := s.commitLocked(id, cur, ops, true)
	if err != nil {
		return false, err
	}
	return res.committed, nil
}

// readVersion 读取某实例当前版本（O(1)）。
func (s *Store) readVersion(id string) (uint64, error) {
	mu := s.lockFor(id)
	if mu == nil {
		return 0, &ErrNotFound{What: "instance " + id}
	}
	mu.Lock()
	defer mu.Unlock()
	return s.stateFor(id).version, nil
}

// checkCardinality 对“目标实例当前最新计数”执行一次纯基数校验，不做任何写入。
// 输入的 ops 会先按链接合并净效果；返回逐约束判定与第一个不满足的约束。
// 整个判定只做 O(K) 次计数器读取与 map 查表（K=本次涉及的目标侧约束数），
// 不随该链接类型已存在的关联总数增长。
func (s *Store) checkCardinality(target string, ops []Op) (version uint64, verdicts []ConstraintVerdict, badKey *ConstraintKey, err error) {
	mu := s.lockFor(target)
	if mu == nil {
		return 0, nil, nil, &ErrNotFound{What: "instance " + target}
	}
	mu.Lock()
	defer mu.Unlock()

	intent := map[string]*plannedChange{}
	keySet := map[ConstraintKey]bool{}
	st := s.stateFor(target)

	for _, op := range ops {
		lt, ok := s.linkType(op.TypeID)
		if !ok {
			return 0, nil, nil, &ErrNotFound{What: "link type " + op.TypeID}
		}
		if !s.InstanceExists(op.Other) {
			return 0, nil, nil, &ErrNotFound{What: "instance " + op.Other}
		}
		lk := Link{TypeID: op.TypeID}
		if op.Side != SideA && op.Side != SideB {
			return 0, nil, nil, &ErrInvalid{Msg: "unknown side"}
		}
		if lt.Constraint(op.Side) == nil {
			continue // 目标侧无该约束，不参与基数判定
		}
		if op.Side == SideA {
			lk.A, lk.B = target, op.Other
		} else {
			lk.A, lk.B = op.Other, target
		}
		key := ConstraintKey{TypeID: op.TypeID, Side: op.Side}
		keySet[key] = true
		if cur, seen := intent[lk.Key()]; seen {
			cur.add = op.Add
		} else {
			intent[lk.Key()] = &plannedChange{key: lk.Key(), link: lk, add: op.Add}
		}
	}

	for _, ch := range intent {
		_, ch.exists = st.links[ch.key]
	}

	keys := make([]ConstraintKey, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].TypeID != keys[j].TypeID {
			return keys[i].TypeID < keys[j].TypeID
		}
		return keys[i].Side < keys[j].Side
	})

	s.bumpCounterReads(len(keys))
	verdicts = make([]ConstraintVerdict, 0, len(keys))
	for _, key := range keys {
		lt, _ := s.linkType(key.TypeID)
		v := verdictFor(lt, key, st.counts[key], intent, target)
		verdicts = append(verdicts, v)
		if !v.Satisfied && badKey == nil {
			k := key
			badKey = &k
		}
	}
	return st.version, verdicts, badKey, nil
}
