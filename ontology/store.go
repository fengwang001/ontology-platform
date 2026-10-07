package ontology

import "sync"

// Object 是本体对象：一组整型字段的集合。
type Object struct {
	ID     string
	Fields map[string]int
	// Version 每次被任意写操作修改时递增。补偿续作不以 Version 作为
	// 是否继续的依据（见 DESIGN.md：无关并发修改共存规则）。
	Version int64
}

// ObjectStore 是本体对象的并发安全存储。
type ObjectStore struct {
	mu      sync.Mutex
	objects map[string]*Object
}

// NewObjectStore 构造空对象存储。
func NewObjectStore() *ObjectStore {
	return &ObjectStore{objects: make(map[string]*Object)}
}

// Put 创建或整体替换一个对象。
func (s *ObjectStore) Put(obj Object) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fields := make(map[string]int, len(obj.Fields))
	for k, v := range obj.Fields {
		fields[k] = v
	}
	s.objects[obj.ID] = &Object{ID: obj.ID, Fields: fields, Version: obj.Version}
}

// Get 返回对象的快照；不存在时 ok=false。
func (s *ObjectStore) Get(id string) (obj Object, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, found := s.objects[id]
	if !found {
		return Object{}, false
	}
	return snapshot(o), true
}

// Exists 报告对象是否存在。
func (s *ObjectStore) Exists(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, found := s.objects[id]
	return found
}

// apply 在 store 锁内施加一项副作用。返回 false 表示目标对象不存在。
// 该操作不检查对象版本，因此与无关的并发修改天然共存（可串行化由
// 调用方的全局顺序保证）。
func (s *ObjectStore) apply(e SideEffect) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, found := s.objects[e.ObjectID]
	if !found {
		return false
	}
	switch e.Op {
	case OpSet:
		o.Fields[e.Field] = e.Value
	case OpAdd:
		o.Fields[e.Field] += e.Value
	case OpDelete:
		delete(s.objects, e.ObjectID)
		return true
	}
	o.Version++
	return true
}

// Snapshot 返回全部对象的快照副本（按对象 ID 排序的映射）。
func (s *ObjectStore) Snapshot() map[string]Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Object, len(s.objects))
	for id, o := range s.objects {
		out[id] = snapshot(o)
	}
	return out
}

func snapshot(o *Object) Object {
	fields := make(map[string]int, len(o.Fields))
	for k, v := range o.Fields {
		fields[k] = v
	}
	return Object{ID: o.ID, Fields: fields, Version: o.Version}
}
