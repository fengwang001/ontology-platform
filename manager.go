package ontology

import "sync"

type constraint struct {
	name  string
	roles map[string]struct{}
	n     int
}

type session struct {
	user   string
	active map[string]struct{}
}

type InactiveAssignment struct {
	Session string
	Role    string
}

type Manager struct {
	mu       sync.RWMutex
	smax     int
	cmax     int
	roles    map[string]struct{}
	users    map[string]struct{}
	sessions map[string]*session
	perms    map[string]struct{}

	juniorEdges map[string]map[string]struct{}
	seniorEdges map[string]map[string]struct{}

	userRoles          map[string]map[string]struct{}
	roleUsers          map[string]map[string]struct{}
	activeRoleSessions map[string]map[string]struct{}
	rolePerms          map[string]map[string]struct{}
	ssd                map[string]*constraint
	dsd                map[string]*constraint

	inheritCheckedUsers       int
	inheritCheckedSessions    int
	inheritRecomputedUsers    int
	inheritRecomputedSessions int
}

func New(smax, cmax int) (*Manager, error) {
	if smax < 1 || smax > 1000 {
		return nil, invalid("configuration", "Smax", "must be between 1 and 1000")
	}
	if cmax < 1 || cmax > 1000 {
		return nil, invalid("configuration", "Cmax", "must be between 1 and 1000")
	}
	return newManager(smax, cmax), nil
}

func newManager(smax, cmax int) *Manager {
	return &Manager{
		smax:               smax,
		cmax:               cmax,
		roles:              map[string]struct{}{},
		users:              map[string]struct{}{},
		sessions:           map[string]*session{},
		perms:              map[string]struct{}{},
		juniorEdges:        map[string]map[string]struct{}{},
		seniorEdges:        map[string]map[string]struct{}{},
		userRoles:          map[string]map[string]struct{}{},
		roleUsers:          map[string]map[string]struct{}{},
		activeRoleSessions: map[string]map[string]struct{}{},
		rolePerms:          map[string]map[string]struct{}{},
		ssd:                map[string]*constraint{},
		dsd:                map[string]*constraint{},
	}
}

func (m *Manager) AddRole(role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if role == "" {
		return invalid("role", role, "name must not be empty")
	}
	if containsSet(m.roles, role) {
		return conflict("role", role, "already exists")
	}
	m.roles[role] = struct{}{}
	return nil
}

func (m *Manager) AddUser(user string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if user == "" {
		return invalid("user", user, "name must not be empty")
	}
	if containsSet(m.users, user) {
		return conflict("user", user, "already exists")
	}
	m.users[user] = struct{}{}
	m.userRoles[user] = map[string]struct{}{}
	return nil
}

func (m *Manager) AssignPerm(role, perm string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if role == "" {
		return invalid("role", role, "name must not be empty")
	}
	if perm == "" {
		return invalid("permission", perm, "name must not be empty")
	}
	if !containsSet(m.roles, role) {
		return notFound("role", role)
	}
	if containsSet(m.rolePerms[role], perm) {
		return conflict("permission", perm, "already assigned")
	}
	m.perms[perm] = struct{}{}
	if m.rolePerms[role] == nil {
		m.rolePerms[role] = map[string]struct{}{}
	}
	m.rolePerms[role][perm] = struct{}{}
	return nil
}

func (m *Manager) Check(sid, perm string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if sid == "" {
		return false, invalid("session", sid, "name must not be empty")
	}
	if perm == "" {
		return false, invalid("permission", perm, "name must not be empty")
	}
	sess, ok := m.sessions[sid]
	if !ok {
		return false, notFound("session", sid)
	}
	if !containsSet(m.perms, perm) {
		return false, notFound("permission", perm)
	}
	for role := range m.effectiveRoles(sess) {
		if containsSet(m.rolePerms[role], perm) {
			return true, nil
		}
	}
	return false, nil
}
