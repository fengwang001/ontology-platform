package ontology_test

// 本文件实现一个刻意朴素、与生产代码完全独立的参考模型：
//
//   - 它保留全部历史对应关系记录，每次修订就地演进并可重放检查；
//   - 每个实例只记住自己"当前实际版本"与原始数据；
//   - 每次读取都从原始数据出发当场重新计算视图（无缓存、无哈希加速假设）；
//   - 每次写入/回填都按同一套朴素规则推演。
//
// 随机测试中同一组操作同时喂给 Store 与朴素模型，逐条对照错误类别与
// 每次读取结果；二者一致即证明 Store 的优化（哈希直查、转换快照、
// 陈旧队列项跳过）与朴素重算语义等价，因而等价于某种全局串行顺序。

import (
	"errors"
	"sort"

	"ontology/ontology"
)

type naiveMapping struct {
	attr string
	kind ontology.Kind
	def  ontology.Value
}

type naiveInstance struct {
	exists     bool
	backfilled bool
	gen        int64
	oldRaw     map[string]ontology.Value
	newRaw     map[string]ontology.Value
	// kindSnap 是朴素模型在转换当时抄录的"属性->Kind"快照；
	// 已回填实例的旧视图只依据这份快照投影。
	kindSnap map[string]ontology.Kind
}

type naiveModel struct {
	started bool
	rev     int64
	active  map[string]naiveMapping
	frozen  map[string]bool
	insts   map[string]*naiveInstance
	queue   []string
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		active: map[string]naiveMapping{},
		frozen: map[string]bool{},
		insts:  map[string]*naiveInstance{},
	}
}

type errClass int

const (
	classOK errClass = iota
	classInvalid
	classNotFound
)

func classify(err error) errClass {
	switch {
	case err == nil:
		return classOK
	case errors.Is(err, ontology.ErrInvalidArgument):
		return classInvalid
	case errors.Is(err, ontology.ErrNotFound):
		return classNotFound
	default:
		return -1
	}
}

func cloneM(p map[string]ontology.Value) map[string]ontology.Value {
	out := make(map[string]ontology.Value, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

func (m *naiveModel) amend(mappings []naiveMapping) errClass {
	batch := map[string]naiveMapping{}
	for _, mp := range mappings {
		if mp.attr == "" {
			return classInvalid
		}
		if _, dup := batch[mp.attr]; dup {
			return classInvalid
		}
		batch[mp.attr] = mp
	}
	for attr := range batch {
		if m.frozen[attr] {
			return classInvalid
		}
	}
	for _, mp := range mappings {
		switch mp.kind {
		case ontology.KindKeep, ontology.KindDrop:
		case ontology.KindAdd:
			if !mp.def.Set {
				return classInvalid
			}
		default:
			return classInvalid
		}
	}
	for _, mp := range mappings {
		m.active[mp.attr] = mp
	}
	m.rev++
	return classOK
}

func (m *naiveModel) start(mappings []naiveMapping) errClass {
	if m.started {
		return classInvalid
	}
	if c := m.amend(mappings); c != classOK {
		return c
	}
	m.started = true
	ids := make([]string, 0, len(m.insts))
	for id := range m.insts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !m.insts[id].backfilled {
			m.queue = append(m.queue, id)
		}
	}
	return classOK
}

func (m *naiveModel) validate(v ontology.Version, p map[string]ontology.Value) errClass {
	if v != ontology.VersionOld && v != ontology.VersionNew {
		return classInvalid
	}
	if !m.started {
		if v != ontology.VersionOld {
			return classInvalid
		}
		return classOK
	}
	for attr, val := range p {
		mp, known := m.active[attr]
		if !known {
			continue
		}
		if v == ontology.VersionNew && mp.kind == ontology.KindDrop && val.Set {
			return classInvalid
		}
		if v == ontology.VersionOld && mp.kind == ontology.KindAdd && val.Set {
			return classInvalid
		}
	}
	return classOK
}

func (m *naiveModel) create(id string, v ontology.Version, p map[string]ontology.Value) errClass {
	if c := m.validate(v, p); c != classOK {
		return c
	}
	if _, exists := m.insts[id]; exists {
		return classInvalid
	}
	in := &naiveInstance{exists: true}
	if m.started && v == ontology.VersionNew {
		in.backfilled = true
		in.gen = m.rev
		in.kindSnap = m.currentKindSnap()
		in.newRaw = cloneM(p)
	} else {
		in.oldRaw = cloneM(p)
		if m.started {
			m.queue = append(m.queue, id)
		}
	}
	m.insts[id] = in
	return classOK
}

func (m *naiveModel) currentKindSnap() map[string]ontology.Kind {
	snap := make(map[string]ontology.Kind, len(m.active))
	for attr, mp := range m.active {
		snap[attr] = mp.kind
	}
	return snap
}

func (m *naiveModel) convert(in *naiveInstance) {
	next := map[string]ontology.Value{}
	used := []string{}
	for attr, val := range in.oldRaw {
		if mp, known := m.active[attr]; known {
			used = append(used, attr)
			if mp.kind == ontology.KindDrop {
				continue
			}
		}
		next[attr] = val
	}
	attrs := make([]string, 0, len(m.active))
	for attr, mp := range m.active {
		if mp.kind == ontology.KindAdd {
			attrs = append(attrs, attr)
		}
	}
	sort.Strings(attrs)
	for _, attr := range attrs {
		if _, ok := next[attr]; !ok {
			next[attr] = m.active[attr].def
		}
		used = append(used, attr)
	}
	in.newRaw = next
	in.backfilled = true
	in.gen = m.rev
	in.kindSnap = m.currentKindSnap()
	in.oldRaw = nil
	for _, attr := range used {
		m.frozen[attr] = true
	}
}

func (m *naiveModel) write(id string, v ontology.Version, p map[string]ontology.Value) errClass {
	if c := m.validate(v, p); c != classOK {
		return c
	}
	in, ok := m.insts[id]
	if !ok {
		return classNotFound
	}
	if v == ontology.VersionNew || !m.started {
		if !m.started {
			in.oldRaw = cloneM(p)
		} else {
			if !in.backfilled {
				in.backfilled = true
				in.gen = m.rev
				in.kindSnap = m.currentKindSnap()
				in.oldRaw = nil
			}
			in.newRaw = cloneM(p)
		}
		return classOK
	}
	if in.backfilled {
		next := map[string]ontology.Value{}
		for attr, val := range p {
			if k, known := in.kindSnap[attr]; known && k != ontology.KindKeep {
				continue
			}
			next[attr] = val
		}
		in.newRaw = next
		return classOK
	}
	m.convert(in)
	next := cloneM(in.newRaw)
	for attr, val := range p {
		if mp, known := m.active[attr]; known && mp.kind == ontology.KindDrop {
			continue
		}
		next[attr] = val
	}
	in.newRaw = next
	return classOK
}

func (m *naiveModel) read(id string, v ontology.Version) (map[string]ontology.Value, errClass) {
	if v != ontology.VersionOld && v != ontology.VersionNew {
		return nil, classInvalid
	}
	in, ok := m.insts[id]
	if !ok {
		return nil, classNotFound
	}
	if in.backfilled {
		if v == ontology.VersionNew {
			return cloneM(in.newRaw), classOK
		}
		out := map[string]ontology.Value{}
		for attr, val := range in.newRaw {
			if k, known := in.kindSnap[attr]; known && k != ontology.KindKeep {
				continue
			}
			out[attr] = val
		}
		return out, classOK
	}
	if v == ontology.VersionOld {
		return cloneM(in.oldRaw), classOK
	}
	out := map[string]ontology.Value{}
	for attr, val := range in.oldRaw {
		if mp, known := m.active[attr]; known && mp.kind == ontology.KindDrop {
			continue
		}
		out[attr] = val
	}
	return out, classOK
}

func (m *naiveModel) delete(id string) errClass {
	if _, ok := m.insts[id]; !ok {
		return classNotFound
	}
	delete(m.insts, id)
	return classOK
}

type naiveBackfillResult struct {
	id        string
	didWork   bool
	skipped   bool
	backfills bool
}

func (m *naiveModel) backfill() naiveBackfillResult {
	if len(m.queue) == 0 {
		return naiveBackfillResult{}
	}
	id := m.queue[0]
	m.queue = m.queue[1:]
	in, exists := m.insts[id]
	if !exists {
		return naiveBackfillResult{id: id, didWork: true, skipped: true}
	}
	if in.backfilled {
		return naiveBackfillResult{id: id, didWork: true, skipped: true}
	}
	m.convert(in)
	return naiveBackfillResult{id: id, didWork: true, backfills: true}
}
