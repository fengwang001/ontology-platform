package graph

import "sort"

func (s *graphState) neighbors(objectID string, directions []Direction) []edge {
	neighbors := make([]edge, 0)
	for _, direction := range directions {
		switch direction {
		case Outgoing:
			neighbors = append(neighbors, s.outgoing[objectID]...)
		case Incoming:
			neighbors = append(neighbors, s.incoming[objectID]...)
		}
	}

	sort.Slice(neighbors, func(i, j int) bool {
		if neighbors[i].toID != neighbors[j].toID {
			return neighbors[i].toID < neighbors[j].toID
		}
		return neighbors[i].linkType < neighbors[j].linkType
	})

	if len(neighbors) < 2 {
		return neighbors
	}

	unique := neighbors[:1]
	for _, candidate := range neighbors[1:] {
		last := unique[len(unique)-1]
		if candidate.toID != last.toID || candidate.linkType != last.linkType {
			unique = append(unique, candidate)
		}
	}
	return unique
}
