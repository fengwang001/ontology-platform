package fence

type Cell struct {
	X, Y int64
}

// baseRange is the immutable merchant footprint. Ring distance is an
// explicit level label supplied at registration (the geometry that derives
// it is irrelevant to this service); only membership and the label matter.
type baseRange struct {
	rings  map[Cell]int
	radius int
}

func newBaseRange(cells map[Cell]int) *baseRange {
	r := &baseRange{rings: make(map[Cell]int, len(cells))}
	for cell, ring := range cells {
		r.rings[cell] = ring
		if ring > r.radius {
			r.radius = ring
		}
	}
	return r
}

func (r *baseRange) baseRadius() int { return r.radius }

type Reachability int

const (
	Reachable Reachability = iota
	OutsideForever
	ShrunkTemporarily
)

// classify is O(1): one hash lookup and one integer comparison, independent
// of the total number of cells in the footprint.
func (r *baseRange) classify(cell Cell, effectiveRadius int) Reachability {
	ring, ok := r.rings[cell]
	if !ok {
		return OutsideForever
	}
	if ring <= effectiveRadius {
		return Reachable
	}
	return ShrunkTemporarily
}
