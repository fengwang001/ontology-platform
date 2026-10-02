package sps

import "container/heap"

// applyIncrease 处理“变长/删除”类更新：图中修改已经完成，仅当被改边在
// 修改前是紧边（wasTight）才可能产生影响。调用方持写锁。
//
//  1. 终点 v 仍有别的紧边：距离不变，至多父边切换。
//  2. 否则传播受影响集合 A：失去全部紧边的节点入 A，沿出边继续；仍有来自
//     A 外紧入边的节点为边界节点，不继续传播。
//  3. 对 A 做以 A 外节点为源的多源 Dijkstra：边界节点距离不变（可能只换
//     父边），其余节点距离变大或变不可达。
func (svc *Service) applyIncrease(e *edge, wasTight bool, meter *examMeter) (dChanged, pChanged []int) {
	// 指向源点的边永不为紧边，其增权/删除不产生任何影响。
	if !wasTight || e.v == svc.source {
		return nil, nil
	}

	// 第一步：终点是否仍有别的紧边。
	if newPar, ok := svc.bestParentLocked(e.v, meter); ok {
		if newPar != svc.par[e.v] {
			svc.par[e.v] = newPar
			return nil, []int{e.v}
		}
		return nil, nil
	}

	// 第二步：传播受影响集合。
	affected := map[int]bool{e.v: true}
	// boundary 记录边界节点（不入 A）：距离保持旧值，但来自 A 内的紧边
	// 全部消失后，父边可能要切到 A 外编号最小的紧边。
	boundary := map[int]int{}

	// candidates 是“与 A 内节点通过旧紧边相连、但尚未入 A”的节点，
	// A 扩大时必须重新评估，因为它原有的 A 外紧入边可能也来自新 A 成员。
	candSet := map[int]bool{}
	queue := []int{e.v}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, id := range svc.out[x] {
			meter.edges(1)
			ye := svc.edges[id]
			y := ye.v
			if y == svc.source || affected[y] {
				continue
			}
			candSet[y] = true
		}
	}

	// 反复扫描候选集合直到固定点。
	changed := true
	for changed {
		changed = false
		for y := range candSet {
			if affected[y] {
				continue
			}
			bestOutside := 0
			for _, yid := range svc.in[y] {
				meter.edges(1)
				yi := svc.edges[yid]
				if !affected[yi.u] && svc.tightLocked(yi) &&
					(bestOutside == 0 || yi.id < bestOutside) {
					bestOutside = yi.id
				}
			}
			if bestOutside != 0 {
				boundary[y] = bestOutside
			} else {
				delete(boundary, y)
				affected[y] = true
				changed = true
				// 新 A 成员的出边引入新候选。
				for _, id := range svc.out[y] {
					meter.edges(1)
					ye := svc.edges[id]
					if ye.v != svc.source && !affected[ye.v] {
						candSet[ye.v] = true
					}
				}
			}
		}
	}

	// 边界节点距离不变；先按 A 外编号最小紧边确定新父边，变化计入 PChanged。
	for y, pid := range boundary {
		if svc.par[y] != pid {
			svc.par[y] = pid
			pChanged = append(pChanged, y)
		}
	}

	// 第三步：统一多源 Dijkstra 重算 A。
	oldDist := make(map[int]int64, len(affected))
	oldPar := make(map[int]int, len(affected))
	for x := range affected {
		if x == svc.source {
			delete(affected, x)
			continue
		}
		oldDist[x] = svc.dist[x]
		oldPar[x] = svc.par[x]
		svc.dist[x] = inf
		svc.par[x] = 0
	}

	h := &minHeap{}
	heap.Init(h)

	// 初始候选：每个 A 节点取来自 A 外可达节点的最小 (d(u)+w, id)。
	for x := range affected {
		bestDist := inf
		bestFrom := 0
		for _, id := range svc.in[x] {
			meter.edges(1)
			ie := svc.edges[id]
			if !ie.alive || affected[ie.u] || svc.dist[ie.u] == inf {
				continue
			}
			nd := svc.dist[ie.u] + ie.w
			if nd < bestDist || (nd == bestDist && (bestFrom == 0 || ie.id < bestFrom)) {
				bestDist = nd
				bestFrom = ie.id
			}
		}
		if bestFrom != 0 {
			heap.Push(h, heapItem{node: x, dist: bestDist, from: bestFrom})
		}
	}

	for h.Len() > 0 {
		it := heap.Pop(h).(heapItem)
		x := it.node
		if it.dist >= svc.dist[x] {
			continue
		}
		svc.dist[x] = it.dist
		svc.par[x] = it.from

		for _, id := range svc.out[x] {
			meter.edges(1)
			ye := svc.edges[id]
			if !ye.alive {
				continue
			}
			y := ye.v
			if !affected[y] {
				continue
			}
			if nd := it.dist + ye.w; nd < svc.dist[y] {
				heap.Push(h, heapItem{node: y, dist: nd, from: ye.id})
			}
		}
	}

	// 汇总：距离变化（含变不可达）入 DChanged；距离不变但父边变入 PChanged。
	for x := range affected {
		switch {
		case svc.dist[x] != oldDist[x]:
			dChanged = append(dChanged, x)
		case svc.par[x] != oldPar[x]:
			pChanged = append(pChanged, x)
		}
	}
	sortInts(dChanged)
	sortInts(pChanged)
	return dChanged, pChanged
}
