// Package naive 是批量可达性查询的独立参照实现：
// 朴素逐项穷举，不做任何批内去重、记忆化或索引，
// 用于在随机化差分测试中对照优化实现的判定结果。
package naive

import (
	ontology "ontology/ontology"
)

// Model 朴素模型：直接以 map + 切片保存状态，每次判定独立穷举。
// 非并发安全：由测试以串行方式驱动。
type Model struct {
	objectTypes map[ontology.ObjectTypeID]ontology.ObjectType
	linkTypes   map[ontology.LinkTypeID]ontology.LinkType
	objects     map[ontology.ObjectID]ontology.Object
	links       []ontology.Link
	existence   map[ontology.CallerID]map[ontology.ObjectID]bool
	traversal   map[ontology.CallerID]map[ontology.LinkTypeID]bool
}

// NewModel 返回空的朴素模型。
func NewModel() *Model {
	return &Model{
		objectTypes: map[ontology.ObjectTypeID]ontology.ObjectType{},
		linkTypes:   map[ontology.LinkTypeID]ontology.LinkType{},
		objects:     map[ontology.ObjectID]ontology.Object{},
		existence:   map[ontology.CallerID]map[ontology.ObjectID]bool{},
		traversal:   map[ontology.CallerID]map[ontology.LinkTypeID]bool{},
	}
}

// PutObjectType 注册对象类型。
func (m *Model) PutObjectType(t ontology.ObjectType) {
	m.objectTypes[t.ID] = t
}

// PutLinkType 注册链接类型。
func (m *Model) PutLinkType(t ontology.LinkType) {
	m.linkTypes[t.ID] = t
}

// AddObject 添加对象。
func (m *Model) AddObject(o ontology.Object) {
	m.objects[o.ID] = o
}

// RemoveObject 删除对象并级联删除关联链接与存在性授权。
func (m *Model) RemoveObject(id ontology.ObjectID) {
	delete(m.objects, id)
	kept := m.links[:0]
	for _, l := range m.links {
		if l.From != id && l.To != id {
			kept = append(kept, l)
		}
	}
	m.links = kept
	for c, grants := range m.existence {
		delete(grants, id)
		if len(grants) == 0 {
			delete(m.existence, c)
		}
	}
}

// AddLink 添加有向链接（幂等：重复添加视为无操作）。
func (m *Model) AddLink(l ontology.Link) {
	for _, e := range m.links {
		if e == l {
			return
		}
	}
	m.links = append(m.links, l)
}

// RemoveLink 删除有向链接。
func (m *Model) RemoveLink(l ontology.Link) {
	for i, e := range m.links {
		if e == l {
			m.links = append(m.links[:i], m.links[i+1:]...)
			return
		}
	}
}

// SetExistence 授予或回收存在性权限。
func (m *Model) SetExistence(caller ontology.CallerID, object ontology.ObjectID, allow bool) {
	grants := m.existence[caller]
	if grants == nil {
		grants = map[ontology.ObjectID]bool{}
		m.existence[caller] = grants
	}
	if allow {
		grants[object] = true
	} else {
		delete(grants, object)
	}
}

// SetTraversal 授予或回收遍历权限。
func (m *Model) SetTraversal(caller ontology.CallerID, linkType ontology.LinkTypeID, allow bool) {
	grants := m.traversal[caller]
	if grants == nil {
		grants = map[ontology.LinkTypeID]bool{}
		m.traversal[caller] = grants
	}
	if allow {
		grants[linkType] = true
	} else {
		delete(grants, linkType)
	}
}

func (m *Model) visible(caller ontology.CallerID, id ontology.ObjectID) bool {
	if _, ok := m.objects[id]; !ok {
		return false
	}
	return m.existence[caller][id]
}

func (m *Model) typeForbidden(id ontology.ObjectID) bool {
	o, ok := m.objects[id]
	if !ok {
		return false
	}
	return m.objectTypes[o.Type].ReachableQueryForbidden
}

// Reachable 逐项穷举判定：每次调用独立深度优先搜索全部链接，
// 判定次序与优化实现一致：类型禁用 > 存在性权限 > 正常搜索。
func (m *Model) Reachable(start, end ontology.ObjectID, caller ontology.CallerID) ontology.ItemResult {
	if m.typeForbidden(start) || m.typeForbidden(end) {
		return ontology.ItemResult{State: ontology.StateRestrictedUnknown, Reason: ontology.ReasonTypeForbidden}
	}
	if !m.visible(caller, start) || !m.visible(caller, end) {
		return ontology.ItemResult{State: ontology.StateRestrictedUnknown, Reason: ontology.ReasonNoExistence}
	}
	if start == end {
		return ontology.ItemResult{State: ontology.StateReachable, Reason: ontology.ReasonReachable}
	}
	if m.dfs(start, end, caller, map[ontology.ObjectID]bool{start: true}) {
		return ontology.ItemResult{State: ontology.StateReachable, Reason: ontology.ReasonReachable}
	}
	return ontology.ItemResult{State: ontology.StateUnreachable, Reason: ontology.ReasonUnreachable}
}

// dfs 朴素穷举：每次扩展都全量扫描链接切片。
func (m *Model) dfs(cur, end ontology.ObjectID, caller ontology.CallerID, visited map[ontology.ObjectID]bool) bool {
	for _, l := range m.links {
		if l.From != cur || !m.traversal[caller][l.Type] {
			continue
		}
		if l.To == end {
			return true
		}
		if visited[l.To] {
			continue
		}
		visited[l.To] = true
		if m.dfs(l.To, end, caller, visited) {
			return true
		}
	}
	return false
}

// BatchReachable 朴素批量判定：逐项独立穷举，不做任何去重。
func (m *Model) BatchReachable(pairs []ontology.Pair, caller ontology.CallerID) []ontology.ItemResult {
	results := make([]ontology.ItemResult, len(pairs))
	for i, p := range pairs {
		results[i] = m.Reachable(p.Start, p.End, caller)
	}
	return results
}
