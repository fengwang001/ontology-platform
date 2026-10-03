package ontology

import (
	"container/heap"
	"sort"
)

type copyNode struct {
	src int64
	dst int64
	len int64
}

type stashedCopy struct {
	node int
	slot int
}

type copySchedule struct {
	copies           []int
	stashes          []stashedCopy
	stashBytes       int64
	edges            int
	intervalCompares int64
}

type sweepEvent struct {
	at      int64
	sources []int
	dests   []int
}

type endItem struct {
	node int
	end  int64
}

type endHeap struct {
	items    []endItem
	compares *int64
}

func (h endHeap) Len() int { return len(h.items) }

func (h endHeap) Less(i, j int) bool {
	*h.compares++
	if h.items[i].end != h.items[j].end {
		return h.items[i].end < h.items[j].end
	}
	return h.items[i].node < h.items[j].node
}

func (h endHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *endHeap) Push(value any) { h.items = append(h.items, value.(endItem)) }

func (h *endHeap) Pop() any {
	last := len(h.items) - 1
	item := h.items[last]
	h.items = h.items[:last]
	return item
}

type dstHeap []int

func (h dstHeap) Len() int { return len(h) }

func (h dstHeap) Less(i, j int) bool { return h[i] < h[j] }

func (h dstHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *dstHeap) Push(value any) { *h = append(*h, value.(int)) }
func (h *dstHeap) Pop() any {
	last := len(*h) - 1
	value := (*h)[last]
	*h = (*h)[:last]
	return value
}

func scheduleCopies(nodes []copyNode) copySchedule {
	k := len(nodes)
	schedule := copySchedule{
		copies: make([]int, 0, k),
	}
	if k == 0 {
		return schedule
	}

	adj := buildIntervals(nodes, &schedule.intervalCompares)
	for _, neighbors := range adj {
		schedule.edges += len(neighbors)
	}
	graphSchedule := scheduleGraph(nodes, adj)
	schedule.copies = graphSchedule.copies
	schedule.stashes = graphSchedule.stashes
	schedule.stashBytes = graphSchedule.stashBytes
	return schedule
}

func scheduleGraph(nodes []copyNode, adj [][]int) copySchedule {
	k := len(nodes)
	schedule := copySchedule{
		copies: make([]int, 0, k),
	}
	if k == 0 {
		return schedule
	}

	indegree := make([]int, k)
	for _, neighbors := range adj {
		for _, to := range neighbors {
			indegree[to]++
		}
	}

	active := make([]bool, k)
	ready := &dstHeap{}
	for node := 0; node < k; node++ {
		active[node] = true
		if indegree[node] == 0 {
			heap.Push(ready, node)
		}
	}

	components := make([][]int, 0)
	dirty := true
	remaining := k
	stashSet := make([]int, 0)

	runReady := func() {
		for ready.Len() > 0 {
			node := heap.Pop(ready).(int)
			if !active[node] || indegree[node] != 0 {
				continue
			}
			active[node] = false
			remaining--
			schedule.copies = append(schedule.copies, node)
			for _, next := range adj[node] {
				if active[next] {
					indegree[next]--
					if indegree[next] == 0 {
						heap.Push(ready, next)
					}
				}
			}
		}
	}

	runReady()
	for remaining > 0 {
		if dirty {
			components = refreshComponents(components, nodes, adj, active)
			dirty = false
		}
		chosen := -1
		for _, component := range components {
			if len(component) < 2 {
				continue
			}
			for _, node := range component {
				if !active[node] {
					continue
				}
				if chosen < 0 || lessCopy(nodes[node], nodes[chosen]) {
					chosen = node
				}
			}
		}
		if chosen < 0 {
			components = cyclicComponents(nodes, adj, active)
			for _, component := range components {
				for _, node := range component {
					if chosen < 0 || lessCopy(nodes[node], nodes[chosen]) {
						chosen = node
					}
				}
			}
		}

		active[chosen] = false
		remaining--
		stashSet = append(stashSet, chosen)
		schedule.stashBytes += nodes[chosen].len
		for componentIndex, component := range components {
			for _, node := range component {
				if node == chosen {
					components[componentIndex] = nil
					break
				}
			}
		}
		dirty = true
		for _, next := range adj[chosen] {
			if active[next] {
				indegree[next]--
				if indegree[next] == 0 {
					heap.Push(ready, next)
				}
			}
		}
		runReady()
	}

	sort.Slice(stashSet, func(i, j int) bool { return nodes[stashSet[i]].dst < nodes[stashSet[j]].dst })
	schedule.stashes = make([]stashedCopy, len(stashSet))
	for slot, node := range stashSet {
		schedule.stashes[slot] = stashedCopy{node: node, slot: slot}
	}
	return schedule
}

func buildIntervals(nodes []copyNode, compares *int64) [][]int {
	k := len(nodes)
	events := make([]sweepEvent, 0, k)
	eventByAt := make(map[int64]int)
	ensureEvent := func(at int64) int {
		if index, ok := eventByAt[at]; ok {
			return index
		}
		index := len(events)
		eventByAt[at] = index
		events = append(events, sweepEvent{at: at})
		return index
	}
	for node := 0; node < k; node++ {
		sourceIndex := ensureEvent(nodes[node].src)
		events[sourceIndex].sources = append(events[sourceIndex].sources, node)
		destIndex := ensureEvent(nodes[node].dst)
		events[destIndex].dests = append(events[destIndex].dests, node)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].at < events[j].at })

	adj := make([][]int, k)
	activeSources := make(map[int]int64)
	activeDests := make(map[int]int64)
	sourceEnds := &endHeap{compares: compares}
	destEnds := &endHeap{compares: compares}

	removeEnded := func(at int64) {
		for sourceEnds.Len() > 0 && sourceEnds.items[0].end <= at {
			item := heap.Pop(sourceEnds).(endItem)
			if activeSources[item.node] == item.end {
				delete(activeSources, item.node)
			}
		}
		for destEnds.Len() > 0 && destEnds.items[0].end <= at {
			item := heap.Pop(destEnds).(endItem)
			if activeDests[item.node] == item.end {
				delete(activeDests, item.node)
			}
		}
	}

	for _, event := range events {
		removeEnded(event.at)
		for _, dest := range event.dests {
			for source := range activeSources {
				if source != dest {
					adj[source] = append(adj[source], dest)
				}
			}
		}
		for _, source := range event.sources {
			activeSources[source] = nodes[source].src + nodes[source].len
			heap.Push(sourceEnds, endItem{node: source, end: nodes[source].src + nodes[source].len})
		}
		for _, dest := range event.dests {
			activeDests[dest] = nodes[dest].dst + nodes[dest].len
			heap.Push(destEnds, endItem{node: dest, end: nodes[dest].dst + nodes[dest].len})
		}
		for _, source := range event.sources {
			for dest := range activeDests {
				if source != dest {
					adj[source] = append(adj[source], dest)
				}
			}
		}
	}

	for node := range adj {
		sort.Ints(adj[node])
	}
	return adj
}

func lessCopy(a, b copyNode) bool {
	if a.len != b.len {
		return a.len < b.len
	}
	return a.dst < b.dst
}

func refreshComponents(previous [][]int, nodes []copyNode, adj [][]int, active []bool) [][]int {
	if len(previous) == 0 {
		return cyclicComponents(nodes, adj, active)
	}
	activePrevious := false
	for _, component := range previous {
		for _, node := range component {
			if active[node] {
				activePrevious = true
			}
		}
	}
	if !activePrevious {
		return cyclicComponents(nodes, adj, active)
	}

	stable := make([][]int, 0, len(previous))
	dirtyNodes := make([]int, 0)
	inStable := make([]bool, len(nodes))
	for _, component := range previous {
		hasActive := false
		hasChosen := false
		for _, node := range component {
			if active[node] {
				hasActive = true
			} else {
				hasChosen = true
			}
		}
		if !hasActive {
			continue
		}
		if !hasChosen && len(component) >= 2 {
			copied := append([]int(nil), component...)
			stable = append(stable, copied)
			for _, node := range component {
				inStable[node] = true
			}
		} else {
			for _, node := range component {
				if active[node] {
					dirtyNodes = append(dirtyNodes, node)
				}
			}
		}
	}

	for node := range nodes {
		if active[node] && !inStable[node] {
			dirtyNodes = append(dirtyNodes, node)
		}
	}
	return append(stable, cyclicComponentsOn(dirtyNodes, adj, active)...)
}

func cyclicComponents(nodes []copyNode, adj [][]int, active []bool) [][]int {
	subset := make([]int, 0, len(nodes))
	for node := range nodes {
		if active[node] {
			subset = append(subset, node)
		}
	}
	return cyclicComponentsOn(subset, adj, active)
}

func cyclicComponentsOn(subset []int, adj [][]int, active []bool) [][]int {
	if len(subset) == 0 {
		return nil
	}
	inSubset := make([]bool, len(adj))
	for _, node := range subset {
		inSubset[node] = true
	}

	visit := make([]bool, len(adj))
	order := make([]int, 0, len(subset))
	var visitForward func(int)
	visitForward = func(node int) {
		visit[node] = true
		for _, next := range adj[node] {
			if inSubset[next] && active[next] && !visit[next] {
				visitForward(next)
			}
		}
		order = append(order, node)
	}
	for _, node := range subset {
		if !visit[node] {
			visitForward(node)
		}
	}

	pred := make([][]int, len(adj))
	for from := range adj {
		if !inSubset[from] || !active[from] {
			continue
		}
		for _, to := range adj[from] {
			if inSubset[to] && active[to] {
				pred[to] = append(pred[to], from)
			}
		}
	}

	componentID := make([]int, len(adj))
	for i := range componentID {
		componentID[i] = -1
	}
	var visitBackward func(int, int)
	visitBackward = func(node, id int) {
		componentID[node] = id
		for _, prev := range pred[node] {
			if componentID[prev] == -1 {
				visitBackward(prev, id)
			}
		}
	}
	nextID := 0
	for i := len(order) - 1; i >= 0; i-- {
		node := order[i]
		if componentID[node] == -1 {
			visitBackward(node, nextID)
			nextID++
		}
	}

	components := make([][]int, nextID)
	for _, node := range subset {
		id := componentID[node]
		components[id] = append(components[id], node)
	}
	cyclic := components[:0]
	for _, component := range components {
		if len(component) >= 2 {
			sort.Ints(component)
			cyclic = append(cyclic, component)
		}
	}
	sort.Slice(cyclic, func(i, j int) bool { return cyclic[i][0] < cyclic[j][0] })
	return cyclic
}
