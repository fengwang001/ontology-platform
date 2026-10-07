package ontology

import "sync"

// Object 是本体平台中的一个持久化对象。
type Object struct {
	ID      string
	Type    string
	Props   map[string]any
	Version uint64
}

// Invariant 校验某个对象应当保持的不变量，返回非 nil 表示违反。
type Invariant func(obj Object) error

// Store 是持久化对象存储，提交在其互斥锁内原子完成。
type Store struct {
	mu         sync.Mutex
	objects    map[string]Object
	version    uint64
	commitSeq  uint64
	invariants map[string][]Invariant
}

func NewStore() *Store {
	return &Store{objects: map[string]Object{}, invariants: map[string][]Invariant{}}
}

func (s *Store) RegisterInvariant(objType string, inv Invariant) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invariants[objType] = append(s.invariants[objType], inv)
}

// Seed 直接写入一个对象（测试与初始化用），绕过动作执行流程。
func (s *Store) Seed(obj Object) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	obj.Version = s.version
	s.objects[obj.ID] = obj
}

func (s *Store) Get(id string) (Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	return obj, ok
}

// Snapshot 返回当前状态的一致性只读快照。
func (s *Store) Snapshot() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	objects := make(map[string]Object, len(s.objects))
	for id, obj := range s.objects {
		objects[id] = obj
	}
	return &Snapshot{objects: objects, version: s.version}
}

// commit 在持锁状态下原子地完成：读集冲突检测 -> 不变量校验 -> 应用写入计划。
// 返回值分别为提交序号、是否发生读写冲突（调用方应重试）、不变量错误（调用方应放弃）。
func (s *Store) commit(reads map[string]uint64, plan *WritePlan) (seq uint64, conflict bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ver := range reads {
		cur, ok := s.objects[id]
		if !ok {
			if ver != 0 {
				return 0, true, nil
			}
			continue
		}
		if cur.Version != ver {
			return 0, true, nil
		}
	}
	for _, id := range plan.order {
		obj, ok := plan.puts[id]
		if !ok {
			continue
		}
		for _, inv := range s.invariants[obj.Type] {
			if ierr := inv(obj); ierr != nil {
				return 0, false, ierr
			}
		}
	}
	s.version++
	for _, id := range plan.order {
		if obj, ok := plan.puts[id]; ok {
			obj.Version = s.version
			s.objects[id] = obj
		} else {
			delete(s.objects, id)
		}
	}
	s.commitSeq++
	return s.commitSeq, false, nil
}

// Snapshot 是某一版本的一致性只读视图。
type Snapshot struct {
	objects map[string]Object
	version uint64
}

func (sn *Snapshot) Version() uint64 { return sn.version }

func (sn *Snapshot) get(id string) (Object, bool) {
	obj, ok := sn.objects[id]
	return obj, ok
}

// WritePlan 是一次执行链条中尚未提交的写入计划。
type WritePlan struct {
	puts  map[string]Object
	dels  map[string]struct{}
	order []string
}

func newWritePlan() *WritePlan {
	return &WritePlan{puts: map[string]Object{}, dels: map[string]struct{}{}}
}

func (p *WritePlan) put(obj Object) {
	if _, ok := p.puts[obj.ID]; !ok {
		if _, deleted := p.dels[obj.ID]; !deleted {
			p.order = append(p.order, obj.ID)
		}
	}
	delete(p.dels, obj.ID)
	p.puts[obj.ID] = obj
}

func (p *WritePlan) del(id string) {
	if _, ok := p.puts[id]; ok {
		delete(p.puts, id)
		p.dels[id] = struct{}{}
		return
	}
	if _, ok := p.dels[id]; !ok {
		p.dels[id] = struct{}{}
		p.order = append(p.order, id)
	}
}

// get 查询写入计划：decided 表示计划对该对象已有定论（写入或删除）。
func (p *WritePlan) get(id string) (obj Object, exists bool, decided bool) {
	if obj, ok := p.puts[id]; ok {
		return obj, true, true
	}
	if _, ok := p.dels[id]; ok {
		return Object{}, false, true
	}
	return Object{}, false, false
}

// merge 把子调用的写入计划并入当前计划（子调用成功提交到外层时调用）。
func (p *WritePlan) merge(child *WritePlan) {
	for _, id := range child.order {
		if obj, ok := child.puts[id]; ok {
			p.put(obj)
		} else {
			p.del(id)
		}
	}
}

func (p *WritePlan) keys() []string {
	out := make([]string, 0, len(p.order))
	out = append(out, p.order...)
	return out
}

// View 是求值视图：持久化快照叠加若干层未提交写入计划（内层优先）。
type View struct {
	base   *Snapshot
	layers []*WritePlan
	reads  map[string]uint64
}

// Get 先查各层未提交写入计划（内层优先），再回落到持久化快照；
// 回落到快照的读取会记入读集，供提交时的冲突检测使用。
func (v *View) Get(id string) (Object, bool) {
	for i := len(v.layers) - 1; i >= 0; i-- {
		if obj, exists, decided := v.layers[i].get(id); decided {
			return obj, exists
		}
	}
	obj, ok := v.base.get(id)
	if ok {
		v.reads[id] = obj.Version
	} else {
		v.reads[id] = 0
	}
	return obj, ok
}

// basis 汇总当前视图的求值依据：快照版本 + 各层可见的未提交写入键。
func (v *View) basis() Basis {
	b := Basis{BaseVersion: v.base.version}
	for _, layer := range v.layers {
		b.PendingWrites = append(b.PendingWrites, layer.keys()...)
	}
	return b
}
