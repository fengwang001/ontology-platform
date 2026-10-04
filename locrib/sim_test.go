package locrib

import (
	"fmt"

	"ontology/policy"
)

// ---------------- 朴素全量重算模拟器（独立实现，供差分对照） ----------------

type simPeer struct {
	id       int
	as       uint32
	routerID uint32
	internal bool
	policy   []policy.Term
	raw      map[policy.Prefix]policy.Attrs
}

type simulator struct {
	localAS uint32
	pmax    int
	peers   map[int]*simPeer
	log     []string
}

func newSimulator(localAS uint32, pmax int) *simulator {
	return &simulator{localAS: localAS, pmax: pmax, peers: map[int]*simPeer{}}
}

type simRoute struct {
	peer  *simPeer
	p     policy.Prefix
	attrs policy.Attrs
}

func (s *simulator) addPeer(id int, as, rid uint32) error {
	if id < 1 || id > 1000000 || rid == 0 {
		return policy.ErrInvalidArgument
	}
	if _, ok := s.peers[id]; ok {
		return policy.ErrPeerExists
	}
	s.peers[id] = &simPeer{
		id: id, as: as, routerID: rid, internal: as == s.localAS,
		policy: []policy.Term{}, raw: map[policy.Prefix]policy.Attrs{},
	}
	s.log = append(s.log, fmt.Sprintf("AddPeer id=%d as=%d rid=%d -> ok", id, as, rid))
	return nil
}

func (s *simulator) update(id int, p policy.Prefix, a policy.Attrs) error {
	va, err := policy.ValidateAttrs(a)
	if err != nil {
		s.log = append(s.log, fmt.Sprintf("Update peer=%d %v -> invalid attrs", id, p))
		return err
	}
	if err := policy.ValidatePrefix(p); err != nil {
		s.log = append(s.log, fmt.Sprintf("Update peer=%d -> invalid prefix", id))
		return err
	}
	pr, ok := s.peers[id]
	if !ok {
		return policy.ErrPeerNotFound
	}
	if _, exists := pr.raw[p]; !exists && len(pr.raw) >= s.pmax {
		return policy.ErrPrefixLimit
	}
	pr.raw[p] = va
	s.log = append(s.log, fmt.Sprintf("Update peer=%d prefix=%v/%d attrs={lp=%d med=%d origin=%d path=%v comm=%v} -> accepted",
		id, p.Addr, p.Len, va.LocalPref, va.MED, va.Origin, va.ASPath, va.Communities))
	return nil
}

func (s *simulator) withdraw(id int, p policy.Prefix) error {
	if err := policy.ValidatePrefix(p); err != nil {
		return err
	}
	pr, ok := s.peers[id]
	if !ok {
		return policy.ErrPeerNotFound
	}
	if _, exists := pr.raw[p]; !exists {
		return policy.ErrRouteNotFound
	}
	delete(pr.raw, p)
	s.log = append(s.log, fmt.Sprintf("Withdraw peer=%d %v/%d -> ok", id, p.Addr, p.Len))
	return nil
}

func (s *simulator) setPolicy(id int, terms []policy.Term) error {
	if err := policy.ValidateTerms(terms); err != nil {
		return err
	}
	pr, ok := s.peers[id]
	if !ok {
		return policy.ErrPeerNotFound
	}
	cp := make([]policy.Term, len(terms))
	copy(cp, terms)
	pr.policy = cp
	s.log = append(s.log, fmt.Sprintf("SetPolicy peer=%d terms=%d -> ok", id, len(terms)))
	return nil
}

func (s *simulator) postRoutes(p policy.Prefix) []simRoute {
	var out []simRoute
	for _, pr := range s.peers {
		raw, ok := pr.raw[p]
		if !ok {
			continue
		}
		a, ok := policy.Apply(raw, p, pr.policy, s.localAS, pr.as, !pr.internal)
		if ok {
			out = append(out, simRoute{pr, p, a})
		}
	}
	return out
}

func simExtBetter(a, b *simPeer) bool { return !a.internal && b.internal }

// simBest 与 locrib.selectBest 相同的分组规则（朴素独立书写）。
func simBest(routes []simRoute) *simRoute {
	if len(routes) == 0 {
		return nil
	}
	lp := routes[0].attrs.LocalPref
	for _, r := range routes[1:] {
		if r.attrs.LocalPref > lp {
			lp = r.attrs.LocalPref
		}
	}
	layer := routes[:0:0]
	for _, r := range routes {
		if r.attrs.LocalPref == lp {
			layer = append(layer, r)
		}
	}
	pathLen := len(layer[0].attrs.ASPath)
	for _, r := range layer[1:] {
		if l := len(r.attrs.ASPath); l < pathLen {
			pathLen = l
		}
	}
	l2 := layer[:0:0]
	for _, r := range layer {
		if len(r.attrs.ASPath) == pathLen {
			l2 = append(l2, r)
		}
	}
	origin := l2[0].attrs.Origin
	for _, r := range l2[1:] {
		if r.attrs.Origin < origin {
			origin = r.attrs.Origin
		}
	}
	var cand []simRoute
	for _, r := range l2 {
		if r.attrs.Origin == origin {
			cand = append(cand, r)
		}
	}
	groups := map[uint32][]simRoute{}
	var order []uint32
	for _, r := range cand {
		nbr := uint32(0)
		if len(r.attrs.ASPath) > 0 {
			nbr = r.attrs.ASPath[0]
		}
		if _, ok := groups[nbr]; !ok {
			order = append(order, nbr)
		}
		groups[nbr] = append(groups[nbr], r)
	}
	lessAcross := func(a, b simRoute) bool {
		if a.peer.internal != b.peer.internal {
			return simExtBetter(a.peer, b.peer)
		}
		if a.peer.routerID != b.peer.routerID {
			return a.peer.routerID < b.peer.routerID
		}
		return a.peer.id < b.peer.id
	}
	lessIn := func(a, b simRoute) bool {
		if a.attrs.MED != b.attrs.MED {
			return a.attrs.MED < b.attrs.MED
		}
		return lessAcross(a, b)
	}
	var winners []simRoute
	for _, nbr := range order {
		g := groups[nbr]
		w := g[0]
		for _, r := range g[1:] {
			if lessIn(r, w) {
				w = r
			}
		}
		winners = append(winners, w)
	}
	best := winners[0]
	for _, r := range winners[1:] {
		if lessAcross(r, best) {
			best = r
		}
	}
	return &best
}

func hasSimComm(a policy.Attrs, c uint32) bool {
	for _, x := range a.Communities {
		if x == c {
			return true
		}
	}
	return false
}

// fullExport 朴素全量重算：map[peer]map[prefix]attrs
func (s *simulator) fullExport() map[int]map[policy.Prefix]policy.Attrs {
	out := map[int]map[policy.Prefix]policy.Attrs{}
	for id := range s.peers {
		out[id] = map[policy.Prefix]policy.Attrs{}
	}
	prefixSet := map[policy.Prefix]struct{}{}
	for _, pr := range s.peers {
		for p := range pr.raw {
			prefixSet[p] = struct{}{}
		}
	}
	for p := range prefixSet {
		routes := s.postRoutes(p)
		best := simBest(routes)
		if best == nil {
			continue
		}
		for _, q := range s.peers {
			if q.id == best.peer.id {
				continue
			}
			if best.peer.internal && q.internal {
				continue
			}
			if hasSimComm(best.attrs, policy.NoAdvertise) {
				continue
			}
			if hasSimComm(best.attrs, policy.NoExport) && !q.internal {
				continue
			}
			a := best.attrs.Clone()
			if !q.internal {
				a.ASPath = append(append([]uint32{}, s.localAS), a.ASPath...)
				a.LocalPref = 0
				a.MED = 0
			}
			out[q.id][p] = a
		}
	}
	return out
}
