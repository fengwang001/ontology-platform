package ontology

import "container/heap"

type dstHeap []int

func (h dstHeap) Len() int           { return len(h) }
func (h dstHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h dstHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *dstHeap) Push(value any)    { *h = append(*h, value.(int)) }
func (h *dstHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

func scheduleGraph(copies []graphCopy) ([]int, []graphCopy, overlapGraph) {
	graph := buildOverlapGraph(copies)
	active := make([]bool, len(copies))
	for i := range copies {
		active[i] = true
	}

	indegree := append([]int(nil), graph.indegree...)
	ready := &dstHeap{}
	for i, degree := range indegree {
		if degree == 0 {
			*ready = append(*ready, i)
		}
	}
	heap.Init(ready)

	var order []int
	var stashed []graphCopy
	for len(order)+len(stashed) < len(copies) {
		if ready.Len() > 0 {
			from := heap.Pop(ready).(int)
			if !active[from] || indegree[from] != 0 {
				continue
			}
			active[from] = false
			order = append(order, from)
			graph.forEachOut(from, func(to int) {
				if active[to] {
					indegree[to]--
					if indegree[to] == 0 {
						heap.Push(ready, to)
					}
				}
			})
			continue
		}

		target := chooseStash(graph, active)
		active[target] = false
		stashed = append(stashed, graph.copies[target])
		graph.forEachOut(target, func(to int) {
			if active[to] {
				indegree[to]--
				if indegree[to] == 0 {
					heap.Push(ready, to)
				}
			}
		})
		graph.forEachIn(target, func(from int) {
			if active[from] {
				indegree[target]--
			}
		})
	}

	return order, stashed, graph
}

func chooseStash(graph overlapGraph, active []bool) int {
	finish := kosarajuFinish(graph, active)
	assigned := make([]int, len(graph.copies))
	for i := range assigned {
		assigned[i] = -1
	}

	componentID := 0
	componentSize := []int{}
	for i := len(finish) - 1; i >= 0; i-- {
		start := finish[i]
		if !active[start] || assigned[start] != -1 {
			continue
		}
		stack := []int{start}
		assigned[start] = componentID
		size := 0
		for len(stack) > 0 {
			from := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			size++
			graph.forEachIn(from, func(to int) {
				if active[to] && assigned[to] == -1 {
					assigned[to] = componentID
					stack = append(stack, to)
				}
			})
		}
		componentSize = append(componentSize, size)
		componentID++
	}

	target := -1
	for id, component := range assigned {
		if !active[id] || componentSize[component] < 2 {
			continue
		}
		if target == -1 ||
			graph.copies[id].len < graph.copies[target].len ||
			(graph.copies[id].len == graph.copies[target].len && graph.copies[id].dst < graph.copies[target].dst) {
			target = id
		}
	}
	return target
}

func kosarajuFinish(graph overlapGraph, active []bool) []int {
	state := make([]uint8, len(graph.copies))
	type frame struct {
		node int
		next int
	}
	var finish []int

	for start := range graph.copies {
		if !active[start] || state[start] != 0 {
			continue
		}
		stack := []frame{{node: start, next: graph.outStart[start]}}
		state[start] = 1
		for len(stack) > 0 {
			frameIndex := len(stack) - 1
			current := &stack[frameIndex]
			for current.next < graph.outEnd[current.node] &&
				(current.next == current.node || !active[current.next]) {
				current.next++
			}
			if current.next == graph.outEnd[current.node] {
				state[current.node] = 2
				finish = append(finish, current.node)
				stack = stack[:frameIndex]
				continue
			}
			next := current.next
			current.next++
			if state[next] == 0 {
				state[next] = 1
				stack = append(stack, frame{node: next, next: graph.outStart[next]})
			}
		}
	}
	return finish
}
