package ontology

import (
	"errors"
	"sync"
)

const (
	unreachable int64 = 1 << 50
	noParent          = -1
)

type ErrorCode string

const (
	InvalidArgument ErrorCode = "invalid argument"
	EdgeNotFound    ErrorCode = "edge not found"
	CapacityFull    ErrorCode = "capacity full"
	HistoryExpired  ErrorCode = "history expired"
	VersionFuture   ErrorCode = "version not yet produced"
)

type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	return string(e.Code) + ": " + e.Msg
}

func errorWith(code ErrorCode, msg string) error {
	return &Error{Code: code, Msg: msg}
}

func ErrorCodeOf(err error) ErrorCode {
	var serviceErr *Error
	if errors.As(err, &serviceErr) {
		return serviceErr.Code
	}
	return ""
}

type Edge struct {
	ID     int
	From   int
	To     int
	Weight int
}

type UpdateResult struct {
	Version  int
	EdgeID   int
	DChanged []int
	PChanged []int
}

type Service struct {
	mu       sync.RWMutex
	n        int
	source   int
	maxEdges int
	keep     int
	version  int
	examined uint64
	nextID   int
	live     int
	edges    map[int]*edge
	out      [][]int
	in       [][]int
	dist     []int64
	parent   []int
	history  []snapshot
}

type edge struct {
	id     int
	from   int
	to     int
	weight int
	alive  bool
}

type snapshot struct {
	version int
	dist    []int64
	parent  []int
}

func New(n, source, maxEdges, keep int) (*Service, error) {
	if n < 1 || n > 2000 {
		return nil, errorWith(InvalidArgument, "n must be in [1, 2000]")
	}
	if source < 0 || source >= n {
		return nil, errorWith(InvalidArgument, "source is out of range")
	}
	if maxEdges < 1 || maxEdges > 100000 {
		return nil, errorWith(InvalidArgument, "emax must be in [1, 100000]")
	}
	if keep < 1 || keep > 64 {
		return nil, errorWith(InvalidArgument, "k must be in [1, 64]")
	}

	return &Service{
		n:        n,
		source:   source,
		maxEdges: maxEdges,
		keep:     keep,
		nextID:   1,
		edges:    make(map[int]*edge),
		out:      make([][]int, n),
		in:       make([][]int, n),
		dist:     initialDist(n, source),
		parent:   initialParent(n),
		history: []snapshot{{
			version: 0,
			dist:    initialDist(n, source),
			parent:  initialParent(n),
		}},
	}, nil
}

func initialDist(n, source int) []int64 {
	dist := make([]int64, n)
	for i := range dist {
		dist[i] = unreachable
	}
	dist[source] = 0
	return dist
}

func initialParent(n int) []int {
	parent := make([]int, n)
	for i := range parent {
		parent[i] = noParent
	}
	return parent
}

func (s *Service) validateNode(v int) bool {
	return v >= 0 && v < s.n
}

func (s *Service) validateWeight(weight int) bool {
	return weight >= 1 && weight <= 1_000_000
}

func (s *Service) CurrentVersion() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

func (s *Service) Examined() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.examined
}

func (s *Service) Dist(v int) (int64, bool, error) {
	if !s.validateNode(v) {
		return 0, false, errorWith(InvalidArgument, "node is out of range")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.dist[v] == unreachable {
		return 0, false, nil
	}
	return s.dist[v], true, nil
}

func (s *Service) Parent(v int) (int, bool, error) {
	if !s.validateNode(v) {
		return 0, false, errorWith(InvalidArgument, "node is out of range")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.parent[v] == noParent {
		return 0, false, nil
	}
	return s.parent[v], true, nil
}

func (s *Service) Path(v int) ([]int, bool, error) {
	if !s.validateNode(v) {
		return nil, false, errorWith(InvalidArgument, "node is out of range")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.dist[v] == unreachable {
		return nil, false, nil
	}
	if v == s.source {
		return []int{}, true, nil
	}

	path := make([]int, 0, s.n)
	current := v
	for current != s.source {
		id := s.parent[current]
		if id == noParent {
			return nil, false, nil
		}
		path = append(path, id)
		parentEdge := s.edges[id]
		current = parentEdge.from
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, true, nil
}

func (s *Service) DistAt(v, version int) (int64, bool, error) {
	if !s.validateNode(v) {
		return 0, false, errorWith(InvalidArgument, "node is out of range")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if version > s.version {
		return 0, false, errorWith(VersionFuture, "version has not been produced")
	}
	oldest := s.history[0].version
	if version < oldest {
		return 0, false, errorWith(HistoryExpired, "version is no longer retained")
	}
	snap := s.history[version-oldest]
	if snap.dist[v] == unreachable {
		return 0, false, nil
	}
	return snap.dist[v], true, nil
}

func (s *Service) appendSnapshot() {
	dist := append([]int64(nil), s.dist...)
	parent := append([]int(nil), s.parent...)
	s.history = append(s.history, snapshot{
		version: s.version,
		dist:    dist,
		parent:  parent,
	})
	if len(s.history) > s.keep {
		s.history = s.history[len(s.history)-s.keep:]
	}
}
