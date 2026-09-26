// Package sites keeps an ordered site registry with a grid-based nearest query.
package sites

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"ontology/nbr"
)

const (
	maxCoord = 10_000
	cellSize = 32
)

// minCell/maxCell bound the fixed cell square containing every site.
var minCell, maxCell = cellOf(-maxCoord), cellOf(maxCoord)

// The rejected-operation errors are distinct sentinels.
var (
	ErrDuplicate, ErrOutOfBounds, ErrBadRadius, ErrEmpty = errors.New("sites: duplicate coordinate"),
		errors.New("sites: coordinate out of bounds"), errors.New("sites: negative radius"),
		errors.New("sites: registry is empty")
)

// Registry is an ordered table of sites; insertion order is the site index.
type Registry struct {
	mu     sync.RWMutex
	pts    []nbr.Point         // index-ordered sites
	seen   map[[2]int]struct{} // occupied coordinates
	cells  map[[2]int][]int    // grid cell -> site indices in add order
	probed atomic.Int64        // sites evaluated by the most recent Nearest
}

// New returns an empty registry.
func New() *Registry { return &Registry{seen: map[[2]int]struct{}{}, cells: map[[2]int][]int{}} }

// cellOf maps a coordinate to its floor-divided grid cell index.
func cellOf(v int) int {
	if v >= 0 {
		return v / cellSize
	}
	return (v - cellSize + 1) / cellSize
}

// Add registers a site and returns its index; rejected adds leave no trace.
func (r *Registry) Add(x, y int) (int, error) {
	if x < -maxCoord || x > maxCoord || y < -maxCoord || y > maxCoord {
		return 0, ErrOutOfBounds
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := [2]int{x, y}
	if _, ok := r.seen[key]; ok {
		return 0, ErrDuplicate
	}
	idx := len(r.pts)
	r.pts = append(r.pts, nbr.Point{X: x, Y: y})
	r.seen[key] = struct{}{}
	cx, cy := cellOf(x), cellOf(y)
	r.cells[[2]int{cx, cy}] = append(r.cells[[2]int{cx, cy}], idx)
	return idx, nil
}

// Nearest returns the closest site index; ties go to the smallest index.
func (r *Registry) Nearest(qx, qy int) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	r.probed.Store(0)
	if len(r.pts) == 0 {
		return 0, ErrEmpty
	}
	q := nbr.Point{X: qx, Y: qy}
	// Clamp cell origin onto the site square: argmin/tie set unchanged.
	cx0, cy0 := cellOf(max(-maxCoord, min(maxCoord, qx))), cellOf(max(-maxCoord, min(maxCoord, qy)))
	best, bestD2, found := -1, int64(0), false
	scan := func(cx, cy int) {
		for _, i := range r.cells[[2]int{cx, cy}] {
			r.probed.Add(1)
			if d2 := nbr.Dist2(r.pts[i], q); !found || d2 < bestD2 || (d2 == bestD2 && i < best) {
				found, bestD2, best = true, d2, i
			}
		}
	}
	for k := 0; ; k++ {
		if k == 0 {
			scan(cx0, cy0)
		} else {
			for d := -k; d <= k; d++ {
				scan(cx0-k, cy0+d)
				scan(cx0+k, cy0+d)
			}
			for d := -k + 1; d < k; d++ {
				scan(cx0+d, cy0-k)
				scan(cx0+d, cy0+k)
			}
		}
		// Ring k+1 has d2 >= lb^2; strict > preserves equal-distance ties.
		if lb := int64(k*cellSize + 1); found && lb*lb > bestD2 {
			return best, nil
		}
		// Rings 0..k now cover the whole cell square: no site stays unseen.
		if cx0-k <= minCell && cx0+k >= maxCell && cy0-k <= minCell && cy0+k >= maxCell {
			return best, nil
		}
	}
}

// Within counts sites strictly inside the circle (d2 < r2); boundary excluded.
func (r *Registry) Within(qx, qy, radius int) (int, error) {
	if radius < 0 {
		return 0, ErrBadRadius
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if radius >= 100000 {
		return len(r.pts), nil // r^2 > max d2; avoids int64 overflow too
	}
	r2, q, n := int64(radius)*int64(radius), nbr.Point{X: qx, Y: qy}, 0
	for _, p := range r.pts {
		if nbr.Inside(nbr.Dist2(p, q), r2) {
			n++
		}
	}
	return n, nil
}

// Len returns the number of registered sites.
func (r *Registry) Len() int { r.mu.RLock(); defer r.mu.RUnlock(); return len(r.pts) }

// CheckSublinear verifies the Nearest candidate count stays bounded by an
// m-independent constant; only pass/fail is reported, never the count itself.
func CheckSublinear() error {
	for _, n := range []int{100, 400, 1000, 4000, 10000} {
		r, side := New(), int(math.Ceil(math.Sqrt(float64(n))))
		for i := 0; i < n; i++ {
			r.Add((i%side-side/2)*200, (i/side-side/2)*200)
		}
		for _, q := range [][2]int{{0, 0}, {100, 100}, {-97, 53}} {
			r.Nearest(q[0], q[1])
			if r.probed.Load() > 32 {
				return fmt.Errorf("sites: Nearest not sublinear at m=%d", n)
			}
		}
	}
	return nil
}
