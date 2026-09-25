// Package api is the public facade of the HRW key locator. It depends only
// on cluster.
package api

import "ontology/cluster"

// Locator is the concurrency-safe HRW key locator.
type Locator struct {
	c *cluster.Cluster
}

// New returns an empty locator.
func New() *Locator {
	return &Locator{c: cluster.New()}
}

// AddNode joins a node, migrating only the keys it strictly outranks.
func (l *Locator) AddNode(id string) error { return l.c.AddNode(id) }

// RemoveNode removes a node, relocating only the keys it owned.
func (l *Locator) RemoveNode(id string) error { return l.c.RemoveNode(id) }

// Owner returns the unique node that owns key.
func (l *Locator) Owner(key string) (string, error) { return l.c.Owner(key) }

// SelfCheck verifies the invariants on a built-in scenario.
func (l *Locator) SelfCheck() error { return l.c.SelfCheck() }
