package ontology

import "sync"

// Value 是字段值。受支持的动态类型为
// int64 / float64 / string / bool，与 FieldType 一一对应。
type Value = any

// Instance 是一个已存在实例的完整字段集合。
// map 视为只读值，更新时整体替换，不做原地修改。
type Instance map[string]Value

// TypeState 是单个对象类型的已提交状态。
type TypeState struct {
	Instances map[string]Instance
	// Version 在每次该类型发生提交后单调递增，用于乐观并发检测。
	Version int64
}

// Snapshot 是批次开始时拿到的一致性只读快照。
// 钩子可见的「已存在实例状态」始终来自快照，
// 从而不会读到其他并发批次未提交或交错提交的中间效果。
type Snapshot struct {
	types        map[string]*TypeState
	baseVersions map[string]int64
}

// Get 读取某类型下某主键的已提交实例，返回（实例, 是否存在）。
// 这是一次哈希查询，开销 O(1)。
func (s *Snapshot) Get(typeName, pk string) (Instance, bool) {
	ts, ok := s.types[typeName]
	if !ok {
		return nil, false
	}
	inst, ok := ts.Instances[pk]
	return inst, ok
}

// State 返回某类型的已提交状态（测试/朴素模型用）。
func (s *Snapshot) State(typeName string) *TypeState {
	return s.types[typeName]
}

// Store 是全平台已提交状态的持有者。
type Store struct {
	mu    sync.Mutex
	types map[string]*TypeState
}

// NewStore 创建空存储，并为每个已注册类型建立状态行。
func NewStore(reg *Registry) *Store {
	st := &Store{types: map[string]*TypeState{}}
	for name := range reg.types {
		st.types[name] = &TypeState{Instances: map[string]Instance{}, Version: 0}
	}
	return st
}

// Snapshot 取当前已提交状态的一致性只读快照。
// 实例映射做一层拷贝（Instance 本身只读），
// 保证后续提交不会原地改变钩子看到的内容。
func (st *Store) Snapshot() *Snapshot {
	st.mu.Lock()
	defer st.mu.Unlock()
	snap := &Snapshot{
		types:        map[string]*TypeState{},
		baseVersions: map[string]int64{},
	}
	for name, ts := range st.types {
		copied := make(map[string]Instance, len(ts.Instances))
		for pk, inst := range ts.Instances {
			c := make(Instance, len(inst))
			for f, val := range inst {
				c[f] = val
			}
			copied[pk] = c
		}
		snap.types[name] = &TypeState{Instances: copied, Version: ts.Version}
		snap.baseVersions[name] = ts.Version
	}
	return snap
}

// Commit 在全局互斥下提交单类型的最终写入。
// changed 为待写入的实例映射（已含全部成功记录效果）；
// inst 为 nil 表示删除（当前导入语义不产生删除，保留扩展）。
// 若快照基线版本已被其他并发批次推进，返回 false 表示冲突，
// 调用方必须基于新快照整批重放；冲突时本方法不改变任何状态。
func (st *Store) Commit(snap *Snapshot, typeName string, changed map[string]Instance) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	cur, ok := st.types[typeName]
	if !ok {
		cur = &TypeState{Instances: map[string]Instance{}}
		st.types[typeName] = cur
	}
	if cur.Version != snap.baseVersions[typeName] {
		return false
	}
	for pk, inst := range changed {
		if inst == nil {
			delete(cur.Instances, pk)
		} else {
			// 存入独立拷贝，切断 store 与批内 overlay 的引用别名。
			c := make(Instance, len(inst))
			for f, val := range inst {
				c[f] = val
			}
			cur.Instances[pk] = c
		}
	}
	cur.Version++
	return true
}

// CurrentInstances 返回某类型当前实例映射的拷贝（测试断言用）。
func (st *Store) CurrentInstances(typeName string) map[string]Instance {
	st.mu.Lock()
	defer st.mu.Unlock()
	ts, ok := st.types[typeName]
	if !ok {
		return map[string]Instance{}
	}
	out := make(map[string]Instance, len(ts.Instances))
	for k, v := range ts.Instances {
		out[k] = v
	}
	return out
}

// CurrentVersion 返回某类型当前版本号（测试断言用）。
func (st *Store) CurrentVersion(typeName string) int64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	if ts, ok := st.types[typeName]; ok {
		return ts.Version
	}
	return 0
}
