package ontology

import "sort"

// Store 保存本体平台的已提交状态：对象类型、链接类型、实例、
// 链接、审计记录与逻辑时钟。Store 本身不加锁；并发串行化由
// Engine 负责。动作执行期间的所有变更先写入 tx 暂存区，
// 仅在最终判定为允许时由 commit 原子落盘。
type Store struct {
	Types     map[ObjectTypeID]ObjectType
	LinkTypes map[LinkTypeID]LinkType
	Instances map[InstanceID]*Instance
	Links     []Link
	Audit     []AuditRecord
	Clock     int64

	out map[InstanceID][]Link // From 方向的邻接索引
}

// NewStore 返回一个空 Store。
func NewStore() *Store {
	return &Store{
		Types:     map[ObjectTypeID]ObjectType{},
		LinkTypes: map[LinkTypeID]LinkType{},
		Instances: map[InstanceID]*Instance{},
		out:       map[InstanceID][]Link{},
	}
}

// AddObjectType 注册对象类型（管理态操作，不属于动作事务）。
func (s *Store) AddObjectType(t ObjectType) { s.Types[t.ID] = t }

// AddLinkType 注册链接类型（管理态操作）。
func (s *Store) AddLinkType(lt LinkType) { s.LinkTypes[lt.ID] = lt }

// AddLink 建立实例间的链接并维护邻接索引（管理态操作）。
func (s *Store) AddLink(l Link) {
	s.Links = append(s.Links, l)
	s.out[l.From] = append(s.out[l.From], l)
}

// PutInstance 直接放入一个实例（管理态/建图操作）。
func (s *Store) PutInstance(in *Instance) { s.Instances[in.ID] = in }

// neighbors 返回从 id 出发、经未排除链接类型可达的 (链接类型, 目标) 对。
func (s *Store) neighbors(id InstanceID, excluded map[LinkTypeID]bool) []Link {
	var res []Link
	for _, l := range s.out[id] {
		if excluded[l.Type] {
			continue
		}
		res = append(res, l)
	}
	return res
}

// State 是 Store 可观察状态的深拷贝快照，用于测试与参照实现对照。
type State struct {
	Instances map[InstanceID]Instance
	AuditLen  int
	Clock     int64
}

// Snapshot 返回当前已提交状态的深拷贝。
func (s *Store) Snapshot() State {
	st := State{Instances: make(map[InstanceID]Instance, len(s.Instances)), Clock: s.Clock}
	for id, in := range s.Instances {
		st.Instances[id] = *in.clone()
	}
	st.AuditLen = len(s.Audit)
	return st
}

// tx 是动作执行期间的写暂存区。被拒绝时直接丢弃，
// 不会对 Store 产生任何可观察影响。
type tx struct {
	upserts map[InstanceID]*Instance // 创建与修改后的新实例状态
	audit   AuditRecord
}

func newTx() *tx {
	return &tx{upserts: map[InstanceID]*Instance{}, audit: AuditRecord{Committed: true}}
}

// get 优先读暂存区，实现事务内“读己之写”。
func (t *tx) get(s *Store, id InstanceID) *Instance {
	if in, ok := t.upserts[id]; ok {
		return in
	}
	return s.Instances[id]
}

// commit 将暂存区整体落盘并推进逻辑时钟、追加审计记录。
// 该操作在 Engine 的串行锁内执行，对外表现为不可分割的整体。
func (s *Store) commit(t *tx) {
	s.Clock++
	ids := make([]string, 0, len(t.upserts))
	for id := range t.upserts {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		s.Instances[InstanceID(id)] = t.upserts[InstanceID(id)]
	}
	t.audit.Seq = s.Clock
	s.Audit = append(s.Audit, t.audit)
}
