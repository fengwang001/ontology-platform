package pvbinding

import "math"

type flowEdge struct {
	to       int
	rev      int
	capacity int64
	cost     int64
}

type flowNetwork struct {
	edges      [][]flowEdge
	source     int
	claimBase  int
	volumeBase int
	sink       int
}

func newFlowNetwork(claimCount, volumeCount int) *flowNetwork {
	claimBase := 1
	volumeBase := claimBase + claimCount
	sink := volumeBase + volumeCount
	network := &flowNetwork{
		edges:      make([][]flowEdge, sink+1),
		source:     0,
		claimBase:  claimBase,
		volumeBase: volumeBase,
		sink:       sink,
	}
	for claimIndex := 0; claimIndex < claimCount; claimIndex++ {
		network.addEdge(network.source, claimBase+claimIndex, 1, 0)
	}
	for volumeIndex := 0; volumeIndex < volumeCount; volumeIndex++ {
		network.addEdge(volumeBase+volumeIndex, network.sink, 1, 0)
	}
	return network
}

func (n *flowNetwork) addEdge(from, to int, capacity, cost int64) {
	n.edges[from] = append(n.edges[from], flowEdge{to: to, rev: len(n.edges[to]), capacity: capacity, cost: cost})
	n.edges[to] = append(n.edges[to], flowEdge{to: from, rev: len(n.edges[from]) - 1, capacity: 0, cost: -cost})
}

func (n *flowNetwork) minCostMaxFlow(source, sink int, requiredFlow int64) (int64, int64) {
	var flow, cost int64
	for flow < requiredFlow {
		dist := make([]int64, len(n.edges))
		parentNode := make([]int, len(n.edges))
		parentEdge := make([]int, len(n.edges))
		inQueue := make([]bool, len(n.edges))
		for i := range dist {
			dist[i] = math.MaxInt64
		}
		dist[source] = 0
		queue := []int{source}
		inQueue[source] = true
		for len(queue) > 0 {
			from := queue[0]
			queue = queue[1:]
			inQueue[from] = false
			for edgeIndex, edge := range n.edges[from] {
				if edge.capacity == 0 || dist[from] == math.MaxInt64 || dist[from]+edge.cost >= dist[edge.to] {
					continue
				}
				dist[edge.to] = dist[from] + edge.cost
				parentNode[edge.to] = from
				parentEdge[edge.to] = edgeIndex
				if !inQueue[edge.to] {
					queue = append(queue, edge.to)
					inQueue[edge.to] = true
				}
			}
		}
		if dist[sink] == math.MaxInt64 {
			return flow, cost
		}
		for node := sink; node != source; node = parentNode[node] {
			from := parentNode[node]
			edgeIndex := parentEdge[node]
			n.edges[from][edgeIndex].capacity--
			n.edges[node][n.edges[from][edgeIndex].rev].capacity++
		}
		flow++
		cost += dist[sink]
	}
	return flow, cost
}

func (n *flowNetwork) matching() map[int]int {
	result := make(map[int]int)
	for claimIndex := n.claimBase; claimIndex < n.volumeBase; claimIndex++ {
		for _, edge := range n.edges[claimIndex] {
			if edge.to >= n.volumeBase && edge.to < n.sink && edge.capacity == 0 {
				result[claimIndex-n.claimBase] = edge.to - n.volumeBase
			}
		}
	}
	return result
}
