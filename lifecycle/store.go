package lifecycle

import (
	"fmt"
	"sort"
	"sync"
)

// Instance 是一个对象实例的当前可观察状态。
type Instance struct {
	ID      InstanceID
	Type    string
	State   State
	Attrs   map[AttrKey]AttrValue
	Clock   uint64
	Version uint64 // 任何状态/属性/链接变动都会递增，用于乐观读集校验
}

func (i *Instance) clone() *Instance {
	cp := &Instance{ID: i.ID, Type: i.Type, State: i.State, Clock: i.Clock, Version: i.Version}
	if len(i.Attrs) > 0 {
		cp.Attrs = make(map[AttrKey]AttrValue, len(i.Attrs))
		for k, v := range i.Attrs {
			cp.Attrs[k] = v
		}
	}
	return cp
}

// Store 保存全部实例与有向链接，并提供按实例加锁、稳定顺序的多实例加锁
// （按 InstanceID 升序，避免死锁）以及乐观并发控制所需的读集版本。
//
// 复杂度约定：引擎从不扫描历史迁移记录，只访问实例的当前状态、属性与其
// 邻接链接；单次判定开销只随本次迁移实际涉及的关联实例数量增长。
type Store struct {
	mu        sync.RWMutex
	types     map[string]*ObjectType
	instances map[InstanceID]*Instance

	// links[src][linkType] -> set(dst)
	links map[InstanceID]map[LinkType]map[InstanceID]struct{}
	// back[dst][linkType] -> set(src)
	back map[InstanceID]map[LinkType]map[InstanceID]struct{}

	locks   map[InstanceID]*sync.Mutex
	locksMu sync.Mutex
	clock   uint64
}

func NewStore() *Store {
	return &Store{
		types:     map[string]*ObjectType{},
		instances: map[InstanceID]*Instance{},
		links:     map[InstanceID]map[LinkType]map[InstanceID]struct{}{},
		back:      map[InstanceID]map[LinkType]map[InstanceID]struct{}{},
		locks:     map[InstanceID]*sync.Mutex{},
	}
}

func (s *Store) RegisterType(t *ObjectType) error {
	if t == nil || t.Name == "" {
		return fmt.Errorf("lifecycle: object type must have a name")
	}
	seenStates := map[State]bool{}
	for _, st := range t.States {
		if seenStates[st] {
			return fmt.Errorf("lifecycle: duplicate state %q in type %q", st, t.Name)
		}
		seenStates[st] = true
	}
	if !seenStates[t.Initial] {
		return fmt.Errorf("lifecycle: initial state %q not declared in type %q", t.Initial, t.Name)
	}
	for _, term := range t.TerminalsList {
		if !seenStates[term] {
			return fmt.Errorf("lifecycle: terminal state %q not declared in type %q", term, t.Name)
		}
	}
	ruleNames := map[string]bool{}
	for name, r := range t.Transitions {
		if r == nil {
			return fmt.Errorf("lifecycle: nil transition %q in type %q", name, t.Name)
		}
		if r.Name == "" {
			r.Name = name
		}
		if ruleNames[r.Name] {
			return fmt.Errorf("lifecycle: duplicate transition name %q in type %q", r.Name, t.Name)
		}
		ruleNames[r.Name] = true
		if !seenStates[r.To] {
			return fmt.Errorf("lifecycle: transition %q targets undeclared state %q", r.Name, r.To)
		}
		for _, from := range r.From {
			if !seenStates[from] {
				return fmt.Errorf("lifecycle: transition %q from undeclared state %q", r.Name, from)
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.types[t.Name] = t
	return nil
}

func (s *Store) CreateInstance(id InstanceID, typ string) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.instances[id]; exists {
		return nil, fmt.Errorf("lifecycle: instance %q already exists", id)
	}
	t, ok := s.types[typ]
	if !ok {
		return nil, fmt.Errorf("lifecycle: object type %q not registered", typ)
	}
	inst := &Instance{
		ID:      id,
		Type:    typ,
		State:   t.Initial,
		Attrs:   map[AttrKey]AttrValue{},
		Clock:   0,
		Version: 0,
	}
	s.instances[id] = inst
	return inst.clone(), nil
}

// snapshotInstance 返回实例当前状态的深拷贝（调用方需持有 RLock 或自行加锁）。
func (s *Store) snapshotInstanceLocked(id InstanceID) (*Instance, bool) {
	inst, ok := s.instances[id]
	if !ok {
		return nil, false
	}
	return inst.clone(), true
}

// SnapshotInstance 返回实例当前状态的深拷贝。
func (s *Store) SnapshotInstance(id InstanceID) (*Instance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotInstanceLocked(id)
}

// typeLocked 返回对象类型（调用方需持有 RLock）。
func (s *Store) typeLocked(typeName string) *ObjectType {
	return s.types[typeName]
}

// TypeOf 返回实例对应的对象类型。
func (s *Store) TypeOf(id InstanceID) (*ObjectType, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.instances[id]
	if !ok {
		return nil, false
	}
	t := s.types[inst.Type]
	return t, t != nil
}

// neighborsLocked 返回出向邻居集合的快照（调用方需持有 RLock）。
func (s *Store) neighborsLocked(src InstanceID, link LinkType) []InstanceID {
	out := []InstanceID{}
	if m, ok := s.links[src]; ok {
		for dst := range m[link] {
			out = append(out, dst)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Neighbors 返回出向邻居（稳定排序，保证求值确定）。
func (s *Store) Neighbors(src InstanceID, link LinkType) []InstanceID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.neighborsLocked(src, link)
}

// lockFor 返回实例的专用锁。
func (s *Store) lockFor(id InstanceID) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	mu, ok := s.locks[id]
	if !ok {
		mu = &sync.Mutex{}
		s.locks[id] = mu
	}
	return mu
}

// lockAll 按 ID 升序锁定给定实例（去重），返回解锁函数（逆序解锁）。
func (s *Store) lockAll(ids []InstanceID) func() {
	uniq := uniqueSortedIDs(ids)
	locked := make([]*sync.Mutex, 0, len(uniq))
	for _, id := range uniq {
		mu := s.lockFor(id)
		mu.Lock()
		locked = append(locked, mu)
	}
	return func() {
		for i := len(locked) - 1; i >= 0; i-- {
			locked[i].Unlock()
		}
	}
}

func uniqueSortedIDs(ids []InstanceID) []InstanceID {
	seen := map[InstanceID]bool{}
	out := make([]InstanceID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// RLock/RUnlock 暴露给引擎的读阶段使用。
func (s *Store) RLock()   { s.mu.RLock() }
func (s *Store) RUnlock() { s.mu.RUnlock() }
func (s *Store) Lock()    { s.mu.Lock() }
func (s *Store) Unlock()  { s.mu.Unlock() }

// bumpClock 在提交阶段推进处理单元时钟。
func (s *Store) nextClockLocked() uint64 {
	s.clock++
	return s.clock
}
