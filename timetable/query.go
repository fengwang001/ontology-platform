package timetable

// inf 用于“无穷远”的分段边界；实际时刻至多 1e9、耗时至多 1e6，
// 留出充足余量避免加法溢出。
const inf = int64(1) << 60

// cseg 是某版本下一条边可见通告记录合并裁剪后的有效分段。
// 区间 [start,end) 内的绝对出发时刻 t 使用 cost（-1 为封闭）。
// 起点 start 属于本分段；end 等于 inf 表示最后一段延伸至无穷。
type cseg struct {
	start int64
	end   int64
	cost  int64
}

// cedge 是某版本下可见的一条边及其合并后的分段。
type cedge struct {
	id   int
	u, v int
	segs []cseg
}

// snapshot 编译给定版本下可见的边与记录。可见规则：
// 边 addedAt<=ver；记录 addedAt<=ver 且（未替换或 replacedAt>ver）。
func (nw *Network) snapshot(ver int) []*cedge {
	es := make([]*cedge, 0, len(nw.edges))
	for _, e := range nw.edges {
		if e.addedAt > ver {
			continue
		}
		vis := make([]*record, 0, len(e.records))
		for _, r := range e.records {
			if r.addedAt <= ver && (r.replacedAt == 0 || r.replacedAt > ver) {
				vis = append(vis, r)
			}
		}
		if len(vis) == 0 {
			continue
		}
		ce := &cedge{id: e.id, u: e.u, v: e.v}
		segs := make([]cseg, 0, len(vis)*maxSegs)
		for i, r := range vis {
			// 记录的最后边界为下一记录的 eff（剖面在此被截断），
			// 最后一条记录延伸至无穷。
			recEnd := inf
			if i+1 < len(vis) {
				recEnd = vis[i+1].eff
			}
			for j, s := range r.segments {
				start := r.eff + s.Offset
				if start >= recEnd {
					break
				}
				end := recEnd
				if j+1 < len(r.segments) {
					if nx := r.eff + r.segments[j+1].Offset; nx < end {
						end = nx
					}
				}
				// 同边界可能因记录恰好接在前一记录最后分段起点而重合；
				// start 严格递增时才新增，否则后者覆盖前者该单点。
				if len(segs) > 0 && segs[len(segs)-1].start == start {
					segs[len(segs)-1] = cseg{start: start, end: end, cost: s.Cost}
					continue
				}
				segs = append(segs, cseg{start: start, end: end, cost: s.Cost})
			}
		}
		ce.segs = segs
		es = append(es, ce)
	}
	return es
}

// locate 返回覆盖时刻 t 的分段下标；t 早于首段起点时返回 -1。
func (ce *cedge) locate(t int64) int {
	lo, hi := 0, len(ce.segs)
	for lo < hi {
		mid := (lo + hi) / 2
		if ce.segs[mid].start <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1
}

// earliestDeparture 返回不小于 t 的最小整数出发时刻 dep，以及从 dep
// 出发的到达时刻 dep+cost(dep)，使得到达时刻等于 arr(t)；不存在开放
// 分段时 ok 为 false。
//
// 由于每个开放分段内 t'+cost(t') 随 t' 严格递增（cost 为正常数），
// 候选出发点只可能是 t 本身（若 t 位于开放分段）或 t 之后各分段的
// 起点；逐一比较即可。
func (ce *cedge) earliestDeparture(t int64) (dep, arr int64, ok bool) {
	i := ce.locate(t)
	if i < 0 {
		i = 0
	}
	bestArr := inf
	bestDep := int64(0)
	found := false
	for j := i; j < len(ce.segs); j++ {
		s := ce.segs[j]
		var cand int64
		if s.start <= t {
			cand = t
		} else {
			cand = s.start
		}
		if cand >= s.end {
			continue
		}
		if s.cost < 0 {
			// 封闭分段：其内部任何时刻都不能出发；若 t 落在其中，
			// 直接跳到下一分段起点继续尝试。
			continue
		}
		a := cand + s.cost
		if !found || a < bestArr {
			found = true
			bestArr = a
			bestDep = cand
		}
		// 后续分段起点只会更大；其分段内候选值就是分段起点，
		// 循环中逐个比较。分段按 start 递增，无需提前剪枝
		// （封闭段后耗时可能骤降，如示例边 2）。
	}
	if !found {
		return 0, 0, false
	}
	return bestDep, bestArr, true
}

// arrival 仅返回最早到达时刻与可用性，供 Dijkstra 松弛使用。
func (ce *cedge) arrival(t int64) (int64, bool) {
	_, a, ok := ce.earliestDeparture(t)
	return a, ok
}
