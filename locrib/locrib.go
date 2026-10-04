package locrib

import (
	"sort"
	"sync"

	"ontology/adjrib"
	"ontology/policy"
)

type ChangeKind int

const (
	ChangeAdvertise ChangeKind = iota
	ChangeWithdraw
)

type Change struct {
	Peer   int
	Prefix policy.Prefix
	Kind   ChangeKind
	Attrs  policy.Attrs
}

type Engine struct {
	localAS  uint32
	pmax     int
	tab      *adjrib.Table
	mu       sync.RWMutex
	compared uint64
}

func New(localAS uint32, pmax int) *Engine {
	if pmax < 0 {
		pmax = 0
	}
	return &Engine{localAS: localAS, pmax: pmax, tab: adjrib.New(localAS)}
}

func (e *Engine) AddPeer(id int, as uint32, routerID uint32) error {
	if id < 1 || id > 1000000 || routerID == 0 {
		return policy.ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tab.PeerExists(id) {
		return policy.ErrPeerExists
	}
	e.tab.AddPeer(&policy.Peer{
		ID: id, AS: as, RouterID: routerID,
		Internal: as == e.localAS,
		Policy:   []policy.Term{},
	})
	return nil
}

func (e *Engine) Update(id int, p policy.Prefix, a policy.Attrs) ([]Change, error) {
	attrs, err := policy.ValidateAttrs(a)
	if err != nil {
		return nil, err
	}
	if err := policy.ValidatePrefix(p); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.tab.PeerExists(id) {
		return nil, policy.ErrPeerNotFound
	}
	if !e.tab.RawHas(id, p) && e.tab.RawCount(id) >= e.pmax {
		return nil, policy.ErrPrefixLimit
	}
	before := e.snapshotExports(p, false)
	e.tab.SetRaw(id, p, attrs)
	return e.diff(p, before), nil
}

func (e *Engine) Withdraw(id int, p policy.Prefix) ([]Change, error) {
	if err := policy.ValidatePrefix(p); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.tab.PeerExists(id) {
		return nil, policy.ErrPeerNotFound
	}
	before := e.snapshotExports(p, false)
	if !e.tab.Delete(id, p) {
		return nil, policy.ErrRouteNotFound
	}
	return e.diff(p, before), nil
}

func (e *Engine) SetPolicy(id int, terms []policy.Term) ([]Change, error) {
	if err := policy.ValidateTerms(terms); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.tab.PeerExists(id) {
		return nil, policy.ErrPeerNotFound
	}
	affected := e.tab.Prefixes(id)
	before := make(map[policy.Prefix]map[int]policy.Attrs, len(affected))
	for _, p := range affected {
		before[p] = e.snapshotExports(p, false)
	}
	copied := make([]policy.Term, len(terms))
	copy(copied, terms)
	e.tab.ReplacePolicy(id, copied)
	changes := make([]Change, 0)
	for _, p := range affected {
		changes = append(changes, e.diff(p, before[p])...)
	}
	sortChanges(changes)
	return dedup(changes), nil
}

func (e *Engine) Export(id int) []Change {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Change, 0)
	if !e.tab.PeerExists(id) {
		return out
	}
	for _, p := range e.tab.AllPrefixes() {
		routes := e.tab.PostRoutes(p)
		best := selectBest(routes, nil)
		if best == nil {
			continue
		}
		if a, ok := exportAttrs(e.tab.LookupPeer(id), best, e.localAS); ok {
			out = append(out, Change{Peer: id, Prefix: p, Kind: ChangeAdvertise, Attrs: a})
		}
	}
	sortChanges(out)
	return out
}

// Compared 返回选优累计考察的策略后路由条数。
func (e *Engine) Compared() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.compared
}

// snapshotExports 计算单个前缀当前对每个邻居的导出属性（nil 值表示不通告）。
func (e *Engine) snapshotExports(p policy.Prefix, count bool) map[int]policy.Attrs {
	routes := e.tab.PostRoutes(p)
	var counter *uint64
	if count {
		counter = &e.compared
	}
	best := selectBest(routes, counter)
	view := make(map[int]policy.Attrs)
	if best == nil {
		return view
	}
	for _, id := range e.tab.PeerIDs() {
		if a, ok := exportAttrs(e.tab.LookupPeer(id), best, e.localAS); ok {
			view[id] = a
		}
	}
	return view
}

func (e *Engine) diff(p policy.Prefix, before map[int]policy.Attrs) []Change {
	after := e.snapshotExports(p, true)
	ids := map[int]struct{}{}
	for id := range before {
		ids[id] = struct{}{}
	}
	for id := range after {
		ids[id] = struct{}{}
	}
	changes := make([]Change, 0, len(ids))
	for id := range ids {
		ba, bok := before[id]
		aa, aok := after[id]
		switch {
		case bok && aok && policy.AttrsEqual(ba, aa):
		case bok && aok:
			changes = append(changes, Change{Peer: id, Prefix: p, Kind: ChangeAdvertise, Attrs: aa})
		case bok && !aok:
			changes = append(changes, Change{Peer: id, Prefix: p, Kind: ChangeWithdraw})
		case !bok && aok:
			changes = append(changes, Change{Peer: id, Prefix: p, Kind: ChangeAdvertise, Attrs: aa})
		}
	}
	sortChanges(changes)
	return changes
}

// selectBest 严格按 localPref/路径长/origin 过滤后按邻接 AS 分组：
// 组内 med、外部优先、routerID、id 选代表；组间只比外部优先、routerID、id。
func selectBest(routes []adjrib.Route, compared *uint64) *adjrib.Route {
	if compared != nil {
		*compared += uint64(len(routes))
	}
	if len(routes) == 0 {
		return nil
	}
	// 顺序过滤（非独立极值）：localPref 最大 → 路径最短 → origin 最小。
	bestLP := routes[0].Attrs.LocalPref
	for _, r := range routes[1:] {
		if r.Attrs.LocalPref > bestLP {
			bestLP = r.Attrs.LocalPref
		}
	}
	layer := make([]adjrib.Route, 0, len(routes))
	for _, r := range routes {
		if r.Attrs.LocalPref == bestLP {
			layer = append(layer, r)
		}
	}
	bestLen := len(layer[0].Attrs.ASPath)
	for _, r := range layer[1:] {
		if len(r.Attrs.ASPath) < bestLen {
			bestLen = len(r.Attrs.ASPath)
		}
	}
	next := layer[:0]
	for _, r := range layer {
		if len(r.Attrs.ASPath) == bestLen {
			next = append(next, r)
		}
	}
	layer = next
	bestOrigin := layer[0].Attrs.Origin
	for _, r := range layer[1:] {
		if r.Attrs.Origin < bestOrigin {
			bestOrigin = r.Attrs.Origin
		}
	}
	cand := make([]adjrib.Route, 0, len(layer))
	for _, r := range layer {
		if r.Attrs.Origin == bestOrigin {
			cand = append(cand, r)
		}
	}
	groups := make(map[uint32][]adjrib.Route)
	for _, r := range cand {
		nbr := uint32(0)
		if len(r.Attrs.ASPath) > 0 {
			nbr = r.Attrs.ASPath[0]
		}
		groups[nbr] = append(groups[nbr], r)
	}
	winners := make([]adjrib.Route, 0, len(groups))
	for _, g := range groups {
		w := g[0]
		for _, r := range g[1:] {
			if withinGroupLess(r, w) {
				w = r
			}
		}
		winners = append(winners, w)
	}
	best := winners[0]
	for _, r := range winners[1:] {
		if acrossGroupLess(r, best) {
			best = r
		}
	}
	return &best
}

func extBetter(a, b adjrib.Route) bool { return !a.Internal && b.Internal }

func withinGroupLess(a, b adjrib.Route) bool {
	if a.Attrs.MED != b.Attrs.MED {
		return a.Attrs.MED < b.Attrs.MED
	}
	return acrossGroupLess(a, b)
}

func acrossGroupLess(a, b adjrib.Route) bool {
	if a.Internal != b.Internal {
		return extBetter(a, b)
	}
	if a.RouterID != b.RouterID {
		return a.RouterID < b.RouterID
	}
	return a.PeerID < b.PeerID
}

func hasCommunity(a policy.Attrs, c uint32) bool {
	for _, v := range a.Communities {
		if v == c {
			return true
		}
	}
	return false
}

// exportAttrs 计算 best 对目标邻居 q 的导出；ok=false 表示不通告。
func exportAttrs(q *policy.Peer, best *adjrib.Route, localAS uint32) (policy.Attrs, bool) {
	if best == nil || q == nil || best.PeerID == q.ID {
		return policy.Attrs{}, false
	}
	// iBGP 不转发从内部邻居学到的路由；外部来源可转发给内部邻居。
	if best.Internal && q.Internal {
		return policy.Attrs{}, false
	}
	if hasCommunity(best.Attrs, policy.NoAdvertise) {
		return policy.Attrs{}, false
	}
	if hasCommunity(best.Attrs, policy.NoExport) && !q.Internal {
		return policy.Attrs{}, false
	}
	a := best.Attrs.Clone()
	if !q.Internal {
		a.ASPath = append(append([]uint32{}, localAS), a.ASPath...)
		a.LocalPref = 0
		a.MED = 0
	}
	return a, true
}

func sortChanges(c []Change) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].Peer != c[j].Peer {
			return c[i].Peer < c[j].Peer
		}
		if c[i].Prefix.Addr != c[j].Prefix.Addr {
			return c[i].Prefix.Addr < c[j].Prefix.Addr
		}
		return c[i].Prefix.Len < c[j].Prefix.Len
	})
}

func dedup(c []Change) []Change {
	if len(c) <= 1 {
		return c
	}
	out := c[:1]
	for _, ch := range c[1:] {
		last := &out[len(out)-1]
		if last.Peer == ch.Peer && last.Prefix == ch.Prefix {
			*last = ch
			continue
		}
		out = append(out, ch)
	}
	return out
}
