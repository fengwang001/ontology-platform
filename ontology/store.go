package ontology

import "sort"

// ObjectType 描述一个对象类型的属性 schema。
type ObjectType struct {
	Name  string
	Attrs map[string]struct{}
}

// HasAttr 报告属性名是否已在该类型登记。
func (t *ObjectType) HasAttr(name string) bool {
	_, ok := t.Attrs[name]
	return ok
}

// versionedValue 是某属性的一次带版本赋值。
type versionedValue struct {
	version uint64
	value   Value
}

// attrHistory 保存单个属性的版本链（version 严格递增）。
type attrHistory struct {
	versions []versionedValue
}

// at 返回 version 快照下可见的取值：最后一个提交版本 <= version 的记录。
func (h *attrHistory) at(version uint64) (Value, bool) {
	i := sort.Search(len(h.versions), func(i int) bool {
		return h.versions[i].version > version
	})
	if i == 0 {
		return nil, false
	}
	return h.versions[i-1].value, true
}

// instance 是一个对象实例的全部属性版本链。
type instance struct {
	attrs map[string]*attrHistory
}

type instanceKey struct {
	typeName string
	id       string
}

// store 是 MVCC 存储。它自身不加锁，由 Engine 的互斥锁串行化访问。
//
// 快照选取规则（确定且可复现）：快照即创建时刻的全局提交时钟值；
// 快照 s 上读取属性 a 的结果 = a 的版本链中提交版本 <= s 的最后一条记录。
type store struct {
	clock     uint64
	types     map[string]*ObjectType
	instances map[instanceKey]*instance
	// lowWater 是可读水位：版本 < lowWater 的快照已过期。
	lowWater uint64
	// keepVersions 是每条属性版本链至少保留的版本数（GC 参数）。
	keepVersions int
}

func newStore() *store {
	return &store{
		types:        make(map[string]*ObjectType),
		instances:    make(map[instanceKey]*instance),
		keepVersions: 8,
	}
}

// Snapshot 是一次可重复读声明。零值 Snapshot 表示“读最新已提交状态”。
type Snapshot struct {
	version uint64
	latest  bool
}

// Version 返回快照绑定的提交时钟值；最新读快照返回当前时钟。
func (s Snapshot) Version() uint64 { return s.version }

// IsLatest 报告该快照是否表示“读最新”。
func (s Snapshot) IsLatest() bool { return s.latest }

// beginSnapshot 在当前提交点开启可重复读快照。
func (st *store) beginSnapshot() Snapshot {
	return Snapshot{version: st.clock}
}

// resolve 把快照解析为具体的读取版本；过期快照返回 ErrKindSnapshotExpired。
func (st *store) resolve(s Snapshot) (uint64, error) {
	if s.latest {
		return st.clock, nil
	}
	if s.version < st.lowWater {
		return 0, newError(ErrKindSnapshotExpired,
			"快照版本 %d 已过期（当前可读水位 %d）", s.version, st.lowWater)
	}
	return s.version, nil
}

// get 在读取版本 version 下取属性取值。
func (st *store) get(key instanceKey, attr string, version uint64) (Value, bool) {
	inst, ok := st.instances[key]
	if !ok {
		return nil, false
	}
	h, ok := inst.attrs[attr]
	if !ok {
		return nil, false
	}
	return h.at(version)
}

// commit 把一批属性赋值作为一次原子提交写入，返回新的提交版本。
// 调用前必须完成全部校验与权限判定；提交本身不会失败。
func (st *store) commit(key instanceKey, attrs map[string]Value) uint64 {
	st.clock++
	inst, ok := st.instances[key]
	if !ok {
		inst = &instance{attrs: make(map[string]*attrHistory)}
		st.instances[key] = inst
	}
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h := inst.attrs[name]
		if h == nil {
			h = &attrHistory{}
			inst.attrs[name] = h
		}
		h.versions = append(h.versions, versionedValue{version: st.clock, value: attrs[name]})
	}
	return st.clock
}

// gc 对全部属性版本链执行垃圾回收：每条链保留最近 keepVersions 个版本，
// 再额外保留一条“基底”记录（被丢弃记录中版本最高者），使水位之上的快照
// 仍可解析。快照 s 可正确解析当且仅当 s >= 每条链基底版本，因此新的可读
// 水位取所有链基底版本的最大值（且单调不减）。返回新的可读水位。
func (st *store) gc() uint64 {
	water := st.lowWater
	for _, inst := range st.instances {
		for _, h := range inst.attrs {
			if len(h.versions) <= st.keepVersions {
				continue
			}
			keepFrom := len(h.versions) - st.keepVersions
			base := keepFrom - 1
			trimmed := make([]versionedValue, 0, st.keepVersions+1)
			trimmed = append(trimmed, h.versions[base])
			trimmed = append(trimmed, h.versions[keepFrom:]...)
			h.versions = trimmed
			if v := h.versions[0].version; v > water {
				water = v
			}
		}
	}
	st.lowWater = water
	return st.lowWater
}
