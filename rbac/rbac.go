// Package rbac 实现基于角色的权限控制：主体可被赋予多个角色，
// 角色之间支持多层继承，权限可授予主体或角色。主体的最终权限为
// 自身直接授权与其全部角色（含传递继承）授权的并集，直接授权优先。
package rbac

import (
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
)

// Permission 是权限标识。
type Permission string

// Decision 描述单个权限对单个主体的求值结果。
type Decision struct {
	Subject    string
	Permission Permission
	Granted    bool
	// Reason 是判定依据的可读说明。
	Reason string
	// Sources 记录授权来源："direct" 或 "role:<角色名>"，按字典序排列。
	Sources []string
}

// Manager 是并发安全的角色与授权管理器。
type Manager struct {
	mu sync.RWMutex

	// roles 记录已注册角色。
	roles map[string]struct{}
	// parents[child] 为 child 直接继承的父角色集合。
	parents map[string]map[string]struct{}
	// roleGrants[role] 为直接授予角色的权限集合。
	roleGrants map[string]map[Permission]struct{}
	// subjectRoles[subject] 为主体直接赋予的角色集合。
	subjectRoles map[string]map[string]struct{}
	// subjectGrants[subject] 为主体直接授权集合。
	subjectGrants map[string]map[Permission]struct{}

	logMu sync.Mutex
	log   io.Writer
}

// NewManager 创建一个空的管理器。
func NewManager() *Manager {
	return &Manager{
		roles:         map[string]struct{}{},
		parents:       map[string]map[string]struct{}{},
		roleGrants:    map[string]map[Permission]struct{}{},
		subjectRoles:  map[string]map[string]struct{}{},
		subjectGrants: map[string]map[Permission]struct{}{},
		log:           os.Stderr,
	}
}

// SetLogger 设置变更与求值日志的输出位置；传 nil 表示关闭日志。
// 默认输出到 os.Stderr。
func (m *Manager) SetLogger(w io.Writer) {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	m.log = w
}

func (m *Manager) logf(format string, args ...any) {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	if m.log != nil {
		fmt.Fprintf(m.log, format+"\n", args...)
	}
}

// requireRoleLocked 要求角色存在，调用方需持有写锁。
func (m *Manager) requireRoleLocked(role string) error {
	if _, ok := m.roles[role]; !ok {
		return fmt.Errorf("%w: %q", ErrRoleNotFound, role)
	}
	return nil
}

// CreateRole 注册一个新角色。
func (m *Manager) CreateRole(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.roles[name]; ok {
		m.logf("op=create_role role=%q result=deny reason=duplicate_role", name)
		return fmt.Errorf("%w: %q", ErrDuplicate, name)
	}
	m.roles[name] = struct{}{}
	m.parents[name] = map[string]struct{}{}
	m.roleGrants[name] = map[Permission]struct{}{}
	m.logf("op=create_role role=%q result=ok reason=registered", name)
	return nil
}

// RoleExists 返回角色是否已注册。
func (m *Manager) RoleExists(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.roles[name]
	return ok
}

// AssignRole 将角色赋予主体；重复赋予将被拒绝。
func (m *Manager) AssignRole(subject, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireRoleLocked(role); err != nil {
		m.logf("op=assign_role subject=%q role=%q result=deny reason=role_not_found", subject, role)
		return err
	}
	if m.subjectRoles[subject] == nil {
		m.subjectRoles[subject] = map[string]struct{}{}
	}
	if _, ok := m.subjectRoles[subject][role]; ok {
		m.logf("op=assign_role subject=%q role=%q result=deny reason=duplicate_assignment", subject, role)
		return fmt.Errorf("%w: subject=%q role=%q", ErrDuplicate, subject, role)
	}
	m.subjectRoles[subject][role] = struct{}{}
	m.logf("op=assign_role subject=%q role=%q result=ok reason=assigned", subject, role)
	return nil
}

// AddInheritance 让 child 继承 parent 的权限；循环继承将被拒绝。
func (m *Manager) AddInheritance(child, parent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireRoleLocked(child); err != nil {
		m.logf("op=add_inheritance child=%q parent=%q result=deny reason=role_not_found", child, parent)
		return err
	}
	if err := m.requireRoleLocked(parent); err != nil {
		m.logf("op=add_inheritance child=%q parent=%q result=deny reason=role_not_found", child, parent)
		return err
	}
	if child == parent {
		m.logf("op=add_inheritance child=%q parent=%q result=deny reason=cycle_self", child, parent)
		return fmt.Errorf("%w: %q inherits itself", ErrCycle, child)
	}
	if _, ok := m.parents[child][parent]; ok {
		m.logf("op=add_inheritance child=%q parent=%q result=deny reason=duplicate_inheritance", child, parent)
		return fmt.Errorf("%w: %q -> %q", ErrDuplicate, child, parent)
	}
	// 若 parent 已（传递）继承 child，再加入 child -> parent 就会形成环。
	if reachable(m.parents, parent, child) {
		m.logf("op=add_inheritance child=%q parent=%q result=deny reason=cycle", child, parent)
		return fmt.Errorf("%w: adding %q -> %q", ErrCycle, child, parent)
	}
	m.parents[child][parent] = struct{}{}
	m.logf("op=add_inheritance child=%q parent=%q result=ok reason=inherited", child, parent)
	return nil
}

// GrantToRole 向角色授予权限。
func (m *Manager) GrantToRole(role string, perm Permission) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireRoleLocked(role); err != nil {
		m.logf("op=grant_role role=%q permission=%q result=deny reason=role_not_found", role, perm)
		return err
	}
	if _, ok := m.roleGrants[role][perm]; ok {
		m.logf("op=grant_role role=%q permission=%q result=deny reason=duplicate_grant", role, perm)
		return fmt.Errorf("%w: role=%q permission=%q", ErrDuplicate, role, perm)
	}
	m.roleGrants[role][perm] = struct{}{}
	m.logf("op=grant_role role=%q permission=%q result=ok reason=granted", role, perm)
	return nil
}

// GrantToSubject 向主体直接授予权限，直接授权优先于角色授权。
func (m *Manager) GrantToSubject(subject string, perm Permission) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.subjectGrants[subject] == nil {
		m.subjectGrants[subject] = map[Permission]struct{}{}
	}
	if _, ok := m.subjectGrants[subject][perm]; ok {
		m.logf("op=grant_subject subject=%q permission=%q result=deny reason=duplicate_grant", subject, perm)
		return fmt.Errorf("%w: subject=%q permission=%q", ErrDuplicate, subject, perm)
	}
	m.subjectGrants[subject][perm] = struct{}{}
	m.logf("op=grant_subject subject=%q permission=%q result=ok reason=granted", subject, perm)
	return nil
}

// RevokeRoleAssignment 撤销主体的角色赋予。
func (m *Manager) RevokeRoleAssignment(subject, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireRoleLocked(role); err != nil {
		m.logf("op=revoke_role_assignment subject=%q role=%q result=deny reason=role_not_found", subject, role)
		return err
	}
	set := m.subjectRoles[subject]
	if _, ok := set[role]; !ok {
		m.logf("op=revoke_role_assignment subject=%q role=%q result=deny reason=grant_not_found", subject, role)
		return fmt.Errorf("%w: subject=%q role=%q", ErrGrantNotFound, subject, role)
	}
	delete(set, role)
	m.logf("op=revoke_role_assignment subject=%q role=%q result=ok reason=revoked", subject, role)
	return nil
}

// RemoveInheritance 撤销 child 对 parent 的继承。
func (m *Manager) RemoveInheritance(child, parent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireRoleLocked(child); err != nil {
		m.logf("op=remove_inheritance child=%q parent=%q result=deny reason=role_not_found", child, parent)
		return err
	}
	if err := m.requireRoleLocked(parent); err != nil {
		m.logf("op=remove_inheritance child=%q parent=%q result=deny reason=role_not_found", child, parent)
		return err
	}
	if _, ok := m.parents[child][parent]; !ok {
		m.logf("op=remove_inheritance child=%q parent=%q result=deny reason=grant_not_found", child, parent)
		return fmt.Errorf("%w: %q -> %q", ErrGrantNotFound, child, parent)
	}
	delete(m.parents[child], parent)
	m.logf("op=remove_inheritance child=%q parent=%q result=ok reason=revoked", child, parent)
	return nil
}

// RevokeRoleGrant 撤销角色上的权限。
func (m *Manager) RevokeRoleGrant(role string, perm Permission) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireRoleLocked(role); err != nil {
		m.logf("op=revoke_role_grant role=%q permission=%q result=deny reason=role_not_found", role, perm)
		return err
	}
	set := m.roleGrants[role]
	if _, ok := set[perm]; !ok {
		m.logf("op=revoke_role_grant role=%q permission=%q result=deny reason=grant_not_found", role, perm)
		return fmt.Errorf("%w: role=%q permission=%q", ErrGrantNotFound, role, perm)
	}
	delete(set, perm)
	m.logf("op=revoke_role_grant role=%q permission=%q result=ok reason=revoked", role, perm)
	return nil
}

// RevokeSubjectGrant 撤销主体的直接权限。
func (m *Manager) RevokeSubjectGrant(subject string, perm Permission) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.subjectGrants[subject]
	if _, ok := set[perm]; !ok {
		m.logf("op=revoke_subject_grant subject=%q permission=%q result=deny reason=grant_not_found", subject, perm)
		return fmt.Errorf("%w: subject=%q permission=%q", ErrGrantNotFound, subject, perm)
	}
	delete(set, perm)
	m.logf("op=revoke_subject_grant subject=%q permission=%q result=ok reason=revoked", subject, perm)
	return nil
}

// Evaluate 求值单个权限；求值过程只读且并发安全。
func (m *Manager) Evaluate(subject string, perm Permission) Decision {
	m.mu.RLock()
	defer m.mu.RUnlock()

	d := Decision{Subject: subject, Permission: perm, Sources: []string{}}

	// 直接授权优先：主体直接持有该权限时不再报告角色来源。
	if set := m.subjectGrants[subject]; set != nil {
		if _, ok := set[perm]; ok {
			d.Granted = true
			d.Sources = []string{"direct"}
			d.Reason = "granted by direct subject grant (takes precedence over role grants)"
			m.logf("op=evaluate subject=%q permission=%q decision=allow basis=direct", subject, perm)
			return d
		}
	}

	sources := m.permissionSourcesLocked(subject, perm)
	if len(sources) > 0 {
		d.Granted = true
		d.Sources = sources
		d.Reason = fmt.Sprintf("granted via role(s): %v", sources)
		m.logf("op=evaluate subject=%q permission=%q decision=allow basis=roles sources=%v", subject, perm, sources)
		return d
	}

	d.Reason = "denied: no direct grant and no assigned role (including transitive parents) grants it"
	m.logf("op=evaluate subject=%q permission=%q decision=deny basis=none", subject, perm)
	return d
}

// EffectivePermissions 返回主体的最终权限集合及其来源。
func (m *Manager) EffectivePermissions(subject string) map[Permission][]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	acc := map[Permission]map[string]struct{}{}

	for _, role := range m.reachableRolesLocked(subject) {
		for perm := range m.roleGrants[role] {
			if acc[perm] == nil {
				acc[perm] = map[string]struct{}{}
			}
			acc[perm]["role:"+role] = struct{}{}
		}
	}

	for perm := range m.subjectGrants[subject] {
		// 直接授权覆盖角色来源。
		acc[perm] = map[string]struct{}{"direct": {}}
	}

	result := make(map[Permission][]string, len(acc))
	for perm, srcSet := range acc {
		sources := make([]string, 0, len(srcSet))
		for src := range srcSet {
			sources = append(sources, src)
		}
		sort.Strings(sources)
		result[perm] = sources
	}

	m.logf("op=effective_permissions subject=%q permission_count=%d", subject, len(result))
	return result
}

// reachableRolesLocked 返回主体可触达的全部角色（直接角色 + 传递继承），
// 按字典序排列，保证求值结果与注册顺序无关。调用方需持有读锁。
func (m *Manager) reachableRolesLocked(subject string) []string {
	visited := map[string]struct{}{}
	frontier := []string{}
	for role := range m.subjectRoles[subject] {
		if _, seen := visited[role]; !seen {
			visited[role] = struct{}{}
			frontier = append(frontier, role)
		}
	}
	for len(frontier) > 0 {
		role := frontier[0]
		frontier = frontier[1:]
		parents := make([]string, 0, len(m.parents[role]))
		for parent := range m.parents[role] {
			parents = append(parents, parent)
		}
		sort.Strings(parents)
		for _, parent := range parents {
			if _, seen := visited[parent]; !seen {
				visited[parent] = struct{}{}
				frontier = append(frontier, parent)
			}
		}
	}

	roles := make([]string, 0, len(visited))
	for role := range visited {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

// permissionSourcesLocked 返回授予主体某权限的角色来源（含继承），按字典序排列。
// 调用方需持有读锁。
func (m *Manager) permissionSourcesLocked(subject string, perm Permission) []string {
	sources := []string{}
	for _, role := range m.reachableRolesLocked(subject) {
		if _, ok := m.roleGrants[role][perm]; ok {
			sources = append(sources, "role:"+role)
		}
	}
	return sources
}

// reachable 在以 parents 表示的继承有向图中，判断从 from 出发能否经非空路径到达 to。
func reachable(parents map[string]map[string]struct{}, from, to string) bool {
	visited := map[string]struct{}{from: {}}
	frontier := []string{from}
	for len(frontier) > 0 {
		role := frontier[0]
		frontier = frontier[1:]
		for parent := range parents[role] {
			if parent == to {
				return true
			}
			if _, seen := visited[parent]; !seen {
				visited[parent] = struct{}{}
				frontier = append(frontier, parent)
			}
		}
	}
	return false
}
