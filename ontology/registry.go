package ontology

import (
	"maps"
	"sync"
	"sync/atomic"
)

// groupDecls 是单个权限组的不可变声明集合。
// 一旦放入快照即不再修改，变更通过整体替换实现。
type groupDecls struct {
	priority    int
	objectLayer map[ObjectTypeID]DeclValue
	linkLayer   map[LinkTypeID]DeclValue
}

// snapshot 是某一版本权限状态的不可变视图。
type snapshot struct {
	version uint64
	// groups 权限组声明表，键为权限组标识。
	groups map[GroupID]*groupDecls
	// members 主体到权限组集合的隶属关系。
	members map[PrincipalID]map[GroupID]struct{}
}

// emptySnapshot 返回版本 0 的空快照。
func emptySnapshot() *snapshot {
	return &snapshot{
		groups:  make(map[GroupID]*groupDecls),
		members: make(map[PrincipalID]map[GroupID]struct{}),
	}
}

// PermissionRegistry 保存全部权限声明与主体-权限组隶属关系。
//
// 每次变更在写锁下基于当前快照构造新的不可变快照并原子替换，
// 查询在发起时刻原子地加载一次快照指针，整个查询只使用该快照。
// 因此所有操作可串行化为：变更按加锁顺序排列，查询排在加载快照
// 那一刻，其看到的正是该点之前最后一次变更的结果。
type PermissionRegistry struct {
	mu      sync.Mutex
	current atomic.Pointer[snapshot]
	policy  ConflictPolicy
}

// NewPermissionRegistry 创建使用指定合并策略的权限注册表。
func NewPermissionRegistry(policy ConflictPolicy) *PermissionRegistry {
	r := &PermissionRegistry{policy: policy}
	r.current.Store(emptySnapshot())
	return r
}

// load 原子地获取当前快照，是查询在串行化顺序中的排序点。
func (r *PermissionRegistry) load() *snapshot {
	return r.current.Load()
}

// mutate 在写锁下基于当前快照应用变更并发布新版本，返回新版本号。
func (r *PermissionRegistry) mutate(fn func(next *snapshot)) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.current.Load()
	next := &snapshot{
		version: prev.version + 1,
		groups:  maps.Clone(prev.groups),
		members: maps.Clone(prev.members),
	}
	fn(next)
	r.current.Store(next)
	return next.version
}

// cloneGroup 返回指定权限组声明集合的深拷贝，供写时复制使用。
func cloneGroup(g *groupDecls) *groupDecls {
	if g == nil {
		return &groupDecls{
			objectLayer: make(map[ObjectTypeID]DeclValue),
			linkLayer:   make(map[LinkTypeID]DeclValue),
		}
	}
	return &groupDecls{
		priority:    g.priority,
		objectLayer: maps.Clone(g.objectLayer),
		linkLayer:   maps.Clone(g.linkLayer),
	}
}

// UpsertGroup 创建或更新权限组的优先级，返回变更后的状态版本号。
func (r *PermissionRegistry) UpsertGroup(group GroupID, priority int) uint64 {
	return r.mutate(func(next *snapshot) {
		g := cloneGroup(next.groups[group])
		g.priority = priority
		next.groups[group] = g
	})
}

// SetObjectTypeDeclaration 设置对象类型层声明，返回变更后的状态版本号。
// 若权限组尚不存在，以优先级 0 隐式创建。
func (r *PermissionRegistry) SetObjectTypeDeclaration(group GroupID, objType ObjectTypeID, value DeclValue) uint64 {
	return r.mutate(func(next *snapshot) {
		g := cloneGroup(next.groups[group])
		g.objectLayer[objType] = value
		next.groups[group] = g
	})
}

// ClearObjectTypeDeclaration 移除对象类型层声明，返回变更后的状态版本号。
func (r *PermissionRegistry) ClearObjectTypeDeclaration(group GroupID, objType ObjectTypeID) uint64 {
	return r.mutate(func(next *snapshot) {
		g := cloneGroup(next.groups[group])
		delete(g.objectLayer, objType)
		next.groups[group] = g
	})
}

// SetLinkTypeDeclaration 设置链接类型层声明，返回变更后的状态版本号。
// 若权限组尚不存在，以优先级 0 隐式创建。
func (r *PermissionRegistry) SetLinkTypeDeclaration(group GroupID, linkType LinkTypeID, value DeclValue) uint64 {
	return r.mutate(func(next *snapshot) {
		g := cloneGroup(next.groups[group])
		g.linkLayer[linkType] = value
		next.groups[group] = g
	})
}

// ClearLinkTypeDeclaration 移除链接类型层声明，返回变更后的状态版本号。
func (r *PermissionRegistry) ClearLinkTypeDeclaration(group GroupID, linkType LinkTypeID) uint64 {
	return r.mutate(func(next *snapshot) {
		g := cloneGroup(next.groups[group])
		delete(g.linkLayer, linkType)
		next.groups[group] = g
	})
}

// AssignPrincipal 将权限主体加入权限组，返回变更后的状态版本号。
func (r *PermissionRegistry) AssignPrincipal(principal PrincipalID, group GroupID) uint64 {
	return r.mutate(func(next *snapshot) {
		set := maps.Clone(next.members[principal])
		if set == nil {
			set = make(map[GroupID]struct{})
		}
		set[group] = struct{}{}
		next.members[principal] = set
	})
}

// UnassignPrincipal 将权限主体移出权限组，返回变更后的状态版本号。
func (r *PermissionRegistry) UnassignPrincipal(principal PrincipalID, group GroupID) uint64 {
	return r.mutate(func(next *snapshot) {
		set := maps.Clone(next.members[principal])
		delete(set, group)
		next.members[principal] = set
	})
}
