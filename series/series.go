// Package series keeps one ewma.EWMA per string key. It only performs
// keyed access; parameter validation and concurrency control belong to
// the api package that owns it.
package series

import "ontology/ewma"

// Registry maps keys to their independent EWMA instances.
type Registry struct {
	alpha       float64
	seed        float64
	biasCorrect bool
	items       map[string]*ewma.EWMA
}

// New creates an empty Registry. Every key created through it uses the
// given alpha, seed and bias-correction setting. An invalid alpha yields
// ewma.ErrInvalidAlpha.
func New(alpha, seed float64, biasCorrect bool) (*Registry, error) {
	if _, err := ewma.New(alpha, seed, biasCorrect); err != nil {
		return nil, err
	}
	return &Registry{
		alpha:       alpha,
		seed:        seed,
		biasCorrect: biasCorrect,
		items:       make(map[string]*ewma.EWMA),
	}, nil
}

// Get returns the EWMA for key and true, or nil and false when the key
// has never been updated.
func (r *Registry) Get(key string) (*ewma.EWMA, bool) {
	e, ok := r.items[key]
	return e, ok
}

// GetOrCreate returns the existing EWMA for key or inserts a fresh one
// (mean at seed, count 0) and returns that.
func (r *Registry) GetOrCreate(key string) *ewma.EWMA {
	if e, ok := r.items[key]; ok {
		return e
	}
	// ewma.New cannot fail here: Registry construction already proved
	// alpha is inside (0,1); discard the error by construction.
	e, _ := ewma.New(r.alpha, r.seed, r.biasCorrect)
	r.items[key] = e
	return e
}
