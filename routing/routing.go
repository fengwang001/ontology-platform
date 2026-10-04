package routing

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrInvalid  = errors.New("routing: invalid argument")
	ErrNotFound = errors.New("routing: route not found")
	ErrConflict = errors.New("routing: route already defined")
)

type Route struct {
	ID   string
	N    int
	Back []int
	Insp []bool
	R    int
}

// Registry is the concurrency-safe table of defined routes.
type Registry struct {
	mu     sync.RWMutex
	routes map[string]*Route
}

func NewRegistry() *Registry { return &Registry{routes: map[string]*Route{}} }

func (r *Registry) Define(id string, n int, back []int, insp []bool, maxRework int) (*Route, error) {
	if id == "" || n < 1 || n > 32 || maxRework < 0 || maxRework > 1000 ||
		len(back) != n || len(insp) != n {
		return nil, ErrInvalid
	}
	for _, b := range back {
		if b < 1 || b > n {
			return nil, fmt.Errorf("%w: back out of range", ErrInvalid)
		}
	}
	// back[i] is the rework sink of operation i+1: it must not point past
	// the reporting operation itself, i.e. back[i] <= i+1 (1-based).
	for idx, b := range back {
		if b > idx+1 {
			return nil, fmt.Errorf("%w: back beyond operation", ErrInvalid)
		}
	}
	rt := &Route{
		ID:   id,
		N:    n,
		Back: append([]int(nil), back...),
		Insp: append([]bool(nil), insp...),
		R:    maxRework,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.routes[id]; ok {
		return nil, ErrConflict
	}
	r.routes[id] = rt
	return rt, nil
}

func (r *Registry) Get(id string) (*Route, error) {
	if id == "" {
		return nil, ErrInvalid
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.routes[id]
	if !ok {
		return nil, ErrNotFound
	}
	return rt, nil
}

var std = NewRegistry()

// Std returns the process-wide registry backing the package-level Define/Get.
func Std() *Registry { return std }

func Define(id string, n int, back []int, insp []bool, maxRework int) (*Route, error) {
	return std.Define(id, n, back, insp, maxRework)
}

func Get(id string) (*Route, error) { return std.Get(id) }
