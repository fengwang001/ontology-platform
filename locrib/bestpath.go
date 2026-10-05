package locrib

import "ontology/policy"

// candidate 为一条参与选优的策略后路由。
type candidate struct {
	peerID uint32
	attrs  Attrs
}

// adjAS 返回邻接 AS：asPath 首元素，空路径记 0。
func adjAS(a Attrs) uint32 {
	if len(a.AsPath) == 0 {
		return 0
	}
	return a.AsPath[0]
}

// bestPath 在同前缀全部策略后路由中选最优：
// 先留 localPref 最大、asPath 最短、origin 最小者得候选集；
// 按邻接 AS 分组，组内 med 小→外部优先→routerID 小→id 小选出组代表；
// 组代表间外部优先→routerID 小→id 小选出最优（跨组不比 med）。
func (e *Engine) bestPath(p Prefix) (candidate, bool) {
	routes := e.byPrefix[p]
	if len(routes) == 0 {
		return candidate{}, false
	}
	e.compared += uint64(len(routes))

	var cands []candidate
	var maxLP uint32
	first := true
	for id, a := range routes {
		if first || a.LocalPref > maxLP {
			maxLP = a.LocalPref
			cands = cands[:0]
			first = false
		}
		if a.LocalPref == maxLP {
			cands = append(cands, candidate{peerID: id, attrs: a})
		}
	}
	cands = filter(cands, func(a, b Attrs) bool { return len(a.AsPath) < len(b.AsPath) })
	cands = filter(cands, func(a, b Attrs) bool { return a.Origin < b.Origin })

	groups := make(map[uint32]candidate)
	for _, c := range cands {
		adj := adjAS(c.attrs)
		rep, ok := groups[adj]
		if !ok || e.inGroupBetter(c, rep) {
			groups[adj] = c
		}
	}

	var best candidate
	first = true
	for _, rep := range groups {
		if first || e.crossGroupBetter(rep, best) {
			best = rep
			first = false
		}
	}
	return best, true
}

// filter 按 less 定义的序保留最小者集合。
func filter(cands []candidate, less func(a, b Attrs) bool) []candidate {
	out := cands[:0]
	for _, c := range cands {
		switch {
		case len(out) == 0 || less(c.attrs, out[0].attrs):
			out = append(out[:0], c)
		case !less(out[0].attrs, c.attrs):
			out = append(out, c)
		}
	}
	return out
}

// inGroupBetter 组内比较：med 小→外部优先→routerID 小→id 小。
func (e *Engine) inGroupBetter(a, b candidate) bool {
	if a.attrs.Med != b.attrs.Med {
		return a.attrs.Med < b.attrs.Med
	}
	return e.tieBreak(a, b)
}

// crossGroupBetter 组间比较：外部优先→routerID 小→id 小（不比 med）。
func (e *Engine) crossGroupBetter(a, b candidate) bool {
	return e.tieBreak(a, b)
}

// tieBreak 外部优先→routerID 小→邻居 id 小。
func (e *Engine) tieBreak(a, b candidate) bool {
	pa, pb := e.peers[a.peerID], e.peers[b.peerID]
	if pa.internal != pb.internal {
		return !pa.internal
	}
	if pa.routerID != pb.routerID {
		return pa.routerID < pb.routerID
	}
	return a.peerID < b.peerID
}

// exportTo 计算邻居 q 对拥有最优路径 best 的前缀应收到的导出属性。
func (e *Engine) exportTo(q uint32, best candidate, hasBest bool) (Attrs, bool) {
	if !hasBest || best.peerID == q {
		return Attrs{}, false
	}
	src, dst := e.peers[best.peerID], e.peers[q]
	if src.internal && dst.internal {
		return Attrs{}, false
	}
	if policy.HasCommunity(best.attrs, policy.NoAdvertise) {
		return Attrs{}, false
	}
	if !dst.internal && policy.HasCommunity(best.attrs, policy.NoExport) {
		return Attrs{}, false
	}
	out := best.attrs.Clone()
	if !dst.internal {
		out.AsPath = append([]uint32{e.localAS}, best.attrs.AsPath...)
		out.LocalPref = 0
		out.Med = 0
	}
	return out, true
}
