package ontology

// Instance 是对象类型的一个实例。Values 中缺失的键表示该字段取值缺失。
type Instance struct {
	ID     string
	Type   string
	Values map[string]Value
}

// Clone 深拷贝实例，避免测试与引擎之间共享内部状态。
func (in *Instance) Clone() Instance {
	vals := make(map[string]Value, len(in.Values))
	for k, v := range in.Values {
		vals[k] = v
	}
	return Instance{ID: in.ID, Type: in.Type, Values: vals}
}

// InstanceStore 只保存仍然存活的实例。
// 被逻辑删除或被迁移走的实例会从索引中物理移除，
// 因此任何按对象类型的遍历开销只与当前存活总数有关，
// 与历史上曾经存在过的实例总量无关。
type InstanceStore struct {
	live map[string]map[string]*Instance // objectType -> instanceID -> instance
}

func NewInstanceStore() *InstanceStore {
	return &InstanceStore{live: make(map[string]map[string]*Instance)}
}

// Put 写入或覆盖一个存活实例。
func (s *InstanceStore) Put(in Instance) {
	set, ok := s.live[in.Type]
	if !ok {
		set = make(map[string]*Instance)
		s.live[in.Type] = set
	}
	c := in.Clone()
	set[in.ID] = &c
}

// Delete 逻辑删除一个实例：从存活索引中物理移除。
func (s *InstanceStore) Delete(objectType, id string) {
	if set, ok := s.live[objectType]; ok {
		delete(set, id)
	}
}

// MigrateOut 把一个实例迁移出本存储：同样从存活索引中物理移除。
func (s *InstanceStore) MigrateOut(objectType, id string) {
	s.Delete(objectType, id)
}

// Get 返回存活实例的拷贝；不存在时 ok=false。
func (s *InstanceStore) Get(objectType, id string) (Instance, bool) {
	if set, ok := s.live[objectType]; ok {
		if in, ok := set[id]; ok {
			return in.Clone(), true
		}
	}
	return Instance{}, false
}

// LiveCount 返回某对象类型当前实际存活的实例总数。
func (s *InstanceStore) LiveCount(objectType string) int {
	return len(s.live[objectType])
}

// EachLive 按存活索引逐个访问实例，绝不触碰已删除/已迁移的实例。
// 回调返回 false 可提前终止。
func (s *InstanceStore) EachLive(objectType string, fn func(*Instance) bool) {
	for _, in := range s.live[objectType] {
		if !fn(in) {
			return
		}
	}
}

// ObjectType 是一个对象类型的当前版本定义。
type ObjectType struct {
	Name   string
	Fields map[string]*FieldDef
}

func NewObjectType(name string) *ObjectType {
	return &ObjectType{Name: name, Fields: make(map[string]*FieldDef)}
}

func (ot *ObjectType) field(name string) *FieldDef {
	return ot.Fields[name]
}
