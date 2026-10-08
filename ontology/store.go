// Package ontology 提供本体对象、链接类型与链接实例的存储，
// 以及基于角色的路径查询能力。
package ontology

import (
	"fmt"
	"sync"
)

// ObjectID 是对象的唯一标识。
type ObjectID string

// LinkTypeName 是链接类型的唯一标识。
type LinkTypeName string

// Role 是链接类型所承担的角色名。
type Role string

// Caller 是查询调用者标识，用于权限判定。
type Caller string

// LinkType 描述一种链接类型：方向由链接实例的 (from, to) 表达，
// Cost 为每经过一条该类型链接的代价，Roles 为该类型承担的角色
// 及其在角色内的优先级（数值越小优先级越高）。
type LinkType struct {
	Name  LinkTypeName
	Cost  int64
	Roles map[Role]int
}

// linkKey 唯一标识一条链接实例。
type linkKey struct {
	typ  LinkTypeName
	from ObjectID
	to   ObjectID
}

// Store 是本体数据与查询的入口。所有变更与查询在互斥锁下串行化，
// 任意一次查询观察到的是一个一致的时间点快照。
type Store struct {
	mu sync.RWMutex

	objects   map[ObjectID]struct{}
	linkTypes map[LinkTypeName]*LinkType
	links     map[linkKey]struct{}

	// byType 按链接类型索引链接实例，用于角色声明变更时维护邻接索引。
	byType map[LinkTypeName]map[linkKey]struct{}
	// incoming 按终点索引链接实例，用于删除对象时清理。
	incoming map[ObjectID]map[linkKey]struct{}

	// adj 为查询邻接索引：from -> role -> linkType -> []to。
	// 只按角色索引，保证查询展开的开销与全图规模无关。
	adj map[ObjectID]map[Role]map[LinkTypeName][]ObjectID

	// roleRefs 是全局已声明角色集合的引用计数（>0 即视为已声明）。
	roleRefs map[Role]int

	// existence[caller] 为调用者拥有存在性权限的对象集合。
	existence map[Caller]map[ObjectID]struct{}
	// traversal[caller] 为调用者拥有遍历权限的链接类型集合。
	traversal map[Caller]map[LinkTypeName]struct{}
}

// NewStore 创建一个空的本体存储。
func NewStore() *Store {
	return &Store{
		objects:   make(map[ObjectID]struct{}),
		linkTypes: make(map[LinkTypeName]*LinkType),
		links:     make(map[linkKey]struct{}),
		byType:    make(map[LinkTypeName]map[linkKey]struct{}),
		incoming:  make(map[ObjectID]map[linkKey]struct{}),
		adj:       make(map[ObjectID]map[Role]map[LinkTypeName][]ObjectID),
		roleRefs:  make(map[Role]int),
		existence: make(map[Caller]map[ObjectID]struct{}),
		traversal: make(map[Caller]map[LinkTypeName]struct{}),
	}
}

// AddObject 添加一个对象（幂等）。
func (s *Store) AddObject(id ObjectID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[id] = struct{}{}
}

// RemoveObject 删除一个对象及其关联的所有链接实例。
func (s *Store) RemoveObject(id ObjectID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[id]; !ok {
		return
	}
	delete(s.objects, id)
	for role, byType := range s.adj[id] {
		for typ := range byType {
			s.removeAdjLocked(id, role, typ)
		}
	}
	delete(s.adj, id)
	for k := range s.incoming[id] {
		s.removeLinkLocked(k)
	}
}

// AddLinkType 声明一个链接类型（幂等；已存在时仅更新代价）。
func (s *Store) AddLinkType(name LinkTypeName, cost int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lt, ok := s.linkTypes[name]; ok {
		lt.Cost = cost
		return
	}
	s.linkTypes[name] = &LinkType{Name: name, Cost: cost, Roles: make(map[Role]int)}
}

// RemoveLinkType 删除链接类型及其全部链接实例与角色声明。
func (s *Store) RemoveLinkType(name LinkTypeName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lt, ok := s.linkTypes[name]
	if !ok {
		return
	}
	for k := range s.byType[name] {
		s.removeLinkLocked(k)
	}
	for r := range lt.Roles {
		s.decRoleLocked(r)
	}
	delete(s.linkTypes, name)
}

// DeclareRole 声明（或调整优先级）链接类型承担的角色。
// priority 数值越小优先级越高。
func (s *Store) DeclareRole(typ LinkTypeName, role Role, priority int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lt, ok := s.linkTypes[typ]
	if !ok {
		return fmt.Errorf("ontology: link type %q not found", typ)
	}
	if _, ok := lt.Roles[role]; ok {
		lt.Roles[role] = priority
		return nil
	}
	lt.Roles[role] = priority
	s.roleRefs[role]++
	for k := range s.byType[typ] {
		s.addAdjLocked(k, role)
	}
	return nil
}

// UndeclareRole 取消链接类型承担的某个角色。
func (s *Store) UndeclareRole(typ LinkTypeName, role Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lt, ok := s.linkTypes[typ]
	if !ok {
		return fmt.Errorf("ontology: link type %q not found", typ)
	}
	if _, ok := lt.Roles[role]; !ok {
		return nil
	}
	delete(lt.Roles, role)
	s.decRoleLocked(role)
	for k := range s.byType[typ] {
		s.removeAdjLocked(k.from, role, typ)
	}
	return nil
}

// SetRolePriority 调整链接类型在某角色内的优先级。
func (s *Store) SetRolePriority(typ LinkTypeName, role Role, priority int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lt, ok := s.linkTypes[typ]
	if !ok {
		return fmt.Errorf("ontology: link type %q not found", typ)
	}
	if _, ok := lt.Roles[role]; !ok {
		return fmt.Errorf("ontology: link type %q does not bear role %q", typ, role)
	}
	lt.Roles[role] = priority
	return nil
}

// AddLink 添加一条链接实例（幂等）。两端对象与链接类型必须已存在。
func (s *Store) AddLink(typ LinkTypeName, from, to ObjectID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.linkTypes[typ]; !ok {
		return fmt.Errorf("ontology: link type %q not found", typ)
	}
	for _, id := range []ObjectID{from, to} {
		if _, ok := s.objects[id]; !ok {
			return fmt.Errorf("ontology: object %q not found", id)
		}
	}
	k := linkKey{typ: typ, from: from, to: to}
	if _, ok := s.links[k]; ok {
		return nil
	}
	s.links[k] = struct{}{}
	if s.byType[typ] == nil {
		s.byType[typ] = make(map[linkKey]struct{})
	}
	s.byType[typ][k] = struct{}{}
	if s.incoming[to] == nil {
		s.incoming[to] = make(map[linkKey]struct{})
	}
	s.incoming[to][k] = struct{}{}
	for role := range s.linkTypes[typ].Roles {
		s.addAdjLocked(k, role)
	}
	return nil
}

// RemoveLink 删除一条链接实例（幂等）。
func (s *Store) RemoveLink(typ LinkTypeName, from, to ObjectID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLinkLocked(linkKey{typ: typ, from: from, to: to})
}

// GrantExistence 授予调用者对对象的存在性权限。
func (s *Store) GrantExistence(caller Caller, obj ObjectID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.existence[caller] == nil {
		s.existence[caller] = make(map[ObjectID]struct{})
	}
	s.existence[caller][obj] = struct{}{}
}

// RevokeExistence 收回调用者对对象的存在性权限。
func (s *Store) RevokeExistence(caller Caller, obj ObjectID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.existence[caller], obj)
}

// GrantTraversal 授予调用者对链接类型的遍历权限。
func (s *Store) GrantTraversal(caller Caller, typ LinkTypeName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.traversal[caller] == nil {
		s.traversal[caller] = make(map[LinkTypeName]struct{})
	}
	s.traversal[caller][typ] = struct{}{}
}

// RevokeTraversal 收回调用者对链接类型的遍历权限。
func (s *Store) RevokeTraversal(caller Caller, typ LinkTypeName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.traversal[caller], typ)
}

func (s *Store) decRoleLocked(r Role) {
	s.roleRefs[r]--
	if s.roleRefs[r] <= 0 {
		delete(s.roleRefs, r)
	}
}

func (s *Store) addAdjLocked(k linkKey, role Role) {
	byRole := s.adj[k.from]
	if byRole == nil {
		byRole = make(map[Role]map[LinkTypeName][]ObjectID)
		s.adj[k.from] = byRole
	}
	byType := byRole[role]
	if byType == nil {
		byType = make(map[LinkTypeName][]ObjectID)
		byRole[role] = byType
	}
	byType[k.typ] = append(byType[k.typ], k.to)
}

func (s *Store) removeAdjLocked(from ObjectID, role Role, typ LinkTypeName) {
	byRole := s.adj[from]
	if byRole == nil {
		return
	}
	byType := byRole[role]
	if byType == nil {
		return
	}
	delete(byType, typ)
	if len(byType) == 0 {
		delete(byRole, role)
	}
}

func (s *Store) removeLinkLocked(k linkKey) {
	if _, ok := s.links[k]; !ok {
		return
	}
	delete(s.links, k)
	delete(s.byType[k.typ], k)
	if inc := s.incoming[k.to]; inc != nil {
		delete(inc, k)
		if len(inc) == 0 {
			delete(s.incoming, k.to)
		}
	}
	if lt, ok := s.linkTypes[k.typ]; ok {
		for role := range lt.Roles {
			s.removeAdjInstLocked(k, role)
		}
	}
}

func (s *Store) removeAdjInstLocked(k linkKey, role Role) {
	byRole := s.adj[k.from]
	if byRole == nil {
		return
	}
	byType := byRole[role]
	if byType == nil {
		return
	}
	tos := byType[k.typ]
	for i, to := range tos {
		if to == k.to {
			byType[k.typ] = append(tos[:i], tos[i+1:]...)
			break
		}
	}
	if len(byType[k.typ]) == 0 {
		delete(byType, k.typ)
	}
	if len(byType) == 0 {
		delete(byRole, role)
	}
}
