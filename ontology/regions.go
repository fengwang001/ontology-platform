package ontology

import (
	"errors"
	"sort"
	"sync"
)

const maxCoordinate = 1_000_000_000

var (
	ErrCoordinateOutOfRange = errors.New("coordinate out of range")
	ErrTooFewVertices       = errors.New("polygon must have at least 3 vertices")
	ErrOuterNotConvex       = errors.New("outer polygon is not strictly convex")
	ErrHoleNotConvex        = errors.New("hole polygon is not strictly convex")
	ErrHoleOutsideOuter     = errors.New("hole vertex is not strictly inside outer polygon")
	ErrRegionExists         = errors.New("region already exists")
	ErrRegionNotFound       = errors.New("region not found")
)

type Point struct {
	X int64
	Y int64
}

type Region struct {
	ID       string
	Priority int64
	Outer    []Point
	Hole     []Point
}

type RegionSet struct {
	mu      sync.RWMutex
	regions map[string]Region
}

func NewRegionSet() *RegionSet {
	return &RegionSet{regions: make(map[string]Region)}
}

func (s *RegionSet) Put(region Region) error {
	prepared, err := validateAndPrepareRegion(region)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.regions[prepared.ID]; exists {
		return ErrRegionExists
	}
	s.regions[prepared.ID] = prepared
	return nil
}

func (s *RegionSet) Replace(region Region) error {
	prepared, err := validateAndPrepareRegion(region)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.regions[prepared.ID]; !exists {
		return ErrRegionNotFound
	}
	s.regions[prepared.ID] = prepared
	return nil
}

func (s *RegionSet) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.regions[id]; !exists {
		return ErrRegionNotFound
	}
	delete(s.regions, id)
	return nil
}

func (s *RegionSet) Best(x, y int64) (Region, bool, error) {
	matches, err := s.Locate(x, y)
	if err != nil {
		return Region{}, false, err
	}
	if len(matches) == 0 {
		return Region{}, false, nil
	}
	return matches[0], true, nil
}

func (s *RegionSet) Locate(x, y int64) ([]Region, error) {
	if !coordinateInRange(x) || !coordinateInRange(y) {
		return nil, ErrCoordinateOutOfRange
	}
	query := Point{X: x, Y: y}

	s.mu.RLock()
	matches := make([]Region, 0)
	for _, region := range s.regions {
		if regionContains(region, query) {
			matches = append(matches, cloneRegion(region))
		}
	}
	s.mu.RUnlock()

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Priority != matches[j].Priority {
			return matches[i].Priority > matches[j].Priority
		}
		return matches[i].ID < matches[j].ID
	})
	return matches, nil
}

func validateAndPrepareRegion(region Region) (Region, error) {
	if !validateCoordinates(region.Outer...) || !validateCoordinates(region.Hole...) {
		return Region{}, ErrCoordinateOutOfRange
	}
	if len(region.Outer) < 3 || (len(region.Hole) != 0 && len(region.Hole) < 3) {
		return Region{}, ErrTooFewVertices
	}
	if !isStrictlyConvex(region.Outer) {
		return Region{}, ErrOuterNotConvex
	}
	if len(region.Hole) > 0 && !isStrictlyConvex(region.Hole) {
		return Region{}, ErrHoleNotConvex
	}
	for _, vertex := range region.Hole {
		if !pointStrictInsideConvex(vertex, region.Outer) {
			return Region{}, ErrHoleOutsideOuter
		}
	}
	return cloneRegion(region), nil
}

func regionContains(region Region, p Point) bool {
	if !pointOnOrInsideConvex(p, region.Outer) {
		return false
	}
	if len(region.Hole) > 0 && pointInOpenConvex(p, region.Hole) {
		return false
	}
	return true
}

func cloneRegion(region Region) Region {
	clone := Region{
		ID:       region.ID,
		Priority: region.Priority,
		Outer:    append([]Point(nil), region.Outer...),
	}
	if region.Hole != nil {
		clone.Hole = append([]Point(nil), region.Hole...)
	}
	return clone
}
