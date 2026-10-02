package ontology

import "container/heap"

func (s *Service) applyIncrease(modifiedID int, oldWeightValue int, oldDist []int64, oldParent []int, modifiedAlive bool) {
	affected := map[int]struct{}{}
	tightDegree := map[int]int{}
	degreeReady := map[int]bool{}
	tightParent := map[int]int{}
	target := s.edges[modifiedID].to
	frontier := make([]int, 0)

	type seedCandidate struct {
		from     int
		distance int64
		edgeID   int
	}
	targetCandidates := make([]seedCandidate, 0, len(s.in[target]))
	alternativeCount := 0
	updatedAlternative := false
	s.parent[target] = noParent
	s.examined += uint64(len(s.in[target]))
	for _, id := range s.in[target] {
		e := s.edges[id]
		if !e.alive || (id == modifiedID && !modifiedAlive) || oldDist[e.from] == unreachable {
			continue
		}
		candidate := oldDist[e.from] + int64(e.weight)
		targetCandidates = append(targetCandidates, seedCandidate{from: e.from, distance: candidate, edgeID: id})
		if id != modifiedID && candidate == oldDist[target] {
			alternativeCount++
			if s.parent[target] == noParent || id < s.parent[target] {
				s.parent[target] = id
			}
		}
		if candidate < oldDist[target] {
			updatedAlternative = true
		}
	}
	if alternativeCount > 0 && !updatedAlternative {
		return
	}
	tightParent[target] = s.parent[target]

	ensureDegree := func(v int) int {
		if degreeReady[v] {
			return tightDegree[v]
		}
		if v == target {
			degreeReady[v] = true
			tightDegree[v] = 0
			return 0
		}
		count, parent := 0, noParent
		s.examined += uint64(len(s.in[v]))
		for _, id := range s.in[v] {
			if id == modifiedID {
				continue
			}
			e := s.edges[id]
			if !e.alive || oldDist[e.from] == unreachable || oldDist[v] == unreachable {
				continue
			}
			if oldDist[e.from]+int64(e.weight) == oldDist[v] {
				count++
				if parent == noParent || id < parent {
					parent = id
				}
			}
		}
		degreeReady[v], tightDegree[v], tightParent[v] = true, count, parent
		return count
	}

	affected[target] = struct{}{}
	frontier = append(frontier, target)
	for len(frontier) > 0 {
		u := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		s.examined += uint64(len(s.out[u]))
		for _, id := range s.out[u] {
			e := s.edges[id]
			if !e.alive || oldDist[e.from] == unreachable || oldDist[e.to] == unreachable {
				continue
			}
			oldWeight := e.weight
			if id == modifiedID {
				oldWeight = oldWeightValue
			}
			if oldDist[e.from]+int64(oldWeight) != oldDist[e.to] {
				continue
			}
			if _, isAffected := affected[e.to]; isAffected {
				continue
			}
			degree := ensureDegree(e.to) - 1
			tightDegree[e.to] = degree
			if degree == 0 {
				affected[e.to] = struct{}{}
				frontier = append(frontier, e.to)
			}
		}
	}

	boundary := make([]int, 0)
	for v := range degreeReady {
		if _, isAffected := affected[v]; !isAffected && tightDegree[v] > 0 {
			boundary = append(boundary, v)
		}
	}
	if len(boundary) == 0 && len(affected) == 0 {
		return
	}
	updatedBoundary := map[int]struct{}{}

	pq := &distanceHeap{}
	heap.Init(pq)
	for v := range affected {
		if v == s.source {
			s.dist[v] = 0
			s.parent[v] = noParent
			heap.Push(pq, heapItem{node: v, distance: 0})
			continue
		}
		s.dist[v] = unreachable
		s.parent[v] = noParent
		if v == target {
			for _, seed := range targetCandidates {
				if _, fromAffected := affected[seed.from]; fromAffected {
					continue
				}
				if s.dist[v] == unreachable || seed.distance < s.dist[v] {
					s.dist[v], s.parent[v] = seed.distance, seed.edgeID
				} else if seed.distance == s.dist[v] && seed.edgeID < s.parent[v] {
					s.parent[v] = seed.edgeID
				}
			}
		} else {
			s.examined += uint64(len(s.in[v]))
			for _, id := range s.in[v] {
				e := s.edges[id]
				if !e.alive || (id == modifiedID && !modifiedAlive) {
					continue
				}
				if _, fromAffected := affected[e.from]; fromAffected || oldDist[e.from] == unreachable {
					continue
				}
				candidate := oldDist[e.from] + int64(e.weight)
				if s.dist[v] == unreachable || candidate < s.dist[v] {
					s.dist[v], s.parent[v] = candidate, id
				} else if candidate == s.dist[v] && id < s.parent[v] {
					s.parent[v] = id
				}
			}
		}
		if s.dist[v] != unreachable {
			heap.Push(pq, heapItem{node: v, distance: s.dist[v]})
		}
	}

	seeds := map[int]struct{}{}
	addSeed := func(v int) {
		if _, ok := seeds[v]; ok || oldDist[v] == unreachable {
			return
		}
		seeds[v] = struct{}{}
		heap.Push(pq, heapItem{node: v, distance: oldDist[v]})
	}
	for v := range affected {
		for _, id := range s.in[v] {
			e := s.edges[id]
			if e.alive {
				if _, fromAffected := affected[e.from]; !fromAffected {
					addSeed(e.from)
				}
			}
		}
	}
	for u := range affected {
		for _, id := range s.out[u] {
			e := s.edges[id]
			if e.alive && oldDist[u] != unreachable && oldDist[e.to] != unreachable {
				for _, seedID := range s.in[e.to] {
					seedEdge := s.edges[seedID]
					if seedEdge.alive {
						if _, fromAffected := affected[seedEdge.from]; !fromAffected {
							addSeed(seedEdge.from)
						}
					}
				}
			}
		}
	}
	for pq.Len() > 0 {
		item := heap.Pop(pq).(heapItem)
		u := item.node
		if item.distance != s.dist[u] {
			continue
		}
		s.examined += uint64(len(s.out[u]))
		for _, id := range s.out[u] {
			e := s.edges[id]
			if !e.alive || s.dist[u] == unreachable {
				continue
			}
			candidate := s.dist[u] + int64(e.weight)
			if s.dist[e.to] == unreachable || candidate < s.dist[e.to] {
				s.dist[e.to], s.parent[e.to] = candidate, id
				heap.Push(pq, heapItem{node: e.to, distance: candidate})
				if _, isAffected := affected[e.to]; !isAffected {
					affected[e.to] = struct{}{}
					for _, seedID := range s.in[e.to] {
						seedEdge := s.edges[seedID]
						if seedEdge.alive {
							if _, fromAffected := affected[seedEdge.from]; !fromAffected {
								addSeed(seedEdge.from)
							}
						}
					}
				}
			} else if candidate == s.dist[e.to] {
				if _, isAffected := affected[e.to]; !isAffected {
					updatedBoundary[e.to] = struct{}{}
				}
				currentParent := s.parent[e.to]
				if currentParent == noParent {
					s.parent[e.to] = id
					continue
				}
				current := s.edges[currentParent]
				if current == nil || !current.alive || s.dist[current.from] == unreachable || id < currentParent {
					s.parent[e.to] = id
				}
			}
		}
	}

	for _, v := range boundary {
		_, isAffected := affected[v]
		_, updated := updatedBoundary[v]
		if isAffected || updated {
			continue
		}
		if parent, ok := tightParent[v]; ok {
			s.parent[v] = parent
		}
	}
}
