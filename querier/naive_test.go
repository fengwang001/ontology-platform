package querier

import (
	"sort"

	"ontology/member"
)

// naiveMem 是朴素模型的单条成员关系。
type naiveMem struct {
	exp     int64
	pending bool
	leaveAt int64 // pending 后末成员查询起始时刻
}

// naive 是逐秒推进的参考实现：每一秒先落地全部到期与应发查询，再处理该时刻输入。
type naive struct {
	p, rb                    int
	ownIP                    uint32
	qi, qri, lmqi, oqpi, gmi int64
	fast                     []bool
	flood                    bool
	gmax, lp                 int
	mems                     map[uint32]map[int]*naiveMem
	routers                  map[int]int64
	isQuerier                bool
	oq                       int64
	gqPhase                  int64 // 当前查询器时段下一条通用查询时刻
	cur                      int64 // 已 tick 到的时刻
	out                      []Query
	drained                  int64 // 上次 drain 时刻
}

func newNaive(P int, ownIP uint32, qi, qri, rb, lmqi int64, fast []bool, flood bool, gmax, lp int) *naive {
	return &naive{
		p: P, rb: int(rb), ownIP: ownIP, qi: qi, lmqi: lmqi,
		qri: qri, gmi: rb*qi + qri, oqpi: rb*qi + qri/2,
		fast: fast, flood: flood, gmax: gmax, lp: lp,
		mems: map[uint32]map[int]*naiveMem{}, routers: map[int]int64{},
		isQuerier: true, gqPhase: 0, cur: -1, drained: -1,
	}
}

// runTo 逐秒 tick 到 now（含），保证事件处理顺序与真实惰性泵一致。
func (n *naive) runTo(now int64) {
	for t := n.cur + 1; t <= now; t++ {
		n.tick(t)
	}
	n.cur = now
}

// drain 取出 (上次 drain, now] 间产生的全部查询并按规范排序。
func (n *naive) drain(now int64) []Query {
	n.runTo(now)
	out := []Query{}
	for _, q := range n.out {
		if q.Time > n.drained && q.Time <= now {
			out = append(out, q)
		}
	}
	n.drained = now
	qsort(out)
	return out
}

func (n *naive) tick(t int64) {
	// 通用查询：查询器时段相位。
	if n.isQuerier && t == n.gqPhase {
		n.out = append(n.out, Query{Time: t, Kind: General})
		n.gqPhase = t + n.qi
	}
	// 路由器端口到期。
	for p, exp := range n.routers {
		if exp <= t {
			delete(n.routers, p)
		}
	}
	// 查询器恰在 oq 恢复（先于本秒输入）。
	if !n.isQuerier && t >= n.oq && n.oq != 0 {
		n.isQuerier = true
		n.gqPhase = t
		n.oq = 0
		// 恢复时刻本身立即发一条通用查询。
		n.out = append(n.out, Query{Time: t, Kind: General})
		n.gqPhase = t + n.qi
	}
	// 到点的特定组查询：成员仍在且 pending，且该查询时刻 < exp。
	if n.isQuerier {
		for grp, ports := range n.mems {
			for p, m := range ports {
				if !m.pending {
					continue
				}
				k := (t - m.leaveAt) / n.lmqi
				if t == m.leaveAt+k*n.lmqi && k >= 0 && k < int64(n.rb) && t < m.exp {
					n.out = append(n.out, Query{Time: t, Kind: GroupSpecific, Group: grp, Port: p})
				}
			}
		}
	}
	// 成员到期放在到点特定组查询判定之后：exp 同刻的查询随到期取消。
	for grp, ports := range n.mems {
		for p, m := range ports {
			if m.exp <= t {
				delete(ports, p)
			}
		}
		if len(ports) == 0 {
			delete(n.mems, grp)
		}
	}
}

func (n *naive) groupCount() int { return len(n.mems) }

func (n *naive) portCount(p int) int {
	c := 0
	for _, ports := range n.mems {
		if ports[p] != nil {
			c++
		}
	}
	return c
}

func (n *naive) report(port int, group uint32, now int64) ([]int, error) {
	if port < 1 || port > n.p || !member.IsMulticastGroup(group) {
		return nil, member.ErrInvalidParam
	}
	if member.IsLocalGroup(group) {
		return nil, member.ErrLocalGroup
	}
	ports := n.mems[group]
	if ports != nil && ports[port] != nil {
		ports[port].exp = now + n.gmi
		ports[port].pending = false
	} else {
		if n.portCount(port) >= n.lp {
			return nil, member.ErrPortLimit
		}
		if ports == nil && n.groupCount() >= n.gmax {
			return nil, member.ErrGroupLimit
		}
		if ports == nil {
			ports = map[int]*naiveMem{}
			n.mems[group] = ports
		}
		ports[port] = &naiveMem{exp: now + n.gmi}
	}
	to := []int{}
	for p := range n.routers {
		if p != port {
			to = append(to, p)
		}
	}
	sort.Ints(to)
	return to, nil
}

func (n *naive) leave(port int, group uint32, now int64) error {
	if port < 1 || port > n.p || !member.IsMulticastGroup(group) {
		return member.ErrInvalidParam
	}
	if member.IsLocalGroup(group) {
		return member.ErrLocalGroup
	}
	if !n.isQuerier {
		return nil
	}
	m := n.mems[group]
	if m == nil || m[port] == nil {
		return member.ErrNotMember
	}
	cur := m[port]
	if n.fast[port] {
		delete(m, port)
		if len(m) == 0 {
			delete(n.mems, group)
		}
		return nil
	}
	if cur.pending {
		return nil
	}
	cur.pending = true
	cur.leaveAt = now
	newExp := now + int64(n.rb)*n.lmqi
	if newExp < cur.exp {
		cur.exp = newExp
	}
	return nil
}

func (n *naive) query(port int, src uint32, now int64) error {
	if port < 1 || port > n.p || src == 0 || src == n.ownIP {
		return member.ErrInvalidParam
	}
	n.routers[port] = now + n.oqpi
	if src < n.ownIP {
		if n.isQuerier {
			n.isQuerier = false
			n.gqPhase = -1 // 让位期间不推进相位；恢复时重置为 oq。
			for _, ports := range n.mems {
				for _, m := range ports {
					m.pending = false
				}
			}
		}
		n.oq = now + n.oqpi
	}
	return nil
}

func (n *naive) forward(group uint32, inPort int) []int {
	mems := []int{}
	if !member.IsLocalGroup(group) {
		for p := range n.mems[group] {
			mems = append(mems, p)
		}
		sort.Ints(mems)
	}
	routers := []int{}
	for p := range n.routers {
		routers = append(routers, p)
	}
	sort.Ints(routers)
	return forwardDecide(n.p, group, inPort, mems, routers, n.flood)
}

func forwardDecide(P int, group uint32, in int, mems, routers []int, flood bool) []int {
	local := member.IsLocalGroup(group)
	if local || (len(mems) == 0 && flood) {
		out := []int{}
		for p := 1; p <= P; p++ {
			if p != in {
				out = append(out, p)
			}
		}
		return out
	}
	set := map[int]bool{}
	for _, p := range mems {
		set[p] = true
	}
	for _, p := range routers {
		set[p] = true
	}
	out := []int{}
	for p := 1; p <= P; p++ {
		if set[p] && p != in {
			out = append(out, p)
		}
	}
	return out
}
