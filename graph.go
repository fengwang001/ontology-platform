package ontology

import "sort"

type graphCopy struct {
	src int64
	dst int64
	len int64
}

type overlapGraph struct {
	copies      []graphCopy
	indegree    []int
	outStart    []int
	outEnd      []int
	coverTree   [][]int
	leafSize    int
	edges       int64
	comparisons int64
}

func buildOverlapGraph(copies []graphCopy) overlapGraph {
	graph := overlapGraph{
		copies:   copies,
		indegree: make([]int, len(copies)),
		outStart: make([]int, len(copies)),
		outEnd:   make([]int, len(copies)),
	}
	if len(copies) == 0 {
		return graph
	}

	graph.leafSize = 1
	for graph.leafSize < len(copies) {
		graph.leafSize <<= 1
	}
	graph.coverTree = make([][]int, graph.leafSize*2)

	delta := make([]int, len(copies)+1)
	for srcID, copyOp := range copies {
		first := sort.Search(len(copies), func(i int) bool {
			graph.comparisons++
			return copies[i].dst+copies[i].len > copyOp.src
		})
		afterEnd := sort.Search(len(copies), func(i int) bool {
			graph.comparisons++
			return copies[i].dst >= copyOp.src+copyOp.len
		})

		graph.outStart[srcID] = first
		graph.outEnd[srcID] = afterEnd
		graph.addCover(first, afterEnd, srcID)
		delta[first]++
		delta[afterEnd]--
		graph.edges += int64(afterEnd - first)
		if first <= srcID && srcID < afterEnd {
			graph.edges--
			delta[srcID]--
			delta[srcID+1]++
		}
	}

	running := 0
	for i := range graph.indegree {
		running += delta[i]
		graph.indegree[i] = running
	}
	return graph
}

func (graph *overlapGraph) addCover(left, right, id int) {
	left += graph.leafSize
	right += graph.leafSize
	for left < right {
		if left&1 == 1 {
			graph.coverTree[left] = append(graph.coverTree[left], id)
			left++
		}
		if right&1 == 1 {
			right--
			graph.coverTree[right] = append(graph.coverTree[right], id)
		}
		left >>= 1
		right >>= 1
	}
}

func (graph overlapGraph) forEachOut(from int, visit func(int)) {
	for to := graph.outStart[from]; to < graph.outEnd[from]; to++ {
		if to != from {
			visit(to)
		}
	}
}

func (graph overlapGraph) forEachIn(to int, visit func(int)) {
	index := to + graph.leafSize
	for index > 0 {
		for _, from := range graph.coverTree[index] {
			if from != to {
				visit(from)
			}
		}
		index >>= 1
	}
}
