package ontology

import "sort"

// MemStore 是内存版 Store，自带暂存区以支持原子提交。
// 暂存区以"逻辑视图 + 变更集"实现：未提交前所有变更对外不可见，
// commit 时一次性应用变更集，rollback 仅丢弃变更集，因此天然全有或全无。
type MemStore struct {
	inst  map[InstanceID]Instance
	edges []Edge
	clock int64
	audit []AuditRecord

	tx       bool
	txInst   map[InstanceID]*Instance // 覆盖值；指针为 nil 表示删除；key 不存在表示未改
	txCreate map[InstanceID]bool
	txEdges  []edgeChange
}

type edgeChange struct {
	add bool
	e   Edge
}

// NewMemStore 构造空内存存储。
func NewMemStore() *MemStore {
	return &MemStore{inst: map[InstanceID]Instance{}}
}

// AddInstance / AddEdge 在事务外（测试搭建夹具用）写入初始状态。
func (m *MemStore) AddInstance(inst Instance) {
	if inst.Attrs == nil {
		inst.Attrs = map[string]string{}
	}
	m.inst[inst.ID] = inst
}

func (m *MemStore) AddEdge(e Edge) { m.edges = append(m.edges, e) }

func (m *MemStore) Clock() int64                { return m.clock }
func (m *MemStore) Audit() []AuditRecord        { return m.audit }
func (m *MemStore) AppendAudit(rec AuditRecord) { m.audit = append(m.audit, rec) }

// Begin 开启一次事务暂存。
func (m *MemStore) Begin() {
	m.tx = true
	m.txInst = map[InstanceID]*Instance{}
	m.txCreate = map[InstanceID]bool{}
	m.txEdges = nil
}

func (m *MemStore) rollback() {
	m.tx = false
	m.txInst = nil
	m.txCreate = nil
	m.txEdges = nil
}

// commit 一次性应用全部暂存变更；调用点保证此时不会再失败。
func (m *MemStore) commit() {
	for id, v := range m.txInst {
		if v == nil {
			delete(m.inst, id)
		} else {
			cp := *v
			cp.Attrs = cloneAttrs(v.Attrs)
			m.inst[id] = cp
		}
	}
	for _, c := range m.txEdges {
		if c.add {
			m.edges = append(m.edges, c.e)
		} else {
			m.edges = removeEdge(m.edges, c.e)
		}
	}
	m.clock++
	m.rollback()
}

func removeEdge(edges []Edge, target Edge) []Edge {
	out := edges[:0]
	for _, e := range edges {
		if e == target {
			continue
		}
		out = append(out, e)
	}
	return out
}

func cloneAttrs(a map[string]string) map[string]string {
	out := make(map[string]string, len(a))
	for k, v := range a {
		out[k] = v
	}
	return out
}

// view 返回当前逻辑视图（含暂存）下的实例。
func (m *MemStore) view(id InstanceID) (Instance, bool) {
	if m.tx {
		if v, ok := m.txInst[id]; ok {
			if v == nil {
				return Instance{}, false
			}
			return *v, true
		}
	}
	v, ok := m.inst[id]
	return v, ok
}

func (m *MemStore) Get(id InstanceID) (Instance, bool) {
	inst, ok := m.view(id)
	if ok {
		inst.Attrs = cloneAttrs(inst.Attrs)
	}
	return inst, ok
}

func (m *MemStore) Has(id InstanceID) bool {
	_, ok := m.view(id)
	return ok
}

func (m *MemStore) liveEdges() []Edge {
	if !m.tx || len(m.txEdges) == 0 {
		return m.edges
	}
	out := append([]Edge(nil), m.edges...)
	for _, c := range m.txEdges {
		if c.add {
			out = append(out, c.e)
		} else {
			out = removeEdge(out, c.e)
		}
	}
	return out
}

// Neighbors 按确定性顺序（实例 ID）枚举经由指定规则的一步邻居，
// 使首次到达深度不依赖边的插入顺序（与朴素参照一致）。
func (m *MemStore) Neighbors(id InstanceID, rule CascadeRule) []InstanceID {
	seen := map[InstanceID]bool{}
	for _, e := range m.liveEdges() {
		if e.LinkType != rule.LinkType {
			continue
		}
		if rule.Outgoing && e.From == id {
			seen[e.To] = true
		}
		if !rule.Outgoing && e.To == id {
			seen[e.From] = true
		}
	}
	out := make([]InstanceID, 0, len(seen))
	for nb := range seen {
		if _, alive := m.view(nb); !alive {
			continue // 悬空链接目标不参与传播
		}
		out = append(out, nb)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (m *MemStore) stageCreate(op DirectOp) {
	inst := &Instance{ID: op.Target, Type: op.Type, Version: 1, Attrs: cloneAttrs(op.NewAttrs)}
	m.txInst[op.Target] = inst
	m.txCreate[op.Target] = true
}

func (m *MemStore) stageUpdate(id InstanceID, attrs map[string]string) {
	cur, ok := m.view(id)
	if !ok {
		return
	}
	next := cur
	next.Attrs = cloneAttrs(cur.Attrs)
	for k, v := range attrs {
		next.Attrs[k] = v
	}
	next.Version++
	m.txInst[id] = &next
}

func (m *MemStore) stageDelete(id InstanceID) { m.txInst[id] = nil }

func (m *MemStore) stageLink(e Edge)   { m.txEdges = append(m.txEdges, edgeChange{add: true, e: e}) }
func (m *MemStore) stageUnlink(e Edge) { m.txEdges = append(m.txEdges, edgeChange{add: false, e: e}) }

// Snapshot 返回可外部观测的完整状态深拷贝。
func (m *MemStore) Snapshot() WorldState {
	insts := make(map[InstanceID]Instance, len(m.inst))
	for id, in := range m.inst {
		in.Attrs = cloneAttrs(in.Attrs)
		insts[id] = in
	}
	audits := append([]AuditRecord(nil), m.audit...)
	return WorldState{Instances: insts, Edges: append([]Edge(nil), m.edges...), Clock: m.clock, Audit: audits}
}
