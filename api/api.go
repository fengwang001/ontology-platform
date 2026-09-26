// Package api is the public entry point to the Counting Bloom Filter. It
// depends only on cbf; the dependency direction is api -> cbf -> ch.
package api

import "ontology/cbf"

// Re-exported sentinel errors: callers decide failures with errors.Is.
var (
	ErrInvalidParams = cbf.ErrInvalidParams // m <= 0 or k <= 0
	ErrInvalidKey    = cbf.ErrInvalidKey    // key < 0
	ErrNotPresent    = cbf.ErrNotPresent    // Remove with a zero counter
)

// Filter is the in-memory counting bloom filter handle.
type Filter struct{ inner *cbf.Filter }

// New allocates m counters driven by k hash functions.
func New(m, k int) (*Filter, error) {
	f, err := cbf.New(m, k)
	if err != nil {
		return nil, err
	}
	return &Filter{inner: f}, nil
}

// Add increments the k counters of key.
func (f *Filter) Add(key int64) error { return f.inner.Add(key) }

// Remove decrements the k counters of key only when all are >= 1.
func (f *Filter) Remove(key int64) error { return f.inner.Remove(key) }

// Query returns the per-key count lower bound (min over k counters).
func (f *Filter) Query(key int64) (int64, error) { return f.inner.Query(key) }

// SelfCheck verifies the four invariants on a built-in sequence.
func (f *Filter) SelfCheck() bool { return f.inner.SelfCheck() }
