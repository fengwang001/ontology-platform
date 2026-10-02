package dynamicforest

import "sync"

const (
	minNodeCount = 1
	maxNodeCount = 100_000
	minEdgeLimit = 1
	maxEdgeLimit = 500_000
	minWeight    = -1_000_000_000
	maxWeight    = 1_000_000_000
)

type Edge struct {
	ID     int
	U      int
	V      int
	Weight int64
}

type edgeRecord struct {
	id       int
	u        int
	v        int
	weight   int64
	alive    bool
	inForest bool
	since    int
}

type UpdateResult struct {
	Version    int
	Entered    []int
	Left       []int
	Weight     int64
	Components int
}

type PathMaxResult struct {
	EdgeID int
	Weight int64
}

type Service struct {
	mu sync.Mutex

	nodeCount  int
	edgeLimit  int
	edges      []*edgeRecord
	adjacency  []map[int]struct{}
	forestAdj  []map[int]struct{}
	forestTree *linkCutForest

	version     int
	nextID      int
	aliveEdges  int
	totalWeight int64
	components  int
	scanned     int

	bfsMark  []int
	bfsStamp int
}

func validWeight(weight int64) bool {
	return weight >= minWeight && weight <= maxWeight
}

func validNode(node int, nodeCount int) bool {
	return node >= 0 && node < nodeCount
}

func NewService(nodeCount int, edgeLimit int) (*Service, error) {
	if nodeCount < minNodeCount || nodeCount > maxNodeCount || edgeLimit < minEdgeLimit || edgeLimit > maxEdgeLimit {
		return nil, ErrInvalidArgument
	}

	service := &Service{
		nodeCount:  nodeCount,
		edgeLimit:  edgeLimit,
		edges:      make([]*edgeRecord, 1),
		adjacency:  make([]map[int]struct{}, nodeCount),
		forestAdj:  make([]map[int]struct{}, nodeCount),
		forestTree: newLinkCutForest(nodeCount, edgeLimit),
		nextID:     1,
		components: nodeCount,
		bfsMark:    make([]int, nodeCount),
	}
	for node := range service.adjacency {
		service.adjacency[node] = make(map[int]struct{})
		service.forestAdj[node] = make(map[int]struct{})
	}
	return service, nil
}

func emptyResult(version int, weight int64, components int) *UpdateResult {
	return &UpdateResult{
		Version:    version,
		Entered:    []int{},
		Left:       []int{},
		Weight:     weight,
		Components: components,
	}
}

func (s *Service) lookup(id int) (*edgeRecord, bool) {
	if id < 1 || id >= len(s.edges) {
		return nil, false
	}
	edge := s.edges[id]
	if edge == nil || !edge.alive {
		return nil, false
	}
	return edge, true
}

func (s *Service) addGraphEdge(edge *edgeRecord) {
	s.adjacency[edge.u][edge.id] = struct{}{}
	s.adjacency[edge.v][edge.id] = struct{}{}
}

func (s *Service) addForestEdge(edge *edgeRecord) {
	s.forestAdj[edge.u][edge.id] = struct{}{}
	s.forestAdj[edge.v][edge.id] = struct{}{}
}

func (s *Service) removeGraphEdge(edge *edgeRecord) {
	delete(s.adjacency[edge.u], edge.id)
	delete(s.adjacency[edge.v], edge.id)
}

func (s *Service) removeForestEdge(edge *edgeRecord) {
	delete(s.forestAdj[edge.u], edge.id)
	delete(s.forestAdj[edge.v], edge.id)
}

func (s *Service) markEntered(edge *edgeRecord) {
	edge.inForest = true
	edge.since = s.version
}

func (s *Service) markLeft(edge *edgeRecord) {
	edge.inForest = false
	edge.since = 0
}

func (s *Service) AddEdge(u int, v int, weight int64) (int, *UpdateResult, error) {
	if !validNode(u, s.nodeCount) || !validNode(v, s.nodeCount) || u == v || !validWeight(weight) {
		return 0, nil, ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.aliveEdges >= s.edgeLimit {
		return 0, nil, ErrCapacityFull
	}

	id := s.nextID
	edge := &edgeRecord{
		id:     id,
		u:      u,
		v:      v,
		weight: weight,
		alive:  true,
	}
	s.edges = append(s.edges, edge)
	s.forestTree.ensureEdgeCapacity(id, s.nodeCount)
	s.nextID++
	s.aliveEdges++
	s.version++
	s.addGraphEdge(edge)

	first := vertexNode(u)
	second := vertexNode(v)
	result := emptyResult(s.version, s.totalWeight, s.components)

	if s.forestTree.connected(first, second) {
		maxID, maxWeight := s.forestTree.pathMax(first, second)
		if smallerKey(id, weight, maxID, maxWeight) {
			old := s.edges[maxID]
			s.forestTree.cutEdge(old, s.nodeCount)
			s.removeForestEdge(old)
			s.forestTree.linkEdge(edge, s.nodeCount)
			s.addForestEdge(edge)
			s.totalWeight += weight - old.weight
			s.markLeft(old)
			s.markEntered(edge)
			result.Entered = []int{id}
			result.Left = []int{maxID}
			result.Weight = s.totalWeight
		}
	} else {
		s.forestTree.linkEdge(edge, s.nodeCount)
		s.addForestEdge(edge)
		s.totalWeight += weight
		s.components--
		s.markEntered(edge)
		result.Entered = []int{id}
		result.Weight = s.totalWeight
		result.Components = s.components
	}

	return id, result, nil
}

func (s *Service) replaceAcross(old *edgeRecord, includeSelf bool, previousWeight int64, result *UpdateResult) {
	s.forestTree.cutEdge(old, s.nodeCount)
	s.removeForestEdge(old)
	best := s.findReplacement(old, includeSelf)
	if best == nil {
		s.components++
		s.totalWeight -= previousWeight
		s.markLeft(old)
		result.Left = appendIfPositive(result.Left, old.id)
	} else {
		s.forestTree.linkEdge(best, s.nodeCount)
		s.addForestEdge(best)
		if best.id == old.id {
			s.totalWeight += best.weight - previousWeight
		} else {
			s.totalWeight += best.weight - previousWeight
			s.markLeft(old)
			s.markEntered(best)
			result.Entered = appendIfPositive(result.Entered, best.id)
			result.Left = appendIfPositive(result.Left, old.id)
		}
	}
	result.Weight = s.totalWeight
	result.Components = s.components
}

func appendIfPositive(values []int, value int) []int {
	if value > 0 {
		return append(values, value)
	}
	return values
}

func (s *Service) findReplacement(removed *edgeRecord, includeSelf bool) *edgeRecord {
	s.bfsStamp++
	mark := s.bfsStamp

	firstVertex := vertexNode(removed.u)
	secondVertex := vertexNode(removed.v)
	firstTreeSize := s.forestTree.componentSize(firstVertex)
	secondTreeSize := s.forestTree.componentSize(secondVertex)
	firstSize := (firstTreeSize + 1) / 2
	secondSize := (secondTreeSize + 1) / 2
	smallStart := removed.u
	targetStart := removed.v
	smallSize := firstSize
	if secondSize < firstSize {
		smallStart = removed.v
		targetStart = removed.u
		smallSize = secondSize
	}

	smallSide := make([]int, 0, smallSize)
	queue := []int{smallStart}
	s.bfsMark[smallStart] = mark
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		s.scanned++
		smallSide = append(smallSide, node)

		for edgeID := range s.forestAdj[node] {
			edge := s.edges[edgeID]
			if edge == nil || !edge.alive || edge.id == removed.id {
				continue
			}
			other := edge.u
			if edge.u == node {
				other = edge.v
			}
			if s.bfsMark[other] == mark {
				continue
			}
			s.bfsMark[other] = mark
			queue = append(queue, other)
		}
	}

	var best *edgeRecord
	if includeSelf {
		best = removed
	}
	for _, node := range smallSide {
		for edgeID := range s.adjacency[node] {
			edge := s.edges[edgeID]
			if edge == nil || !edge.alive || edge.id == removed.id {
				continue
			}
			other := edge.u
			if edge.u == node {
				other = edge.v
			}
			if s.bfsMark[other] == mark {
				continue
			}
			if edge.id != removed.id && edge.inForest {
				continue
			}
			if !s.forestTree.connected(vertexNode(other), vertexNode(targetStart)) {
				continue
			}
			if best == nil || smallerKey(edge.id, edge.weight, best.id, best.weight) {
				best = edge
			}
		}
	}
	return best
}

func (s *Service) SetWeight(id int, weight int64) (*UpdateResult, error) {
	if id < 1 || !validWeight(weight) {
		return nil, ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	edge, ok := s.lookup(id)
	if !ok {
		return nil, ErrEdgeNotFound
	}

	oldWeight := edge.weight
	edge.weight = weight
	s.version++
	s.forestTree.setEdgeWeight(edge, s.nodeCount)
	result := emptyResult(s.version, s.totalWeight, s.components)

	if edge.inForest {
		s.replaceAcross(edge, true, oldWeight, result)
	} else {
		first := vertexNode(edge.u)
		second := vertexNode(edge.v)
		if s.forestTree.connected(first, second) {
			maxID, maxWeight := s.forestTree.pathMax(first, second)
			if smallerKey(edge.id, weight, maxID, maxWeight) {
				old := s.edges[maxID]
				s.forestTree.cutEdge(old, s.nodeCount)
				s.removeForestEdge(old)
				s.forestTree.linkEdge(edge, s.nodeCount)
				s.addForestEdge(edge)
				s.totalWeight += weight - old.weight
				s.markLeft(old)
				s.markEntered(edge)
				result.Entered = []int{id}
				result.Left = []int{maxID}
				result.Weight = s.totalWeight
			}
		}
	}

	return result, nil
}

func (s *Service) RemoveEdge(id int) (*UpdateResult, error) {
	if id < 1 {
		return nil, ErrEdgeNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	edge, ok := s.lookup(id)
	if !ok {
		return nil, ErrEdgeNotFound
	}

	s.version++
	s.removeGraphEdge(edge)
	edge.alive = false
	s.aliveEdges--
	result := emptyResult(s.version, s.totalWeight, s.components)

	if edge.inForest {
		s.replaceAcross(edge, false, edge.weight, result)
	}

	return result, nil
}

func (s *Service) InForest(id int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	edge, ok := s.lookup(id)
	if !ok {
		return false, ErrEdgeNotFound
	}
	return edge.inForest, nil
}

func (s *Service) TreeSince(id int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	edge, ok := s.lookup(id)
	if !ok {
		return 0, ErrEdgeNotFound
	}
	return edge.since, nil
}

func (s *Service) Weight() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalWeight
}

func (s *Service) Components() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.components
}

func (s *Service) Connected(u int, v int) (bool, error) {
	if !validNode(u, s.nodeCount) || !validNode(v, s.nodeCount) {
		return false, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forestTree.connected(vertexNode(u), vertexNode(v)), nil
}

func (s *Service) PathMax(u int, v int) (*PathMaxResult, error) {
	if !validNode(u, s.nodeCount) || !validNode(v, s.nodeCount) {
		return nil, ErrInvalidArgument
	}
	if u == v {
		return nil, ErrSameNode
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.forestTree.connected(vertexNode(u), vertexNode(v)) {
		return nil, ErrNotConnected
	}
	id, weight := s.forestTree.pathMax(vertexNode(u), vertexNode(v))
	if id == 0 {
		return nil, ErrNotConnected
	}
	return &PathMaxResult{EdgeID: id, Weight: weight}, nil
}

func (s *Service) Scanned() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanned
}

func (s *Service) Version() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}
