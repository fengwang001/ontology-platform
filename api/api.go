// Package api is the outward-facing surface of the reference-count
// tracker. It delegates to reg and re-exports its sentinel errors.
package api

import "ontology/reg"

// Sentinel errors re-exported so callers only import api.
var (
	ErrEmpty    = reg.ErrEmpty
	ErrExists   = reg.ErrExists
	ErrNotFound = reg.ErrNotFound
	ErrNotHeld  = reg.ErrNotHeld
)

// Service is a thread-safe reference-count tracker.
type Service struct {
	r *reg.Registry
}

// New returns an empty Service.
func New() *Service {
	return &Service{r: reg.New()}
}

// Create registers id with reference count 0.
func (s *Service) Create(id string) error { return s.r.Create(id) }

// Acquire makes ref hold id (idempotent per pair).
func (s *Service) Acquire(ref, id string) error { return s.r.Acquire(ref, id) }

// Release drops ref's hold; reclaims id at count 0.
func (s *Service) Release(ref, id string) error { return s.r.Release(ref, id) }

// RefCount returns how many distinct referrers hold id.
func (s *Service) RefCount(id string) (int, error) { return s.r.RefCount(id) }

// Alive reports whether id exists and is not reclaimed.
func (s *Service) Alive(id string) bool { return s.r.Alive(id) }

// SelfCheck replays built-in sequences verifying all invariants.
func (s *Service) SelfCheck() error { return s.r.SelfCheck() }
