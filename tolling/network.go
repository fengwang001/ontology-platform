package tolling

import (
	"container/heap"
	"slices"
)

type Network struct {
	gates map[string]struct{}
	edges map[string]map[string]Edge
}

func NewNetwork() *Network {
	return &Network{gates: map[string]struct{}{}, edges: map[string]map[string]Edge{}}
}

func (n *Network) AddGate(id string) error {
	if id == "" {
		return serviceError(InvalidArgument, "gate id is empty")
	}
	if n == nil {
		return serviceError(InvalidArgument, "network is nil")
	}
	n.gates[id] = struct{}{}
	if n.edges[id] == nil {
		n.edges[id] = map[string]Edge{}
	}
	return nil
}

func (n *Network) AddEdge(edge Edge) error {
	if n == nil || edge.From == "" || edge.To == "" || edge.Distance < 0 || len(edge.Rates) == 0 {
		return serviceError(InvalidArgument, "invalid edge")
	}
	if !n.HasGate(edge.From) || !n.HasGate(edge.To) {
		return serviceError(GateNotFound, "edge endpoint is not registered")
	}
	n.edges[edge.From][edge.To] = edge
	return nil
}

func (n *Network) HasGate(id string) bool {
	_, ok := n.gates[id]
	return ok
}

func (n *Network) edge(from, to, class string) (Edge, bool) {
	byDestination, ok := n.edges[from]
	if !ok {
		return Edge{}, false
	}
	edge, ok := byDestination[to]
	if !ok {
		return Edge{}, false
	}
	_, ok = edge.Rates[class]
	return edge, ok
}

type anchor struct {
	gate  string
	class string
}

type pathState struct {
	cost Money
	path []string
}

type queueItem struct {
	gate string
	cost Money
	path []string
}

type pathQueue []queueItem

func (q pathQueue) Len() int { return len(q) }
func (q pathQueue) Less(i, j int) bool {
	if q[i].cost != q[j].cost {
		return q[i].cost < q[j].cost
	}
	return slices.Compare(q[i].path, q[j].path) < 0
}
func (q pathQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *pathQueue) Push(x any)   { *q = append(*q, x.(queueItem)) }
func (q *pathQueue) Pop() any {
	old := *q
	item := old[len(old)-1]
	*q = old[:len(old)-1]
	return item
}

func (n *Network) Reconstruct(anchors []anchor) ([]string, Money, error) {
	if len(anchors) == 0 {
		return nil, 0, serviceError(InvalidArgument, "empty anchors")
	}
	if len(anchors) == 2 && anchors[0].gate == anchors[1].gate {
		return []string{anchors[0].gate}, 0, nil
	}
	full := []string{anchors[0].gate}
	var total Money
	for i := 0; i+1 < len(anchors); i++ {
		part, cost, err := n.shortest(anchors[i], anchors[i+1], anchors[i+1:])
		if err != nil {
			return nil, 0, err
		}
		full = append(full, part[1:]...)
		total += cost
	}
	return full, total, nil
}

func (n *Network) shortest(start anchor, destination anchor, future []anchor) ([]string, Money, error) {
	blocked := map[string]struct{}{}
	for _, item := range future[1:] {
		blocked[item.gate] = struct{}{}
	}
	best := map[string]pathState{start.gate: {path: []string{start.gate}}}
	queue := &pathQueue{{gate: start.gate, path: []string{start.gate}}}
	heap.Init(queue)
	for queue.Len() > 0 {
		current := heap.Pop(queue).(queueItem)
		initial := current.gate == start.gate && len(current.path) == 1
		if known, ok := best[current.gate]; ok && (!initial || current.gate != destination.gate) && (current.cost > known.cost || current.cost == known.cost && slices.Compare(current.path, known.path) > 0) {
			continue
		}
		if current.gate == destination.gate && !initial {
			return slices.Clone(current.path), current.cost, nil
		}
		if !initial {
			if _, isFutureAnchor := blocked[current.gate]; isFutureAnchor {
				continue
			}
		}
		for nextID := range n.edges[current.gate] {
			if nextID != destination.gate {
				if _, isFutureAnchor := blocked[nextID]; isFutureAnchor {
					continue
				}
			}
			edge, ok := n.edge(current.gate, nextID, start.class)
			if !ok {
				continue
			}
			nextCost := current.cost + edge.Rates[start.class]
			nextPath := append(slices.Clone(current.path), nextID)
			if known, ok := best[nextID]; ok && (nextCost > known.cost || nextCost == known.cost && slices.Compare(nextPath, known.path) >= 0) {
				continue
			}
			best[nextID] = pathState{cost: nextCost, path: slices.Clone(nextPath)}
			heap.Push(queue, queueItem{gate: nextID, cost: nextCost, path: nextPath})
		}
	}
	return nil, 0, serviceError(PathUnreachable, "no ordered path through observed gates")
}
