package ontology

import "sync"

// Store 保存对象类型、对象实例、主体与权限条目，
// 通过单一互斥锁保证并发批量导入等价于某个全局串行顺序。
type Store struct {
	mu          sync.Mutex
	types       map[string]ObjectType
	objects     map[string]map[string]*Object // typeName -> id -> object
	subjects    map[string]bool
	permissions []PermissionEntry
	permIndex   map[[3]string]int // (subject, type, property) -> permissions 下标
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{
		types:       make(map[string]ObjectType),
		objects:     make(map[string]map[string]*Object),
		subjects:    make(map[string]bool),
		permissions: nil,
		permIndex:   make(map[[3]string]int),
	}
}

// AddObjectType 注册对象类型。
func (s *Store) AddObjectType(t ObjectType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.types[t.Name] = t
	if _, ok := s.objects[t.Name]; !ok {
		s.objects[t.Name] = make(map[string]*Object)
	}
}

// AddSubject 注册合法主体。
func (s *Store) AddSubject(subject string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subjects[subject] = true
}

// PutObject 直接写入对象（用于构造初始状态）。
func (s *Store) PutObject(obj Object) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putObjectLocked(obj)
}

func (s *Store) putObjectLocked(obj Object) {
	bucket, ok := s.objects[obj.TypeName]
	if !ok {
		bucket = make(map[string]*Object)
		s.objects[obj.TypeName] = bucket
	}
	props := make(map[string]string, len(obj.Props))
	for k, v := range obj.Props {
		props[k] = v
	}
	stored := obj
	stored.Props = props
	bucket[obj.ID] = &stored
}

// GetObject 读取对象快照；不存在时 ok 为 false。
func (s *Store) GetObject(typeName, id string) (Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getObjectLocked(typeName, id)
}

func (s *Store) getObjectLocked(typeName, id string) (Object, bool) {
	bucket, ok := s.objects[typeName]
	if !ok {
		return Object{}, false
	}
	obj, ok := bucket[id]
	if !ok {
		return Object{}, false
	}
	props := make(map[string]string, len(obj.Props))
	for k, v := range obj.Props {
		props[k] = v
	}
	out := *obj
	out.Props = props
	return out, true
}

// SnapshotObjects 导出全部对象状态的深拷贝，用于结果比对与验证。
func (s *Store) SnapshotObjects() map[string]map[string]map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]map[string]map[string]string, len(s.objects))
	for typeName, bucket := range s.objects {
		copied := make(map[string]map[string]string, len(bucket))
		for id, obj := range bucket {
			props := make(map[string]string, len(obj.Props))
			for k, v := range obj.Props {
				props[k] = v
			}
			copied[id] = props
		}
		out[typeName] = copied
	}
	return out
}

// SetPermission 追加或覆盖一条权限条目（后写覆盖先写）。
func (s *Store) SetPermission(entry PermissionEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := [3]string{entry.Subject, entry.ObjectType, entry.Property}
	if idx, ok := s.permIndex[key]; ok {
		s.permissions[idx].Writable = entry.Writable
		return
	}
	s.permIndex[key] = len(s.permissions)
	s.permissions = append(s.permissions, entry)
}

// PermissionSnapshot 是批量导入发起时刻的权限状态快照，
// 以 (主体, 类型, 属性) 为键的哈希索引，单次判定为 O(1) 次条目访问。
type PermissionSnapshot struct {
	grants map[[3]string]bool
	// accesses 统计判定过程中访问权限条目的次数，用于验证
	// 单条记录的判定开销与批规模、历史条目总数无关。
	accesses int
}

// snapshotPermissionsLocked 在发起时刻物化权限快照。
// 调用方须已持有锁或处于串行上下文。
func (s *Store) snapshotPermissionsLocked() *PermissionSnapshot {
	grants := make(map[[3]string]bool, len(s.permissions))
	for _, p := range s.permissions {
		grants[[3]string{p.Subject, p.ObjectType, p.Property}] = p.Writable
	}
	return &PermissionSnapshot{grants: grants}
}

// Writable 查询主体对某类型某属性是否可写；每次调用计一次条目访问。
func (snap *PermissionSnapshot) Writable(subject, objectType, property string) bool {
	snap.accesses++
	return snap.grants[[3]string{subject, objectType, property}]
}

// Accesses 返回快照累计的权限条目访问次数。
func (snap *PermissionSnapshot) Accesses() int {
	return snap.accesses
}
