package ontology

import (
	"container/heap"
	"sort"
)

func (s *Service) applyDecrease(modifiedID int, oldDist []int64) {
	modified := s.edges[modifiedID]
	decreased := make(map[int]struct{})
	newTight := make(map[int]struct{})

	pq := &distanceHeap{}
	heap.Init(pq)

	if oldDist[modified.from] != unreachable {
		candidate := oldDist[modified.from] + int64(modified.weight)
		if oldDist[modified.to] == unreachable || candidate < oldDist[modified.to] {
			s.dist[modified.to] = candidate
			decreased[modified.to] = struct{}{}
			heap.Push(pq, heapItem{node: modified.to, distance: candidate})
		} else if candidate == oldDist[modified.to] {
			s.recomputeParent(modified.to)
			return
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
			if !e.alive || s.dist[e.from] == unreachable {
				continue
			}
			candidate := s.dist[e.from] + int64(e.weight)
			if oldDist[e.to] == unreachable || candidate <= oldDist[e.to] {
				if s.dist[e.to] == unreachable || candidate < s.dist[e.to] {
					s.dist[e.to] = candidate
					decreased[e.to] = struct{}{}
					heap.Push(pq, heapItem{node: e.to, distance: candidate})
				} else if candidate == oldDist[e.to] {
					newTight[e.to] = struct{}{}
				}
			} else if candidate == oldDist[e.to] {
				if oldDist[e.from] == unreachable || oldDist[e.from]+int64(e.weight) > oldDist[e.to] {
					newTight[e.to] = struct{}{}
				}
				continue
			}
		}
	}

	settled := make([]int, 0, len(decreased))
	for v := range decreased {
		settled = append(settled, v)
	}
	sort.Slice(settled, func(i, j int) bool {
		if s.dist[settled[i]] != s.dist[settled[j]] {
			return s.dist[settled[i]] < s.dist[settled[j]]
		}
		return settled[i] < settled[j]
	})
	for _, v := range settled {
		s.recomputeParent(v)
	}
	for u := range decreased {
		if modifiedID == 190 && u == 2 {
			println("scan decreased 2", s.dist[2])
		}
		s.examined += uint64(len(s.out[u]))
		for _, id := range s.out[u] {
			e := s.edges[id]
			if !e.alive || s.dist[e.from] == unreachable {
				continue
			}
			candidate := s.dist[e.from] + int64(e.weight)
			if _, isDecreased := decreased[e.to]; !isDecreased && candidate == s.dist[e.to] {
				newTight[e.to] = struct{}{}
			}
			if s.parent[e.to] == id && candidate != s.dist[e.to] {
				newTight[e.to] = struct{}{}
			}
		}
	}

	for v := range newTight {
		if _, ok := decreased[v]; ok {
			continue
		}
		if s.dist[v] != unreachable {
			s.recomputeParent(v)
		}
	}
}

func (s *Service) recomputeParent(v int) {
	s.parent[v] = noParent
	for _, id := range s.in[v] {
		s.examined++
		e := s.edges[id]
		if !e.alive || s.dist[e.from] == unreachable || s.dist[v] == unreachable {
			continue
		}
		if s.dist[e.from]+int64(e.weight) == s.dist[v] {
			if s.parent[v] == noParent || id < s.parent[v] {
				s.parent[v] = id
			}
		}
	}
}
