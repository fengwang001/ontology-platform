package ontology

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
)

// instanceState 单个实例的内部状态。版本、属性、关联都只能在持有
// 该实例的 mu 时读写；批次的联合判定与提交在同时持有本批次全部
// 相关实例的锁期间完成，这就是"同一个逻辑时刻"的实现。
type instanceState struct {
	mu      sync.Mutex
	version Version
	attrs   map[string]string
	links   map[LinkTypeID]map[InstanceID]struct{}
}

// Snapshot 实例状态的外部只读视图，用于观测与测试。
type Snapshot struct {
	Version Version
	Attrs   map[string]string
	Links   map[LinkTypeID][]InstanceID
}

// Store 本体实例存储，支持跨实例批量更新的联合版本前置判定。
type Store struct {
	registryMu sync.Mutex
	instances  map[InstanceID]*instanceState
	limits     map[LinkTypeID]int

	logMu     sync.Mutex
	log       []Decision
	clock     uint64
	commitSeq uint64
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{
		instances: make(map[InstanceID]*instanceState),
		limits:    make(map[LinkTypeID]int),
	}
}

// SetLinkLimit 设置某类关联在每个源实例上的最大目标数（基数约束）。
func (s *Store) SetLinkLimit(t LinkTypeID, max int) {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	s.limits[t] = max
}

// Create 以初始版本 1 创建实例；已存在则返回 false。
func (s *Store) Create(id InstanceID) bool {
	st := s.getOrCreate(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.version != 0 {
		return false
	}
	st.version = 1
	return true
}

// SnapshotOf 返回实例当前状态的一致性快照。
func (s *Store) SnapshotOf(id InstanceID) (Snapshot, bool) {
	st := s.getOrCreate(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.version == 0 {
		return Snapshot{}, false
	}
	return snapshotOf(st), true
}

// ApplyBatch 执行一次跨实例批量更新，返回完整判定记录。
//
// 判定顺序（互斥，靠前者优先）：
//  1. 批次内重复前置声明；
//  2. 联合版本前置判定（所有实例版本在同一逻辑时刻读取）；
//  3. 基数约束判定（在内存副本上预演，不落盘）；
//  4. 整体提交，各实例版本各自 +1。
//
// 任何拒绝路径都不会修改任何实例的版本、属性、关联或时钟状态。
func (s *Store) ApplyBatch(b Batch) Decision {
	dec := Decision{BatchID: b.ID, Items: b.Items, Observed: make(map[InstanceID]Version)}

	// 第 1 步：批次内重复前置声明检查，不触碰存储。
	seen := make(map[InstanceID]int, len(b.Items))
	for _, it := range b.Items {
		seen[it.Instance]++
		if seen[it.Instance] > 1 {
			dec.Outcome = OutcomeDuplicatePrecondition
			dec.Detail = fmt.Sprintf("实例 %q 在批次内被重复声明 %d 次", it.Instance, seen[it.Instance])
			return s.record(dec)
		}
	}

	// 第 2 步：按实例 ID 全局一致的顺序加锁（有序两阶段锁，避免死锁），
	// 锁全部持有期间即"同一个逻辑时刻"。
	items := slices.Clone(b.Items)
	sort.Slice(items, func(i, j int) bool { return items[i].Instance < items[j].Instance })
	states := make([]*instanceState, len(items))
	for i, it := range items {
		states[i] = s.getOrCreate(it.Instance)
	}
	for _, st := range states {
		st.mu.Lock()
	}
	defer func() {
		for _, st := range states {
			st.mu.Unlock()
		}
	}()

	// 判定逻辑时刻取号：任何与本批次共享实例的其他批次，
	// 其取号必然发生在本批次持锁区间之外，因此 Tick 序即等价串行序。
	dec.Tick = s.nextTick()

	// 联合版本前置判定：一次性读取全部相关实例的当前版本。
	dec.Reads = len(items)
	for i, it := range items {
		dec.Observed[it.Instance] = states[i].version
	}
	for i, it := range items {
		if states[i].version != it.Expect {
			dec.Outcome = OutcomeVersionConflict
			dec.Detail = fmt.Sprintf("实例 %q 期望版本 %d，实际版本 %d",
				it.Instance, it.Expect, states[i].version)
			return s.record(dec)
		}
	}

	// 第 3 步：在内存副本上预演关联变更并检查基数约束，不写回。
	if detail, ok := s.checkCardinality(items, states); !ok {
		dec.Outcome = OutcomeCardinalityViolation
		dec.Detail = detail
		return s.record(dec)
	}

	// 第 4 步：整体提交。每个实例的版本号按自身序列独立 +1。
	for i, it := range items {
		st := states[i]
		maps.Copy(st.attrs, it.SetAttrs)
		for _, l := range it.RemoveLinks {
			if set, ok := st.links[l.Type]; ok {
				delete(set, l.Target)
			}
		}
		for _, l := range it.AddLinks {
			set, ok := st.links[l.Type]
			if !ok {
				set = make(map[InstanceID]struct{})
				st.links[l.Type] = set
			}
			set[l.Target] = struct{}{}
		}
		st.version++
	}
	dec.Outcome = OutcomeCommitted
	dec.Detail = fmt.Sprintf("已提交，涉及 %d 个实例", len(items))
	dec.CommitSeq = s.nextCommitSeq()
	return s.record(dec)
}

// Decisions 返回判定日志的完整副本（按判定逻辑时刻的全局序号排序）。
func (s *Store) Decisions() []Decision {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	out := make([]Decision, len(s.log))
	copy(out, s.log)
	return out
}

// getOrCreate 取实例状态，不存在则登记一个零值状态（version=0 表示未创建）。
func (s *Store) getOrCreate(id InstanceID) *instanceState {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	st, ok := s.instances[id]
	if !ok {
		st = &instanceState{
			attrs: make(map[string]string),
			links: make(map[LinkTypeID]map[InstanceID]struct{}),
		}
		s.instances[id] = st
	}
	return st
}

// checkCardinality 在内存副本上预演本批次的关联变更，
// 返回（违例描述，是否满足）。调用时必须已持有全部相关实例的锁。
// 只检查本批次实际触及的（实例, 链接类型）对，开销与批次大小成正比。
func (s *Store) checkCardinality(items []Item, states []*instanceState) (string, bool) {
	type linkKey struct {
		inst InstanceID
		lt   LinkTypeID
	}
	counts := make(map[linkKey]int)
	count := func(i int, lt LinkTypeID) int {
		k := linkKey{items[i].Instance, lt}
		n, ok := counts[k]
		if !ok {
			n = len(states[i].links[lt])
			counts[k] = n
		}
		return n
	}
	for i, it := range items {
		touched := make(map[LinkTypeID]struct{}, len(it.AddLinks)+len(it.RemoveLinks))
		for _, l := range it.RemoveLinks {
			touched[l.Type] = struct{}{}
		}
		for _, l := range it.AddLinks {
			touched[l.Type] = struct{}{}
		}
		for lt := range touched {
			limit, bounded := s.limits[lt]
			if !bounded {
				continue
			}
			n := count(i, lt)
			for _, l := range it.RemoveLinks {
				if l.Type == lt {
					if _, ok := states[i].links[lt][l.Target]; ok {
						n--
					}
				}
			}
			added := make(map[InstanceID]struct{}, len(it.AddLinks))
			for _, l := range it.AddLinks {
				if l.Type != lt {
					continue
				}
				if _, dup := added[l.Target]; dup {
					continue
				}
				added[l.Target] = struct{}{}
				if _, ok := states[i].links[lt][l.Target]; !ok {
					n++
				}
			}
			counts[linkKey{it.Instance, lt}] = n
			if n > limit {
				return fmt.Sprintf("实例 %q 的关联 %q 基数 %d 超过上限 %d",
					it.Instance, lt, n, limit), false
			}
		}
	}
	return "", true
}

// nextTick 取判定逻辑时刻序号。
func (s *Store) nextTick() uint64 {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.clock++
	return s.clock
}

// nextCommitSeq 取提交序号。
func (s *Store) nextCommitSeq() uint64 {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.commitSeq++
	return s.commitSeq
}

// record 把判定记录追加到判定日志。重复声明路径未持锁，在此补取 Tick；
// 其结果与串行位置无关，任意位置重放结论一致。
func (s *Store) record(dec Decision) Decision {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if dec.Tick == 0 {
		s.clock++
		dec.Tick = s.clock
	}
	s.log = append(s.log, dec)
	return dec
}

// snapshotOf 拷贝实例状态为只读视图，调用方须持有 st.mu。
func snapshotOf(st *instanceState) Snapshot {
	snap := Snapshot{
		Version: st.version,
		Attrs:   maps.Clone(st.attrs),
		Links:   make(map[LinkTypeID][]InstanceID, len(st.links)),
	}
	for lt, set := range st.links {
		targets := make([]InstanceID, 0, len(set))
		for t := range set {
			targets = append(targets, t)
		}
		slices.Sort(targets)
		snap.Links[lt] = targets
	}
	return snap
}
