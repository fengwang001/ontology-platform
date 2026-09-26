// Package sites keeps the ordered site registry and nearest/disk queries; it
// depends only on package nbr.
package sites

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/nbr"
)

// The three fault-injection errors are mutually distinct sentinels.
var (
	ErrDuplicate      = errors.New("sites: duplicate site coordinates")
	ErrOutOfBounds    = errors.New("sites: site coordinates out of bounds")
	ErrNegativeRadius = errors.New("sites: radius must be non-negative")
	ErrEmpty          = errors.New("sites: no site registered")
)

const (
	maxCoord = 10000
	cellW    = 128 // grid cell edge; search expands ring by ring -> local work
)

// Store is the concurrency-safe in-memory registry; build it with New.
type Store struct {
	mu   sync.RWMutex
	pts  []nbr.Point // index = insertion order, never reordered
	seen map[nbr.Point]struct{}
	grid map[[2]int][]int // cell -> site indices, insertion ordered
	cand atomic.Int64     // sites whose d2 the last Nearest computed
}

// New returns an empty registry.
func New() *Store {
	return &Store{seen: map[nbr.Point]struct{}{}, grid: map[[2]int][]int{}}
}

// Add validates p and rejects duplicates before registering it. The returned
// index equals the insertion order; a rejection changes no state.
func (s *Store) Add(p nbr.Point) (int, error) {
	if p.X < -maxCoord || p.X > maxCoord || p.Y < -maxCoord || p.Y > maxCoord {
		return -1, ErrOutOfBounds
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[p]; ok {
		return -1, ErrDuplicate
	}
	idx := len(s.pts)
	s.pts = append(s.pts, p)
	s.seen[p] = struct{}{}
	k := [2]int{cell(p.X), cell(p.Y)}
	s.grid[k] = append(s.grid[k], idx)
	return idx, nil
}

// cell maps a coordinate to a floor-divided grid index (correct for negatives).
func cell(v int) int {
	c := v / cellW
	if v%cellW != 0 && v < 0 {
		c--
	}
	return c
}

var cellMin, cellMax = cell(-maxCoord), cell(maxCoord)

// Nearest returns the index minimizing d2 to q (ties -> smallest index). It
// expands Chebyshev rings, stopping once farther cells are provably worse.
func (s *Store) Nearest(q nbr.Point) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.pts) == 0 {
		return -1, ErrEmpty
	}
	cx, cy := cell(q.X), cell(q.Y)
	bestIdx, bestD, n := -1, int64(1<<63-1), int64(0)
	consider := func(gx, gy int) {
		for _, i := range s.grid[[2]int{gx, gy}] {
			d := nbr.Dist2(s.pts[i], q)
			n++
			if d < bestD || (d == bestD && i < bestIdx) {
				bestD, bestIdx = d, i
			}
		}
	}
	for k := 0; ; k++ {
		for gx := cx - k; gx <= cx+k; gx++ { // top and bottom edges
			consider(gx, cy-k)
			if k > 0 {
				consider(gx, cy+k)
			}
		}
		for gy := cy - k + 1; gy < cy+k; gy++ { // side edges (empty at k=0)
			consider(cx-k, gy)
			consider(cx+k, gy)
		}
		// Any cell in ring k+1 is at least (k*cellW+1) away on one axis.
		gap := int64(k*cellW + 1)
		if bestIdx >= 0 && gap*gap > bestD {
			break
		}
		if k >= cx-cellMin && k >= cellMax-cx && k >= cy-cellMin && k >= cellMax-cy {
			break // every in-domain cell has been visited
		}
	}
	s.cand.Store(n)
	return bestIdx, nil
}

// Sublinear verifies the measured-distance count stays below a small constant
// on grids up to 10000 sites. It returns only a verdict, never the counter.
func Sublinear() bool {
	const bound int64 = 16
	for _, k := range []int{10, 30, 50, 100} { // m = 100, 900, 2500, 10000
		s := New()
		for i := 0; i < k; i++ {
			for j := 0; j < k; j++ {
				if _, err := s.Add(nbr.Point{X: i * 100, Y: j * 100}); err != nil {
					return false
				}
			}
		}
		if idx, err := s.Nearest(nbr.Point{X: 50, Y: 50}); err != nil || idx != 0 {
			return false
		}
		if s.cand.Load() > bound {
			return false
		}
	}
	return true
}

// Within counts sites with d2 < r2 to q; sites on the circle are excluded.
func (s *Store) Within(q nbr.Point, r int) (int, error) {
	if r < 0 {
		return -1, ErrNegativeRadius
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r2, n := int64(r)*int64(r), 0
	for _, p := range s.pts {
		if nbr.Inside(nbr.Dist2(p, q), r2) {
			n++
		}
	}
	return n, nil
}
