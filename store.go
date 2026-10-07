package ontology

import "sync"

// objectType 是一个已注册对象类型的运行期结构：schema + 该类型的全部实例。
type objectType struct {
	schema  ObjectTypeSchema
	records map[string]*Record // key -> 当前可见版本（含墓碑）
}

// store 是「实例版本仲裁模块」：负责实例当前可见版本的保存与读取，
// 以及按主键的点查。乐观并发凭证的判定与拒绝次序由内核 (kernel.go)
// 在唯一提交锁内编排；store 本身不做策略，只提供串行化的读写原语，
// 从而保证多个写入/删除的效果等价于某个全局串行顺序逐一应用。
type store struct {
	mu     sync.RWMutex
	types  map[string]*objectType
	views  map[string][]AggregateDef // typeName -> 挂在该类型上的视图定义
	viewOf map[string]AggregateDef   // viewName -> 定义
}

func newStore() *store {
	return &store{
		types:  map[string]*objectType{},
		views:  map[string][]AggregateDef{},
		viewOf: map[string]AggregateDef{},
	}
}

// registerType 登记一个对象类型。
func (s *store) registerType(schema ObjectTypeSchema) {
	s.types[schema.Name] = &objectType{schema: schema, records: map[string]*Record{}}
}

// registerView 登记一个定义在某对象类型之上的聚合视图。
func (s *store) registerView(def AggregateDef) {
	s.views[def.SourceType] = append(s.views[def.SourceType], def)
	s.viewOf[def.Name] = def
}

func (s *store) objectTypeByName(name string) (*objectType, bool) {
	ot, ok := s.types[name]
	return ot, ok
}

// get 按主键点查实例的当前可见版本。不存在返回 nil, false。
// 任何对源实例记录的访问都计入 meter，用于以可验证的方式证明：
// 聚合查询路径对源实例的读取次数为 0（与实例总数无关）。
func (s *store) get(typeName, key string, meter *CostMeter) (*Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ot, ok := s.types[typeName]
	if !ok {
		return nil, false
	}
	rec, ok := ot.records[key]
	if meter != nil {
		meter.InstanceRecordReads++
	}
	if !ok {
		return nil, false
	}
	// 返回副本，调用方在锁外/锁内均不会因共享指针产生别名修改。
	cp := *rec
	cp.Attrs = cloneAttrs(rec.Attrs)
	return &cp, true
}

// put 在提交锁保护下写入实例的新版本（含墓碑版本）。版本号由内核仲裁后
// 传入，store 不自行分配，从而保证「被拒绝的操作不占用版本号」。
func (s *store) put(typeName string, rec *Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ot := s.types[typeName]
	stored := *rec
	stored.Attrs = cloneAttrs(rec.Attrs)
	ot.records[rec.Key] = &stored
}

// scanType 返回某类型下全部存活实例的快照副本。仅供聚合索引的
// 全量重建 (rebuild) 与调试/自校验使用，绝不出现在正常查询路径上。
func (s *store) scanType(typeName string) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ot, ok := s.types[typeName]
	if !ok {
		return nil
	}
	out := make([]Record, 0, len(ot.records))
	for _, rec := range ot.records {
		cp := *rec
		cp.Attrs = cloneAttrs(rec.Attrs)
		out = append(out, cp)
	}
	return out
}

func cloneAttrs(in map[string]Value) map[string]Value {
	if in == nil {
		return nil
	}
	out := make(map[string]Value, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
