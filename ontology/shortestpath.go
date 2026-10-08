package ontology

import "container/heap"

// pathLess 比较两条候选路径：先比总代价，代价相同比对象标识序列字典序。
// 由于链接代价严格为正，最小代价路径均为简单路径，不存在互为前缀的平局，
// 因此逐节点最优子结构成立，Dijkstra 的每节点最优 (cost, path) 键有效。
func pathLess(costA int64, pathA []ObjectID, costB int64, pathB []ObjectID) bool {
	if costA != costB {
		return costA < costB
	}
	for i := 0; i < len(pathA) && i < len(pathB); i++ {
		if pathA[i] != pathB[i] {
			return pathA[i] < pathB[i]
		}
	}
	return len(pathA) < len(pathB)
}

type queueItem struct {
	node ObjectID
	cost int64
	path []ObjectID
}

type priorityQueue []queueItem

func (q priorityQueue) Len() int { return len(q) }
func (q priorityQueue) Less(i, j int) bool {
	return pathLess(q[i].cost, q[i].path, q[j].cost, q[j].path)
}
func (q priorityQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *priorityQueue) Push(x any)   { *q = append(*q, x.(queueItem)) }
func (q *priorityQueue) Pop() (x any) {
	old := *q
	*q = old[:len(old)-1]
	return old[len(old)-1]
}

// visible 判断 caller 是否能看到（并遍历）该链接及其两端对象。
func (s *state) visible(l *link, caller UserID) bool {
	if !s.canRead(caller, Resource{Kind: ResourceLinkType, ID: string(l.key.typ)}) {
		return false
	}
	for _, id := range [2]ObjectID{l.key.from, l.key.to} {
		o := s.objects[id]
		if o == nil || !o.active {
			return false
		}
		if !s.canRead(caller, Resource{Kind: ResourceObjectType, ID: string(o.typ)}) {
			return false
		}
	}
	return true
}

// shortestPath 在当前状态上为 caller 计算从 start 到 end 的最短路径。
// 平局时选择经过对象标识序列字典序最小的路径。
// metrics 只统计本次查询实际访问的对象与链接数。
func (s *state) shortestPath(start, end ObjectID, caller UserID, metrics *Metrics) Path {
	notFound := Path{Found: false}
	startObj, ok := s.objects[start]
	if !ok || !startObj.active ||
		!s.canRead(caller, Resource{Kind: ResourceObjectType, ID: string(startObj.typ)}) {
		return notFound
	}
	endObj, ok := s.objects[end]
	if !ok || !endObj.active ||
		!s.canRead(caller, Resource{Kind: ResourceObjectType, ID: string(endObj.typ)}) {
		return notFound
	}
	if start == end {
		return Path{Objects: []ObjectID{start}, Cost: 0, Found: true}
	}

	type bestKey struct {
		cost int64
		path []ObjectID
	}
	best := map[ObjectID]bestKey{start: {cost: 0, path: []ObjectID{start}}}
	pq := &priorityQueue{{node: start, cost: 0, path: []ObjectID{start}}}
	heap.Init(pq)

	for pq.Len() > 0 {
		item := heap.Pop(pq).(queueItem)
		b := best[item.node]
		if item.cost != b.cost || !equalPath(item.path, b.path) {
			continue // 过期条目
		}
		metrics.VisitedObjects++
		if item.node == end {
			return Path{Objects: item.path, Cost: item.cost, Found: true}
		}
		u := s.objects[item.node]
		for _, l := range u.outgoing {
			metrics.VisitedLinks++
			if !s.visible(l, caller) {
				continue
			}
			next := l.key.to
			if next == item.node {
				next = l.key.from // 无向链接的反向遍历
			}
			if next == item.node {
				continue // 自环无意义
			}
			candCost := item.cost + l.cost
			candPath := make([]ObjectID, len(item.path)+1)
			copy(candPath, item.path)
			candPath[len(item.path)] = next
			if nb, seen := best[next]; !seen ||
				pathLess(candCost, candPath, nb.cost, nb.path) {
				best[next] = bestKey{cost: candCost, path: candPath}
				heap.Push(pq, queueItem{node: next, cost: candCost, path: candPath})
			}
		}
	}
	return notFound
}

func equalPath(a, b []ObjectID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
