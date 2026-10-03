package grant

import (
	"ontology/policy"
)

// 朴素模拟：独立于 Service 的逐步实现，用平铺循环逐条重放操作，
// 与 Service 的结果逐步对照。
type simPrin struct {
	role   policy.Role
	scopes []string
}

type simGrant struct {
	req, res           string
	start, end, rw, lk int64
	appr, rej          int64
}

type sim struct {
	clock int64
	pol   policy.Policy
	prins map[string]simPrin
	gs    map[string]*simGrant
}

func newSim(p policy.Policy) *sim {
	return &sim{pol: p, prins: map[string]simPrin{}, gs: map[string]*simGrant{}}
}

func simEffEnd(g *simGrant) int64 {
	if g.rej >= 0 {
		return min(g.end, g.rej)
	}
	if g.appr >= 0 {
		return g.end
	}
	return min(g.end, g.start+g.rw)
}

func (sm *sim) register(now int64, name string, role policy.Role, scopes []string) error {
	if !policy.ValidName(name) || !policy.ValidRole(role) || !policy.ValidScopes(role, scopes) {
		return ErrParam
	}
	if now < sm.clock {
		return ErrClock
	}
	if _, ok := sm.prins[name]; ok {
		return ErrDuplicate
	}
	sm.clock = now
	sm.prins[name] = simPrin{role, scopes}
	return nil
}

func (sm *sim) setPolicy(now int64, p policy.Policy) error {
	if !p.Valid() {
		return ErrParam
	}
	if now < sm.clock {
		return ErrClock
	}
	sm.clock = now
	sm.pol = p
	return nil
}

func (sm *sim) request(now int64, id, req, res string, d int64) error {
	if id == "" || !policy.ValidPath(res) || d < 1 || d > sm.pol.Dmax {
		return ErrParam
	}
	if now < sm.clock {
		return ErrClock
	}
	if _, ok := sm.gs[id]; ok {
		return ErrDuplicate
	}
	if _, ok := sm.prins[req]; !ok {
		return ErrNotFound
	}
	var active int64
	for _, g := range sm.gs {
		if g.req != req {
			continue
		}
		ls := int64(-1)
		if g.rej >= 0 {
			ls = g.rej
		} else if g.appr < 0 {
			ls = g.start + g.rw
		}
		if ls >= 0 && ls <= now && now < ls+g.lk {
			return ErrLocked
		}
		if g.start <= now && now < simEffEnd(g) {
			active++
		}
	}
	if active >= sm.pol.Cmax {
		return ErrTooMany
	}
	sm.clock = now
	sm.gs[id] = &simGrant{req: req, res: res, start: now, end: now + d,
		rw: sm.pol.Rw, lk: sm.pol.Lk, appr: -1, rej: -1}
	return nil
}

func (sm *sim) review(now int64, id, approver string, approve bool) error {
	if id == "" || approver == "" {
		return ErrParam
	}
	if now < sm.clock {
		return ErrClock
	}
	g, ok := sm.gs[id]
	if !ok {
		return ErrNotFound
	}
	if approver == g.req {
		return ErrSelfReview
	}
	p, ok := sm.prins[approver]
	perm := ok && p.role == policy.RoleSecurity
	if ok && p.role == policy.RoleManager {
		for _, sc := range p.scopes {
			if policy.Covers(sc, g.res) {
				perm = true
			}
		}
	}
	if !perm {
		return ErrPermission
	}
	if g.appr >= 0 || g.rej >= 0 {
		return ErrDecided
	}
	if now > g.start+g.rw {
		return ErrOverdue
	}
	sm.clock = now
	if approve {
		g.appr = now
	} else {
		g.rej = now
	}
	return nil
}

func (sm *sim) access(who, res string, t int64) bool {
	if _, ok := sm.prins[who]; !ok {
		return false
	}
	for _, g := range sm.gs {
		if g.req == who && policy.Covers(g.res, res) && g.start <= t && t < simEffEnd(g) {
			return true
		}
	}
	return false
}
