package ontology

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidPredicate = errors.New("invalid predicate")
	ErrContradiction    = errors.New("contradiction")
	ErrUnknownColumn    = errors.New("unknown column")
)

type class struct {
	lo       int64
	hi       int64
	excluded map[int64]struct{}
}

type deriverState struct {
	parent    map[string]string
	rank      map[string]int
	classes   map[string]*class
	different map[string]map[string]struct{}
}

func newState() *deriverState {
	return &deriverState{
		parent:    make(map[string]string),
		rank:      make(map[string]int),
		classes:   make(map[string]*class),
		different: make(map[string]map[string]struct{}),
	}
}

func (s *deriverState) clone() *deriverState {
	c := newState()
	for column, parent := range s.parent {
		c.parent[column] = parent
	}
	for column, rank := range s.rank {
		c.rank[column] = rank
	}
	for root, current := range s.classes {
		excluded := make(map[int64]struct{}, len(current.excluded))
		for value := range current.excluded {
			excluded[value] = struct{}{}
		}
		c.classes[root] = &class{lo: current.lo, hi: current.hi, excluded: excluded}
	}
	for root, neighbors := range s.different {
		copied := make(map[string]struct{}, len(neighbors))
		for neighbor := range neighbors {
			copied[neighbor] = struct{}{}
		}
		c.different[root] = copied
	}
	return c
}

func (s *deriverState) ensureColumn(column string) {
	if _, ok := s.parent[column]; ok {
		return
	}
	s.parent[column] = column
	s.rank[column] = 0
	s.classes[column] = &class{lo: MinInt64, hi: MaxInt64, excluded: make(map[int64]struct{})}
}

func (s *deriverState) find(column string) string {
	parent := s.parent[column]
	if parent == column {
		return column
	}
	root := s.find(parent)
	s.parent[column] = root
	return root
}

func (s *deriverState) registeredRoot(column string) (string, bool) {
	if _, ok := s.parent[column]; !ok {
		return "", false
	}
	return s.find(column), true
}

func (s *deriverState) unionColumns(a, b string) {
	rootA := s.find(a)
	rootB := s.find(b)
	if rootA == rootB {
		return
	}
	if s.rank[rootA] < s.rank[rootB] {
		rootA, rootB = rootB, rootA
	}

	classA := s.classes[rootA]
	classB := s.classes[rootB]
	classA.lo = maxInt64(classA.lo, classB.lo)
	classA.hi = minInt64(classA.hi, classB.hi)
	for value := range classB.excluded {
		classA.excluded[value] = struct{}{}
	}

	s.parent[rootB] = rootA
	if s.rank[rootA] == s.rank[rootB] {
		s.rank[rootA]++
	}
	delete(s.classes, rootB)
	delete(s.rank, rootB)
	s.relinkDifferent(rootA, rootB)
}

func (s *deriverState) relinkDifferent(keep, removed string) {
	for neighbor := range s.different[removed] {
		if neighbor != keep {
			s.addDifferent(keep, s.find(neighbor))
		}
		delete(s.different[neighbor], removed)
	}
	delete(s.different, removed)
	for _, neighbors := range s.different {
		delete(neighbors, removed)
	}
}

func (s *deriverState) addDifferent(a, b string) {
	if a == b {
		return
	}
	if s.different[a] == nil {
		s.different[a] = make(map[string]struct{})
	}
	if s.different[b] == nil {
		s.different[b] = make(map[string]struct{})
	}
	s.different[a][b] = struct{}{}
	s.different[b][a] = struct{}{}
}

func (s *deriverState) close() error {
	for {
		s.canonicalizeGraph()
		if s.hasSelfDifferent() {
			return ErrContradiction
		}

		changed := false
		for root, neighbors := range s.different {
			currentClass := s.classes[root]
			for neighbor := range neighbors {
				neighborClass := s.classes[neighbor]
				if currentClass.lo == currentClass.hi {
					value := currentClass.lo
					if neighborClass.lo == neighborClass.hi && neighborClass.lo == value {
						return ErrContradiction
					}
					if value >= neighborClass.lo && value <= neighborClass.hi {
						if _, exists := neighborClass.excluded[value]; !exists {
							neighborClass.excluded[value] = struct{}{}
							changed = true
						}
					}
				}
			}
		}

		for _, currentClass := range s.classes {
			nextChanged, err := currentClass.normalize()
			if err != nil {
				return err
			}
			changed = changed || nextChanged
		}

		if !changed {
			return nil
		}
	}
}

func (s *deriverState) canonicalizeGraph() {
	next := make(map[string]map[string]struct{})
	add := func(a, b string) {
		if next[a] == nil {
			next[a] = make(map[string]struct{})
		}
		next[a][b] = struct{}{}
	}

	for root, neighbors := range s.different {
		canonicalRoot := s.find(root)
		for neighbor := range neighbors {
			canonicalNeighbor := s.find(neighbor)
			add(canonicalRoot, canonicalNeighbor)
			add(canonicalNeighbor, canonicalRoot)
		}
	}
	s.different = next
}

func (s *deriverState) hasSelfDifferent() bool {
	for root, neighbors := range s.different {
		if _, selfDifferent := neighbors[root]; selfDifferent {
			return true
		}
	}
	return false
}

func (c *class) normalize() (bool, error) {
	changed := false
	for value := range c.excluded {
		if value < c.lo || value > c.hi {
			delete(c.excluded, value)
			changed = true
		}
	}

	for {
		if c.lo > c.hi {
			return changed, ErrContradiction
		}
		if _, excluded := c.excluded[c.lo]; excluded {
			if c.lo == MaxInt64 {
				return changed, ErrContradiction
			}
			c.lo++
			changed = true
			continue
		}
		if _, excluded := c.excluded[c.hi]; excluded {
			if c.hi == MinInt64 {
				return changed, ErrContradiction
			}
			c.hi--
			changed = true
			continue
		}
		break
	}

	for value := range c.excluded {
		if value < c.lo || value > c.hi {
			delete(c.excluded, value)
			changed = true
		}
	}
	if c.lo > c.hi {
		return changed, ErrContradiction
	}
	return changed, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func invalidPredicatef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPredicate, fmt.Sprintf(format, args...))
}
