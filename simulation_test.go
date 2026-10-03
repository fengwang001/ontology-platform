package ontology

import (
	"ontology/policy"
)

// 朴素逐步模拟：不建索引，每次判定都全量扫描重算，用于对照。
type mGrant struct {
	id, requester, resource   string
	start, nominalEnd, rw, lk int64
	approvedAt, rejectedAt    int64
}

type model struct {
	clock  int64
	pol    policy.Policy
	actors map[string]actor
	grants []*mGrant
}

func newModel(p policy.Policy) *model { return &model{pol: p, actors: map[string]actor{}} }

func (m *model) effEnd(g *mGrant) int64 {
	if g.rejectedAt >= 0 {
		return min(g.nominalEnd, g.rejectedAt)
	}
	if g.approvedAt >= 0 {
		return g.nominalEnd
	}
	return min(g.nominalEnd, g.start+g.rw)
}

func (m *model) register(now int64, name string, role policy.Role, scopes []string) error {
	if now < 0 || now > policy.MaxTime || len(name) == 0 || len(name) > 64 || !policy.ValidRole(role) {
		return ErrInvalidParam
	}
	if role == policy.Manager {
		if len(scopes) < 1 || len(scopes) > 8 {
			return ErrInvalidParam
		}
	} else if len(scopes) != 0 {
		return ErrInvalidParam
	}
	for _, sc := range scopes {
		if !policy.ValidPath(sc) {
			return ErrInvalidParam
		}
	}
	if now < m.clock {
		return ErrClockRegression
	}
	if _, ok := m.actors[name]; ok {
		return ErrDuplicate
	}
	m.actors[name] = actor{role: role, scopes: scopes}
	m.clock = now
	return nil
}

func (m *model) setPolicy(now int64, p policy.Policy) error {
	if now < 0 || now > policy.MaxTime || !policy.Valid(p) {
		return ErrInvalidParam
	}
	if now < m.clock {
		return ErrClockRegression
	}
	m.pol = p
	m.clock = now
	return nil
}

func (m *model) request(now int64, id, requester, resource string, d int64) error {
	if now < 0 || now > policy.MaxTime || id == "" || !policy.ValidPath(resource) ||
		d < 1 || d > m.pol.Dmax {
		return ErrInvalidParam
	}
	if now < m.clock {
		return ErrClockRegression
	}
	for _, g := range m.grants {
		if g.id == id {
			return ErrDuplicate
		}
	}
	if _, ok := m.actors[requester]; !ok {
		return ErrNotRegistered
	}
	for _, g := range m.grants {
		if g.requester != requester {
			continue
		}
		ls, ok := int64(0), false
		if g.rejectedAt >= 0 {
			ls, ok = g.rejectedAt, true
		} else if g.approvedAt < 0 {
			ls, ok = g.start+g.rw, true
		}
		if ok && ls <= now && now < ls+g.lk {
			return ErrLocked
		}
	}
	var active int64
	for _, g := range m.grants {
		if g.requester == requester && g.start <= now && now < m.effEnd(g) {
			active++
		}
	}
	if active >= m.pol.Cmax {
		return ErrTooManyConcurrent
	}
	m.grants = append(m.grants, &mGrant{id: id, requester: requester, resource: resource,
		start: now, nominalEnd: now + d, rw: m.pol.Rw, lk: m.pol.Lk, approvedAt: -1, rejectedAt: -1})
	m.clock = now
	return nil
}

func (m *model) review(now int64, id, approver string, approve bool) error {
	if now < 0 || now > policy.MaxTime || id == "" || approver == "" {
		return ErrInvalidParam
	}
	if now < m.clock {
		return ErrClockRegression
	}
	var g *mGrant
	for _, x := range m.grants {
		if x.id == id {
			g = x
		}
	}
	if g == nil {
		return ErrGrantNotFound
	}
	if approver == g.requester {
		return ErrSelfReview
	}
	a, ok := m.actors[approver]
	if !ok || !canReview(a, g.resource) {
		return ErrPermission
	}
	if g.approvedAt >= 0 || g.rejectedAt >= 0 {
		return ErrAlreadyDecided
	}
	if now > g.start+g.rw {
		return ErrOverdue
	}
	if approve {
		g.approvedAt = now
	} else {
		g.rejectedAt = now
	}
	m.clock = now
	return nil
}

func (m *model) access(principal, resource string, t int64) bool {
	if _, ok := m.actors[principal]; !ok {
		return false
	}
	for _, g := range m.grants {
		if g.requester == principal && policy.Covers(g.resource, resource) &&
			g.start <= t && t < m.effEnd(g) {
			return true
		}
	}
	return false
}
