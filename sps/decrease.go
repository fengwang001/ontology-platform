package sps

import "container/heap"

// heapItem 是 Dijkstra 堆中的候选。
type heapItem struct {
	node int
	dist int64
	from int // 提供该候选的边编号
}

type minHeap []heapItem

func (h minHeap) Len() int { return len(h) }
func (h minHeap) Less(i, j int) bool {
	if h[i].dist != h[j].dist {
		return h[i].dist < h[j].dist
	}
	return h[i].from < h[j].from
}
func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)   { *h = append(*h, x.(heapItem)) }
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

type touchInfo struct {
	oldDist int64
	oldPar  int
}

// applyDecrease 处理“变短/新增”类更新：边 e 已经以最终状态写入图中。
// 若 d(u)+w < d(v)（含 v 原不可达），v 的距离必然下降并沿出边做 Dijkstra
// 传播；若恰好相等，则距离不变，只可能把 v 的父边切到编号更小的 e。
// 调用方持写锁。
func (svc *Service) applyDecrease(e *edge, meter *examMeter) (dChanged, pChanged []int) {
	du := svc.dist[e.u]
	dv := svc.dist[e.v]

	// 指向源点的边永不为紧边：正权下 d(u)+w > 0 = d(s)。
	if e.v == svc.source || du == inf || du+e.w > dv {
		return nil, nil
	}

	if du+e.w == dv {
		// 边恰好变紧：距离不变，仅当编号更小时父边切换。
		if e.v != svc.source && (svc.par[e.v] == 0 || e.id < svc.par[e.v]) {
			svc.par[e.v] = e.id
			return nil, []int{e.v}
		}
		return nil, nil
	}

	// du+w < dv：v 的距离严格下降（或由不可达变可达）。
	touched := map[int]touchInfo{
		e.v: {oldDist: dv, oldPar: svc.par[e.v]},
	}

	h := &minHeap{{node: e.v, dist: du + e.w, from: e.id}}
	heap.Init(h)

	for h.Len() > 0 {
		it := heap.Pop(h).(heapItem)
		x := it.node
		if it.dist >= svc.dist[x] {
			// 过期候选；同距离的更小父边已由堆序（dist 相同时 from 小者先出）
			// 在成功出堆时处理。
			continue
		}

		svc.dist[x] = it.dist
		svc.par[x] = it.from

		for _, id := range svc.out[x] {
			meter.edges(1)
			ye := svc.edges[id]
			y := ye.v
			if y == svc.source {
				continue // 指向源点的边永不为紧边、永不参与松弛
			}
			nd := it.dist + ye.w
			if nd < svc.dist[y] {
				if _, seen := touched[y]; !seen {
					touched[y] = touchInfo{oldDist: svc.dist[y], oldPar: svc.par[y]}
				}
				heap.Push(h, heapItem{node: y, dist: nd, from: ye.id})
			} else if nd == svc.dist[y] && y != svc.source &&
				(svc.par[y] == 0 || ye.id < svc.par[y]) {
				if _, seen := touched[y]; !seen {
					touched[y] = touchInfo{oldDist: svc.dist[y], oldPar: svc.par[y]}
				}
				svc.par[y] = ye.id
			}
		}
	}

	for x, info := range touched {
		switch {
		case svc.dist[x] != info.oldDist:
			dChanged = append(dChanged, x)
		case svc.par[x] != info.oldPar:
			pChanged = append(pChanged, x)
		}
	}
	sortInts(dChanged)
	sortInts(pChanged)
	return dChanged, pChanged
}
