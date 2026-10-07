package graph

type edge struct {
	toID     string
	linkType string
}

type graphState struct {
	nodes    map[string]struct{}
	outgoing map[string][]edge
	incoming map[string][]edge
}

func newGraphState() *graphState {
	return &graphState{
		nodes:    make(map[string]struct{}),
		outgoing: make(map[string][]edge),
		incoming: make(map[string][]edge),
	}
}

func (s *graphState) clone() *graphState {
	next := newGraphState()
	for id := range s.nodes {
		next.nodes[id] = struct{}{}
	}
	for id, edges := range s.outgoing {
		next.outgoing[id] = append([]edge(nil), edges...)
	}
	for id, edges := range s.incoming {
		next.incoming[id] = append([]edge(nil), edges...)
	}
	return next
}

func (s *graphState) hasObject(id string) bool {
	_, exists := s.nodes[id]
	return exists
}
