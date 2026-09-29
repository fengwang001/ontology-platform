package rbac

import (
	"log/slog"
	"maps"
	"sync"
)

// Manager keeps roles, role inheritance, subject-role assignments and
// permission grants. All exported methods are safe for concurrent use.
type Manager struct {
	mu sync.RWMutex

	// parents[r] holds the direct parent roles of r.
	parents map[string]map[string]struct{}
	// roles[name] holds grants made directly to the role.
	roles map[string]map[string]Permission
	// assignments[subject] holds the roles assigned to the subject.
	assignments map[string]map[string]struct{}
	// subjects[subject] holds grants made directly to the subject.
	subjects map[string]map[string]Permission

	logger *slog.Logger
}

func NewManager(logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		parents:     make(map[string]map[string]struct{}),
		roles:       make(map[string]map[string]Permission),
		assignments: make(map[string]map[string]struct{}),
		subjects:    make(map[string]map[string]Permission),
		logger:      logger,
	}
}

// CreateRole registers a new role. It fails with ErrRoleExists when the role
// is already registered.
func (m *Manager) CreateRole(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.roles[name]; ok {
		return ErrRoleExists
	}
	m.roles[name] = make(map[string]Permission)
	m.parents[name] = make(map[string]struct{})
	m.logger.Info("role created", "role", name)
	return nil
}

// AddInheritance makes child inherit every permission granted to parent
// (transitively). Self inheritance, missing roles, cycles and duplicate
// inheritance edges are rejected and leave state unchanged.
func (m *Manager) AddInheritance(child, parent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.roles[child]; !ok {
		return ErrRoleNotFound
	}
	if _, ok := m.roles[parent]; !ok {
		return ErrRoleNotFound
	}
	if child == parent {
		return ErrRoleCycle
	}
	if _, dup := m.parents[child][parent]; dup {
		return ErrDuplicateInherit
	}
	m.parents[child][parent] = struct{}{}
	if m.reaches(parent, child) {
		delete(m.parents[child], parent)
		return ErrRoleCycle
	}
	m.logger.Info("role inheritance added", "child", child, "parent", parent)
	return nil
}

// reaches reports whether starting at from follows parent edges to target.
func (m *Manager) reaches(from, target string) bool {
	visited := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for next := range m.parents[current] {
			if next == target {
				return true
			}
			if !visited[next] {
				visited[next] = true
				stack = append(stack, next)
			}
		}
	}
	return false
}

// AssignRole gives subject the role. Missing roles and duplicate assignments
// are rejected.
func (m *Manager) AssignRole(subject, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.roles[role]; !ok {
		return ErrRoleNotFound
	}
	assigned := m.assignments[subject]
	if assigned == nil {
		assigned = make(map[string]struct{})
		m.assignments[subject] = assigned
	}
	if _, dup := assigned[role]; dup {
		return ErrDuplicateAssignment
	}
	assigned[role] = struct{}{}
	if m.subjects[subject] == nil {
		m.subjects[subject] = make(map[string]Permission)
	}
	m.logger.Info("role assigned", "subject", subject, "role", role)
	return nil
}

// GrantToRole grants p to every subject that has the role (including subjects
// that reach it through inheritance).
func (m *Manager) GrantToRole(role string, p Permission) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	grants, ok := m.roles[role]
	if !ok {
		return ErrRoleNotFound
	}
	key := p.key()
	if existing, dup := grants[key]; dup && existing.Effect == p.Effect {
		return ErrDuplicateGrant
	}
	grants[key] = p
	m.logger.Info("permission granted to role",
		"role", role, "resource", p.Resource, "action", p.Action, "effect", p.Effect.String())
	return nil
}

// GrantToSubject grants p directly to subject. A direct grant overrides every
// role-derived grant for the same resource/action.
func (m *Manager) GrantToSubject(subject string, p Permission) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	grants := m.subjects[subject]
	if grants == nil {
		grants = make(map[string]Permission)
		m.subjects[subject] = grants
	}
	key := p.key()
	if existing, dup := grants[key]; dup && existing.Effect == p.Effect {
		return ErrDuplicateGrant
	}
	grants[key] = p
	m.logger.Info("permission granted to subject",
		"subject", subject, "resource", p.Resource, "action", p.Action, "effect", p.Effect.String())
	return nil
}

// RevokeFromRole removes a grant previously made to the role. Re-granting a
// resource/action with the opposite effect is not considered a duplicate and
// must be revoked by its current effect.
func (m *Manager) RevokeFromRole(role string, p Permission) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	grants, ok := m.roles[role]
	if !ok {
		return ErrRoleNotFound
	}
	key := p.key()
	existing, found := grants[key]
	if !found || existing.Effect != p.Effect {
		return ErrGrantNotFound
	}
	delete(grants, key)
	m.logger.Info("permission revoked from role",
		"role", role, "resource", p.Resource, "action", p.Action, "effect", p.Effect.String())
	return nil
}

// RevokeFromSubject removes a direct subject grant.
func (m *Manager) RevokeFromSubject(subject string, p Permission) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	grants := m.subjects[subject]
	key := p.key()
	if grants == nil {
		return ErrGrantNotFound
	}
	existing, found := grants[key]
	if !found || existing.Effect != p.Effect {
		return ErrGrantNotFound
	}
	delete(grants, key)
	m.logger.Info("permission revoked from subject",
		"subject", subject, "resource", p.Resource, "action", p.Action, "effect", p.Effect.String())
	return nil
}

// RevokeRole removes a role previously assigned to subject.
func (m *Manager) RevokeRole(subject, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	assigned := m.assignments[subject]
	if _, ok := m.roles[role]; !ok {
		return ErrRoleNotFound
	}
	if assigned == nil {
		return ErrAssignmentNotFound
	}
	if _, found := assigned[role]; !found {
		return ErrAssignmentNotFound
	}
	delete(assigned, role)
	if len(assigned) == 0 {
		delete(m.assignments, subject)
	}
	m.logger.Info("role assignment revoked", "subject", subject, "role", role)
	return nil
}

// effectiveRoles returns the assigned roles plus every role reached through
// transitive inheritance. The result is a set; iteration order is irrelevant
// to evaluation because permission resolution is key based.
func (m *Manager) effectiveRoles(assigned map[string]struct{}) map[string]struct{} {
	result := maps.Clone(assigned)
	if result == nil {
		result = make(map[string]struct{})
	}
	stack := make([]string, 0, len(assigned))
	for role := range assigned {
		stack = append(stack, role)
	}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for parent := range m.parents[current] {
			if _, seen := result[parent]; !seen {
				result[parent] = struct{}{}
				stack = append(stack, parent)
			}
		}
	}
	return result
}
