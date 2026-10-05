package locrib

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"

	"ontology/policy"
)

// 朴素模拟：每次操作后从原始表全量重算策略后路由、最优路径与导出，
// 与引擎返回的差量对照。判定依据即规格规则本身的全量重述。

type simPeer struct {
	as       uint32
	routerID uint32
	internal bool
	raw      map[Prefix]Attrs
	terms    []Term
}

type sim struct {
	localAS  uint32
	pmax     int
	peers    map[uint32]*simPeer
	exported map[uint32]map[Prefix]Attrs
}

func newSim(localAS uint32, pmax int) *sim {
	return &sim{
		localAS:  localAS,
		pmax:     pmax,
		peers:    make(map[uint32]*simPeer),
		exported: make(map[uint32]map[Prefix]Attrs),
	}
}

func (s *sim) addPeer(id, as, rid uint32) error {
	if id == 0 || id > 1000000 || rid == 0 {
		return ErrInvalidParam
	}
	if _, ok := s.peers[id]; ok {
		return ErrPeerExists
	}
	s.peers[id] = &simPeer{as: as, routerID: rid, internal: as == s.localAS, raw: make(map[Prefix]Attrs)}
	s.exported[id] = make(map[Prefix]Attrs)
	return nil
}

func (s *sim) update(peer uint32, p Prefix, attrs Attrs) ([]Change, error) {
	if !policy.ValidPrefix(p) || !policy.ValidAttrs(attrs) {
		return nil, ErrInvalidParam
	}
	sp, ok := s.peers[peer]
	if !ok {
		return nil, ErrPeerNotFound
	}
	if _, ok := sp.raw[p]; !ok && len(sp.raw) >= s.pmax {
		return nil, ErrPrefixLimit
	}
	sp.raw[p] = attrs.Clone()
	return s.recompute(), nil
}

func (s *sim) withdraw(peer uint32, p Prefix) ([]Change, error) {
	if !policy.ValidPrefix(p) {
		return nil, ErrInvalidParam
	}
	sp, ok := s.peers[peer]
	if !ok {
		return nil, ErrPeerNotFound
	}
	if _, ok := sp.raw[p]; !ok {
		return nil, ErrRouteNotFound
	}
	delete(sp.raw, p)
	return s.recompute(), nil
}

func (s *sim) setPolicy(peer uint32, terms []Term) ([]Change, error) {
	if !policy.ValidTerms(terms) {
		return nil, ErrInvalidParam
	}
	sp, ok := s.peers[peer]
	if !ok {
		return nil, ErrPeerNotFound
	}
	sp.terms = append([]Term(nil), terms...)
	return s.recompute(), nil
}

type simRoute struct {
	peer  uint32
	attrs Attrs
}

// simBestPath 按规格独立实现的最优路径选择：
// localPref 最大→asPath 最短→origin 最小得候选集；按邻接 AS 分组，
// 组内 med 小→外部优先→routerID 小→id 小；组间外部优先→routerID 小→id 小。
func simBestPath(routes []simRoute, peers map[uint32]*simPeer) (uint32, Attrs, bool) {
	if len(routes) == 0 {
		return 0, Attrs{}, false
	}
	cands := routes
	cands = simFilter(cands, func(a, b Attrs) int { return int(b.LocalPref) - int(a.LocalPref) })
	cands = simFilter(cands, func(a, b Attrs) int { return len(a.AsPath) - len(b.AsPath) })
	cands = simFilter(cands, func(a, b Attrs) int { return int(a.Origin) - int(b.Origin) })

	groups := make(map[uint32]simRoute)
	for _, r := range cands {
		adj := uint32(0)
		if len(r.attrs.AsPath) > 0 {
			adj = r.attrs.AsPath[0]
		}
		rep, ok := groups[adj]
		if !ok || simInGroupBetter(r, rep, peers) {
			groups[adj] = r
		}
	}
	var best simRoute
	first := true
	for _, rep := range groups {
		if first || simTieBreak(rep, best, peers) {
			best = rep
			first = false
		}
	}
	return best.peer, best.attrs, true
}

// simFilter 保留按 cmp 序最小的元素（cmp<0 表示 a 优于 b）。
func simFilter(routes []simRoute, cmp func(a, b Attrs) int) []simRoute {
	out := routes[:1]
	for _, r := range routes[1:] {
		switch c := cmp(r.attrs, out[0].attrs); {
		case c < 0:
			out = append(out[:0], r)
		case c == 0:
			out = append(out, r)
		}
	}
	return out
}

// simInGroupBetter 组内比较：med 小→外部优先→routerID 小→id 小。
func simInGroupBetter(a, b simRoute, peers map[uint32]*simPeer) bool {
	if a.attrs.Med != b.attrs.Med {
		return a.attrs.Med < b.attrs.Med
	}
	return simTieBreak(a, b, peers)
}

// simTieBreak 外部优先→routerID 小→邻居 id 小。
func simTieBreak(a, b simRoute, peers map[uint32]*simPeer) bool {
	pa, pb := peers[a.peer], peers[b.peer]
	if pa.internal != pb.internal {
		return !pa.internal
	}
	if pa.routerID != pb.routerID {
		return pa.routerID < pb.routerID
	}
	return a.peer < b.peer
}

// simExport 计算邻居 q 对拥有最优路径的前缀应收到的导出属性。
func (s *sim) simExport(q, bestPeer uint32, bestAttrs Attrs) (Attrs, bool) {
	if bestPeer == q {
		return Attrs{}, false
	}
	src, dst := s.peers[bestPeer], s.peers[q]
	if src.internal && dst.internal {
		return Attrs{}, false
	}
	if policy.HasCommunity(bestAttrs, policy.NoAdvertise) {
		return Attrs{}, false
	}
	if !dst.internal && policy.HasCommunity(bestAttrs, policy.NoExport) {
		return Attrs{}, false
	}
	out := bestAttrs.Clone()
	if !dst.internal {
		out.AsPath = append([]uint32{s.localAS}, bestAttrs.AsPath...)
		out.LocalPref = 0
		out.Med = 0
	}
	return out, true
}

// recompute 全量重算策略后路由、最优路径与导出，与快照比较产生差量。
func (s *sim) recompute() []Change {
	byPrefix := make(map[Prefix][]simRoute)
	for id, sp := range s.peers {
		for p, raw := range sp.raw {
			if policy.HasAS(raw, s.localAS) {
				continue
			}
			in := raw
			if !sp.internal {
				in = raw.Clone()
				in.LocalPref = 100
			}
			out, ok := policy.Eval(sp.terms, p, in, sp.as)
			if !ok {
				continue
			}
			byPrefix[p] = append(byPrefix[p], simRoute{peer: id, attrs: out})
		}
	}
	newExp := make(map[uint32]map[Prefix]Attrs)
	for id := range s.peers {
		newExp[id] = make(map[Prefix]Attrs)
	}
	for p, routes := range byPrefix {
		bp, bAttrs, ok := simBestPath(routes, s.peers)
		if !ok {
			continue
		}
		for q := range s.peers {
			if out, ok := s.simExport(q, bp, bAttrs); ok {
				newExp[q][p] = out
			}
		}
	}
	var changes []Change
	for id := range s.peers {
		for p, na := range newExp[id] {
			old, had := s.exported[id][p]
			if !had || !policy.EqualAttrs(old, na) {
				changes = append(changes, Change{Peer: id, Prefix: p, Attrs: na})
			}
		}
		for p := range s.exported[id] {
			if _, ok := newExp[id][p]; !ok {
				changes = append(changes, Change{Peer: id, Prefix: p, Withdraw: true})
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.Peer != b.Peer {
			return a.Peer < b.Peer
		}
		if a.Prefix.Addr != b.Prefix.Addr {
			return a.Prefix.Addr < b.Prefix.Addr
		}
		return a.Prefix.Len < b.Prefix.Len
	})
	s.exported = newExp
	return changes
}

// ---- 随机场景生成 ----

type scenarioOp struct {
	kind   string
	peer   uint32
	prefix Prefix
	attrs  Attrs
	terms  []Term
}

func (o scenarioOp) String() string {
	switch o.kind {
	case "update":
		return fmt.Sprintf("Update(peer=%d, prefix=%v, attrs=%+v)", o.peer, o.prefix, o.attrs)
	case "withdraw":
		return fmt.Sprintf("Withdraw(peer=%d, prefix=%v)", o.peer, o.prefix)
	default:
		return fmt.Sprintf("SetPolicy(peer=%d, terms=%+v)", o.peer, o.terms)
	}
}

var simPrefixPool = []Prefix{
	{Addr: 0x00000000, Len: 0},
	{Addr: 0x0A000000, Len: 8},
	{Addr: 0x0A010000, Len: 16},
	{Addr: 0x0A010000, Len: 24},
	{Addr: 0x0A010180, Len: 25},
	{Addr: 0xC0000200, Len: 24},
	{Addr: 0xC0000300, Len: 24},
}

func genAttrs(rng *rand.Rand, localAS uint32) Attrs {
	var a Attrs
	asChoices := []uint32{65001, 65002, 65003, 7}
	n := rng.IntN(4)
	for i := 0; i < n; i++ {
		a.AsPath = append(a.AsPath, asChoices[rng.IntN(len(asChoices))])
	}
	if rng.IntN(5) == 0 { // 20% 概率制造环路
		a.AsPath = append(a.AsPath, localAS)
	}
	a.LocalPref = []uint32{0, 50, 100, 200}[rng.IntN(4)]
	a.Med = []uint32{0, 10, 50, 100}[rng.IntN(4)]
	a.Origin = uint8(rng.IntN(3))
	comms := []uint32{0x00010029, 0x00020029, policy.NoExport, policy.NoAdvertise}
	for _, c := range comms {
		if rng.IntN(6) == 0 {
			a.Communities = append(a.Communities, c)
		}
	}
	return a
}

func genTerms(rng *rand.Rand) []Term {
	n := rng.IntN(4)
	terms := make([]Term, 0, n+1)
	for i := 0; i < n; i++ {
		var t Term
		if rng.IntN(5) < 2 {
			base := simPrefixPool[rng.IntN(len(simPrefixPool))]
			ge := base.Len + uint8(rng.IntN(int(33-base.Len)))
			le := ge + uint8(rng.IntN(int(33-ge)))
			t.Match.Prefix = &PrefixCond{Addr: base.Addr, Len: base.Len, Ge: ge, Le: le}
		}
		if rng.IntN(4) == 0 {
			c := []uint32{0x00010029, 0x00020029, policy.NoExport}[rng.IntN(3)]
			t.Match.Community = &c
		}
		if rng.IntN(4) == 0 {
			as := []uint32{65001, 65002, 65003, 7}[rng.IntN(4)]
			t.Match.AS = &as
		}
		switch rng.IntN(20) {
		case 0, 1, 2, 3, 4:
			t.Action = ActionReject
		case 5, 6, 7, 8, 9, 10, 11:
			t.Action = ActionNext
		default:
			t.Action = ActionAccept
		}
		if rng.IntN(5) < 2 {
			lp := []uint32{50, 150, 200, 300}[rng.IntN(4)]
			t.Mods.LocalPref = &lp
		}
		if rng.IntN(4) == 0 {
			c := []uint32{0x00010029, 0x00020029, 42}[rng.IntN(3)]
			t.Mods.AddCommunity = &c
		}
		if rng.IntN(4) == 0 {
			t.Mods.Prepend = 1 + rng.IntN(3)
		}
		terms = append(terms, t)
	}
	if rng.IntN(2) == 0 {
		terms = append(terms, Term{Action: ActionAccept})
	}
	return terms
}

// genScenario 生成邻居配置与操作序列（含约 10% 非法操作）。
func genScenario(rng *rand.Rand, localAS uint32) (peers []simPeer, ids []uint32, pmax int, ops []scenarioOp) {
	n := 2 + rng.IntN(4)
	for i := 0; i < n; i++ {
		id := uint32(i + 1)
		as := uint32(65001 + rng.IntN(3))
		if rng.IntN(10) < 3 {
			as = localAS
		}
		peers = append(peers, simPeer{as: as, routerID: uint32(1 + rng.IntN(7)), internal: as == localAS})
		ids = append(ids, id)
	}
	pmax = 2 + rng.IntN(5)
	// 初始策略：60% 恒 accept，否则随机。
	for _, id := range ids {
		var terms []Term
		if rng.IntN(10) < 6 {
			terms = []Term{{Action: ActionAccept}}
		} else {
			terms = genTerms(rng)
		}
		ops = append(ops, scenarioOp{kind: "setpolicy", peer: id, terms: terms})
	}
	for i := 0; i < 30; i++ {
		peer := ids[rng.IntN(len(ids))]
		p := simPrefixPool[rng.IntN(len(simPrefixPool))]
		switch r := rng.IntN(10); {
		case r < 5:
			ops = append(ops, scenarioOp{kind: "update", peer: peer, prefix: p, attrs: genAttrs(rng, localAS)})
		case r < 7:
			ops = append(ops, scenarioOp{kind: "withdraw", peer: peer, prefix: p})
		case r < 9:
			ops = append(ops, scenarioOp{kind: "setpolicy", peer: peer, terms: genTerms(rng)})
		default:
			ops = append(ops, genInvalidOp(rng, peer, p, localAS))
		}
	}
	return peers, ids, pmax, ops
}

func genInvalidOp(rng *rand.Rand, peer uint32, p Prefix, localAS uint32) scenarioOp {
	switch rng.IntN(7) {
	case 0: // 主机位非零
		return scenarioOp{kind: "update", peer: peer, prefix: Prefix{Addr: p.Addr | 1, Len: 8}, attrs: genAttrs(rng, localAS)}
	case 1: // 团体重复
		a := genAttrs(rng, localAS)
		a.Communities = []uint32{5, 5}
		return scenarioOp{kind: "update", peer: peer, prefix: p, attrs: a}
	case 2: // asPath 过长
		a := genAttrs(rng, localAS)
		a.AsPath = make([]uint32, 65)
		return scenarioOp{kind: "update", peer: peer, prefix: p, attrs: a}
	case 3: // origin 非法
		a := genAttrs(rng, localAS)
		a.Origin = 3
		return scenarioOp{kind: "update", peer: peer, prefix: p, attrs: a}
	case 4: // 邻居不存在
		return scenarioOp{kind: "update", peer: 999, prefix: p, attrs: genAttrs(rng, localAS)}
	case 5: // 邻居不存在
		return scenarioOp{kind: "withdraw", peer: 999, prefix: p}
	default: // 策略条款非法（ge < l）
		return scenarioOp{kind: "setpolicy", peer: peer, terms: []Term{
			{Action: ActionAccept, Match: Match{Prefix: &PrefixCond{Addr: 0x0A000000, Len: 8, Ge: 7, Le: 24}}},
		}}
	}
}

// applyOp 对引擎或模拟器执行同一操作。
func applyOp(e *Engine, s *sim, o scenarioOp) (gotC, wantC []Change, gotErr, wantErr error) {
	switch o.kind {
	case "update":
		gotC, gotErr = e.Update(o.peer, o.prefix, o.attrs)
		wantC, wantErr = s.update(o.peer, o.prefix, o.attrs)
	case "withdraw":
		gotC, gotErr = e.Withdraw(o.peer, o.prefix)
		wantC, wantErr = s.withdraw(o.peer, o.prefix)
	default:
		gotC, gotErr = e.SetPolicy(o.peer, o.terms)
		wantC, wantErr = s.setPolicy(o.peer, o.terms)
	}
	return
}

// 1500 组随机操作序列与全量重算朴素模拟对照。
// 判定依据：引擎差量 == 朴素模拟差量；累计差量（影子状态）== Export 全量；
// 相同操作序列重放得到相同差量。
func TestRandomAgainstNaive(t *testing.T) {
	const localAS = 65000
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewPCG(uint64(seq), 0x9E3779B9))
		peers, ids, pmax, ops := genScenario(rng, localAS)

		eng := New(localAS, pmax)
		engReplay := New(localAS, pmax)
		sm := newSim(localAS, pmax)
		shadow := make(map[uint32]map[Prefix]Attrs)
		for i, id := range ids {
			for _, err := range []error{
				eng.AddPeer(id, peers[i].as, peers[i].routerID),
				engReplay.AddPeer(id, peers[i].as, peers[i].routerID),
				sm.addPeer(id, peers[i].as, peers[i].routerID),
			} {
				if err != nil {
					t.Fatalf("seq %d: AddPeer(%d): %v", seq, id, err)
				}
			}
			shadow[id] = make(map[Prefix]Attrs)
		}

		logThis := seq < 2
		for i, op := range ops {
			gotC, wantC, gotErr, wantErr := applyOp(eng, sm, op)
			replayC, replayErr := applyOne(engReplay, op)
			if logThis {
				t.Logf("seq %d op %d: %s -> changes=%v err=%v", seq, i, op, gotC, gotErr)
			}
			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("seq %d op %d [%s]: err=%v, naive=%v (判定依据: 拒绝次序 参数非法>邻居>路由不存在>超限)",
					seq, i, op, gotErr, wantErr)
			}
			if !errors.Is(replayErr, gotErr) || !equalChanges(replayC, gotC) {
				t.Fatalf("seq %d op %d [%s]: replay mismatch (判定依据: 相同操作序列重放得到相同差量)",
					seq, i, op)
			}
			if gotErr != nil {
				continue
			}
			if !equalChanges(gotC, wantC) {
				t.Fatalf("seq %d op %d [%s]:\n got=%+v\nnaive=%+v (判定依据: 差量与全量重算朴素模拟一致)",
					seq, i, op, gotC, wantC)
			}
			for _, ch := range gotC {
				if ch.Withdraw {
					delete(shadow[ch.Peer], ch.Prefix)
				} else {
					shadow[ch.Peer][ch.Prefix] = ch.Attrs
				}
			}
			for _, id := range ids {
				entries, err := eng.Export(id)
				if err != nil {
					t.Fatalf("seq %d op %d: Export(%d): %v", seq, i, id, err)
				}
				if len(entries) != len(shadow[id]) {
					t.Fatalf("seq %d op %d: Export(%d) has %d entries, shadow has %d (判定依据: 累计差量==Export 全量)",
						seq, i, id, len(entries), len(shadow[id]))
				}
				for _, en := range entries {
					if !policy.EqualAttrs(en.Attrs, shadow[id][en.Prefix]) {
						t.Fatalf("seq %d op %d: Export(%d)[%v]=%+v, shadow=%+v (判定依据: 累计差量==Export 全量)",
							seq, i, id, en.Prefix, en.Attrs, shadow[id][en.Prefix])
					}
				}
			}
		}
		if seq%300 == 0 {
			t.Logf("seq %d done: peers=%d pmax=%d ops=%d", seq, len(ids), pmax, len(ops))
		}
	}
}

func applyOne(e *Engine, o scenarioOp) ([]Change, error) {
	switch o.kind {
	case "update":
		return e.Update(o.peer, o.prefix, o.attrs)
	case "withdraw":
		return e.Withdraw(o.peer, o.prefix)
	default:
		return e.SetPolicy(o.peer, o.terms)
	}
}
