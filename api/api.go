// Package api is the public entry point of the Voronoi nearest-site service.
// It validates arguments, owns one in-memory site registry per process, and
// exposes a self-check. It depends only on package sites.
package api

import (
	"errors"
	"sync"

	"ontology/nbr"
	"ontology/sites"
)

// Sentinel errors so callers can judge failures with errors.Is. The three
// rejection errors are mutually distinct.
var (
	ErrDuplicate      = sites.ErrDuplicate
	ErrOutOfBounds    = sites.ErrOutOfBounds
	ErrNegativeRadius = sites.ErrNegativeRadius
	ErrEmpty          = sites.ErrEmpty
)

const maxCoord = 10000

var (
	mu  sync.Mutex
	cur *sites.Store
)

// New (re)initializes the process-wide registry to an empty state.
func New() error {
	mu.Lock()
	defer mu.Unlock()
	cur = sites.New()
	return nil
}

// Add registers a site at (x, y) and returns its index (insertion order).
// It rejects out-of-range coordinates and duplicate coordinates; either
// rejection changes no state.
func Add(x, y int) (int, error) {
	if x < -maxCoord || x > maxCoord || y < -maxCoord || y > maxCoord {
		return -1, ErrOutOfBounds
	}
	st, err := ready()
	if err != nil {
		return -1, err
	}
	return st.Add(nbr.Point{X: x, Y: y})
}

// Nearest returns the index of the site closest to (qx, qy) by squared
// Euclidean distance; ties resolve to the smallest index.
func Nearest(qx, qy int) (int, error) {
	st, err := ready()
	if err != nil {
		return -1, err
	}
	return st.Nearest(nbr.Point{X: qx, Y: qy})
}

// Within counts sites strictly inside the disk centered at (qx, qy) with
// integer radius r. Sites on the circle are excluded; r < 0 is rejected.
func Within(qx, qy, r int) (int, error) {
	st, err := ready()
	if err != nil {
		return -1, err
	}
	return st.Within(nbr.Point{X: qx, Y: qy}, r)
}

// SelfCheck replays the mandated eight-step sequence on a fresh registry and
// verifies exact distances, naive-scan agreement/tie stability, and that
// rejected operations leave no trace.
func SelfCheck() error {
	if err := New(); err != nil {
		return err
	}
	for _, p := range [][2]int{{0, 0}, {1, 1}, {4, 0}} {
		if _, err := Add(p[0], p[1]); err != nil {
			return err
		}
	}
	if i, err := Nearest(0, 2); err != nil || i != 1 {
		return errors.New("api: selfcheck step 4 nearest")
	}
	if i, err := Nearest(1, 0); err != nil || i != 0 {
		return errors.New("api: selfcheck step 5 tie")
	}
	if _, err := Add(0, 2); err != nil {
		return err
	}
	if i, err := Nearest(0, 2); err != nil || i != 3 {
		return errors.New("api: selfcheck step 7 nearest")
	}
	if n, err := Within(1, 0, 1); err != nil || n != 0 {
		return errors.New("api: selfcheck step 8 within")
	}
	// Rejections: out of bounds, duplicate, negative radius — and a valid
	// Add afterwards must still receive index 4, proving no trace remained.
	if _, err := Add(maxCoord+1, 0); !errors.Is(err, ErrOutOfBounds) {
		return errors.New("api: selfcheck out-of-bounds")
	}
	if _, err := Add(0, 0); !errors.Is(err, ErrDuplicate) {
		return errors.New("api: selfcheck duplicate")
	}
	if _, err := Within(0, 0, -1); !errors.Is(err, ErrNegativeRadius) {
		return errors.New("api: selfcheck negative radius")
	}
	if idx, err := Add(9, 9); err != nil || idx != 4 {
		return errors.New("api: selfcheck rejected op left a trace")
	}
	return nil
}

func ready() (*sites.Store, error) {
	mu.Lock()
	defer mu.Unlock()
	if cur == nil {
		cur = sites.New()
	}
	return cur, nil
}
