package ontology

import "sort"

type naiveConstraint struct {
	roles map[string]struct{}
	n     int
}

type naiveSession struct {
	user   string
	active map[string]struct{}
}

type naiveManager struct {
	smax, cmax  int
	roles       map[string]struct{}
	users       map[string]map[string]struct{}
	sessions    map[string]*naiveSession
	perms       map[string]map[string]struct{}
	edges       map[[2]string]struct{}
	ssd, dsd    map[string]naiveConstraint
	checkUsers  int
	checkSess   int
	recompUsers int
	recompSess  int
}

func newNaive(smax, cmax int) *naiveManager {
	return &naiveManager{
		smax:     smax,
		cmax:     cmax,
		roles:    map[string]struct{}{},
		users:    map[string]map[string]struct{}{},
		sessions: map[string]*naiveSession{},
		perms:    map[string]map[string]struct{}{},
		edges:    map[[2]string]struct{}{},
		ssd:      map[string]naiveConstraint{},
		dsd:      map[string]naiveConstraint{},
	}
}

func (n *naiveManager) juniors(start string) map[string]struct{} {
	seen := map[string]struct{}{start: {}}
	stack := []string{start}
	for len(stack) > 0 {
		role := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for edge := range n.edges {
			if edge[0] == role {
				if _, ok := seen[edge[1]]; !ok {
					seen[edge[1]] = struct{}{}
					stack = append(stack, edge[1])
				}
			}
		}
	}
	return seen
}

func (n *naiveManager) reaches(a, b string) bool { _, ok := n.juniors(a)[b]; return ok }

func (n *naiveManager) auth(user string) map[string]struct{} {
	out := map[string]struct{}{}
	for role := range n.users[user] {
		unionInto(out, n.juniors(role))
	}
	return out
}

func (n *naiveManager) eff(s *naiveSession) map[string]struct{} {
	out := map[string]struct{}{}
	for role := range s.active {
		unionInto(out, n.juniors(role))
	}
	return out
}

func (n *naiveManager) violated(roles map[string]struct{}, c naiveConstraint) bool {
	count := 0
	for role := range c.roles {
		if _, ok := roles[role]; ok {
			count++
			if count >= c.n {
				return true
			}
		}
	}
	return false
}

func sortedNaive[T any](items map[string]T) []string {
	out := make([]string, 0, len(items))
	for key := range items {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func (n *naiveManager) ssdError(users []string, auths map[string]map[string]struct{}) *RBACError {
	users = append([]string(nil), users...)
	sort.Strings(users)
	for _, name := range sortedNaive(n.ssd) {
		c := n.ssd[name]
		for _, user := range users {
			if n.violated(auths[user], c) {
				return violation("user", user, name)
			}
		}
	}
	return nil
}

func (n *naiveManager) dsdError(sessions []string, effs map[string]map[string]struct{}) *RBACError {
	sessions = append([]string(nil), sessions...)
	sort.Strings(sessions)
	for _, name := range sortedNaive(n.dsd) {
		c := n.dsd[name]
		for _, sid := range sessions {
			if n.violated(effs[sid], c) {
				return violation("session", sid, name)
			}
		}
	}
	return nil
}
