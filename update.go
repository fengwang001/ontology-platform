package ontology

import "container/heap"

type heapItem struct {
	node     int
	distance int64
}

type distanceHeap []heapItem

func (h distanceHeap) Len() int           { return len(h) }
func (h distanceHeap) Less(i, j int) bool { return h[i].distance < h[j].distance }
func (h distanceHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *distanceHeap) Push(x any)        { *h = append(*h, x.(heapItem)) }
func (h *distanceHeap) Pop() any {
	old := *h
	item := old[len(old)-1]
	*h = old[:len(old)-1]
	return item
}

func (s *Service) AddEdge(u, v, weight int) (UpdateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.validateNode(u) || !s.validateNode(v) || u == v || !s.validateWeight(weight) {
		return UpdateResult{}, errorWith(InvalidArgument, "edge endpoint or weight is invalid")
	}
	if s.live == s.maxEdges {
		return UpdateResult{}, errorWith(CapacityFull, "live edge capacity is full")
	}

	id := s.nextID
	e := &edge{id: id, from: u, to: v, weight: weight, alive: true}
	s.edges[id] = e
	s.out[u] = append(s.out[u], id)
	s.in[v] = append(s.in[v], id)
	s.nextID++
	s.live++

	mode := noUpdate
	candidate := s.dist[u] + int64(weight)
	if s.dist[u] != unreachable && (s.dist[v] == unreachable || candidate < s.dist[v] ||
		(candidate == s.dist[v] && (s.parent[v] == noParent || id < s.parent[v]))) {
		mode = decreaseUpdate
	}
	return s.commit(id, 0, mode), nil
}

func (s *Service) SetWeight(id, weight int) (UpdateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.validateWeight(weight) {
		return UpdateResult{}, errorWith(InvalidArgument, "weight is out of range")
	}
	e, ok := s.edges[id]
	if !ok || !e.alive {
		return UpdateResult{}, errorWith(EdgeNotFound, "edge does not exist")
	}

	oldWeight := e.weight
	mode := noUpdate
	wasTight := s.isTight(e)
	if weight < oldWeight {
		mode = decreaseUpdate
	} else if weight > oldWeight && wasTight {
		mode = increaseUpdate
	}
	e.weight = weight
	commitMode := mode
	if mode == decreaseUpdate && (s.dist[e.from] == unreachable ||
		(s.dist[e.to] != unreachable &&
			(s.dist[e.from]+int64(weight) > s.dist[e.to] ||
				(s.dist[e.from]+int64(weight) == s.dist[e.to] && s.parent[e.to] != noParent && id > s.parent[e.to])))) {
		commitMode = noUpdate
	}
	return s.commit(id, oldWeight, commitMode), nil
}

func (s *Service) RemoveEdge(id int) (UpdateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.edges[id]
	if !ok || !e.alive {
		return UpdateResult{}, errorWith(EdgeNotFound, "edge does not exist")
	}

	wasTight := s.isTight(e)
	oldWeight := e.weight
	e.alive = false
	s.live--
	mode := noUpdate
	if wasTight && s.parent[e.to] == id {
		mode = increaseUpdate
	}
	return s.commit(id, oldWeight, mode), nil
}

type updateMode int

const (
	noUpdate updateMode = iota
	decreaseUpdate
	increaseUpdate
)

func (s *Service) isTight(e *edge) bool {
	return s.dist[e.from] != unreachable && s.dist[e.to] != unreachable &&
		s.dist[e.from]+int64(e.weight) == s.dist[e.to]
}

func (s *Service) commit(modifiedID int, oldWeight int, mode updateMode) UpdateResult {
	s.examined = 0
	oldDist := append([]int64(nil), s.dist...)
	oldParent := append([]int(nil), s.parent...)
	switch mode {
	case decreaseUpdate:
		s.applyDecrease(modifiedID, oldDist)
	case increaseUpdate:
		s.applyIncrease(modifiedID, oldWeight, oldDist, oldParent, s.edges[modifiedID].alive)
	}
	s.version++

	dChanged := make([]int, 0)
	pChanged := make([]int, 0)
	for v := 0; v < s.n; v++ {
		if oldDist[v] != s.dist[v] {
			dChanged = append(dChanged, v)
			continue
		}
		if oldDist[v] != unreachable && oldParent[v] != s.parent[v] {
			pChanged = append(pChanged, v)
		}
	}

	s.appendSnapshot()
	return UpdateResult{
		Version:  s.version,
		EdgeID:   modifiedID,
		DChanged: dChanged,
		PChanged: pChanged,
	}
}

func (s *Service) recomputeAll() {
	s.examined = 0
	s.dist = initialDist(s.n, s.source)
	s.parent = initialParent(s.n)

	pq := &distanceHeap{{node: s.source, distance: 0}}
	heap.Init(pq)
	for pq.Len() > 0 {
		item := heap.Pop(pq).(heapItem)
		u := item.node
		if item.distance != s.dist[u] {
			continue
		}
		for _, id := range s.out[u] {
			s.examined++
			e := s.edges[id]
			if !e.alive {
				continue
			}
			candidate := item.distance + int64(e.weight)
			if s.dist[e.to] == unreachable || candidate < s.dist[e.to] {
				s.dist[e.to] = candidate
				heap.Push(pq, heapItem{node: e.to, distance: candidate})
			}
		}
	}

	for v := range s.parent {
		s.examined++
		for _, id := range s.in[v] {
			s.examined++
			e := s.edges[id]
			if !e.alive || s.dist[e.from] == unreachable || s.dist[v] == unreachable {
				continue
			}
			if s.dist[e.from]+int64(e.weight) == s.dist[v] && (s.parent[v] == noParent || id < s.parent[v]) {
				s.parent[v] = id
			}
		}
	}
}
