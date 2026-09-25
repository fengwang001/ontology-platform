// Package api is the public facade over the affinity router: it creates the
// initial partition set and forwards operations, exposing judgeable sentinel
// errors from package router.
package api

import (
	"ontology/router"
)

// System is the partition-affinity routing system.
type System struct {
	r *router.Router
}

// New builds a system whose initial partitions are the given ids, all Active.
func New(partitionIDs ...int) *System {
	r := router.New()
	for _, id := range partitionIDs {
		_ = r.AddPartition(id) // ids are the initial, necessarily fresh set
	}
	return &System{r: r}
}

// Assign declares key's affinity partition; p must be Active and key new.
func (s *System) Assign(key string, p int) error { return s.r.Assign(key, p) }

// Put writes key's value; key must be assigned and its owner Active.
func (s *System) Put(key, val string) error { return s.r.Put(key, val) }

// Get reads key's value; reads on a Draining owner remain available.
func (s *System) Get(key string) (string, bool) { return s.r.Get(key) }

// BeginDrain moves p from Active to Draining.
func (s *System) BeginDrain(p int) error { return s.r.BeginDrain(p) }

// Migrate atomically moves all keys from Draining `from` to Active `to`.
func (s *System) Migrate(from, to int) error { return s.r.Migrate(from, to) }

// Count reports keys currently owned by p (Removed/unknown => 0).
func (s *System) Count(p int) int { return s.r.Count(p) }

// SelfCheck verifies all four invariants against a batch recount.
func (s *System) SelfCheck() error { return s.r.SelfCheck() }

// Re-exported sentinels so callers can judge errors without importing router.
var (
	ErrPartitionNotFound = router.ErrPartitionNotFound
	ErrAffinityConflict  = router.ErrAffinityConflict
	ErrNoAffinity        = router.ErrNoAffinity
	ErrInvalidMigrate    = router.ErrInvalidMigrate
)
