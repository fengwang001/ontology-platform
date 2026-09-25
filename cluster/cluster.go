// Package cluster maintains the node set, key ownership and reverse index
// (node -> owned keys) and performs AddNode/RemoveNode with minimal movement.
package cluster

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/rnd"
)

// Sentinel errors are decidable and mutually distinct.
var (
	ErrEmptyNodeID   = errors.New("cluster: empty node id")
	ErrDuplicateNode = errors.New("cluster: duplicate node id")
	ErrNodeNotFound  = errors.New("cluster: node not found")
	ErrEmptyKey      = errors.New("cluster: empty key")
)

// Cluster is the concurrency-safe in-memory node/key topology.
type Cluster struct {
	mu    sync.RWMutex
	nodes map[string]struct{}
	owner map[string]string              // registered key -> owner
	owned map[string]map[string]struct{} // reverse index: node -> owned keys

	// lastRemoveScanned counts keys examined during the most recent RemoveNode; unexported, never in the public API.
	lastRemoveScanned int
}

// New returns an empty cluster.
func New() *Cluster {
	return &Cluster{nodes: map[string]struct{}{}, owner: map[string]string{}, owned: map[string]map[string]struct{}{}}
}

func (c *Cluster) nodesLocked(skip string) []string {
	ns := make([]string, 0, len(c.nodes))
	for n := range c.nodes {
		if n != skip {
			ns = append(ns, n)
		}
	}
	sort.Strings(ns) // tie-break is ID-based, so order is otherwise irrelevant
	return ns
}

// AddNode joins a node: only keys whose new weight is strictly greater
// migrate. Validation precedes every mutation, so rejection leaves no trace.
func (c *Cluster) AddNode(id string) error {
	if id == "" {
		return ErrEmptyNodeID
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.nodes[id]; ok {
		return ErrDuplicateNode
	}
	c.nodes[id], c.owned[id] = struct{}{}, map[string]struct{}{}
	for k, old := range c.owner {
		if rnd.Weight(k, id) > rnd.Weight(k, old) { // strict: a tie does not move
			delete(c.owned[old], k)
			c.owned[id][k], c.owner[k] = struct{}{}, id
		}
	}
	return nil
}

// RemoveNode deletes a node: via the reverse index only the keys it owned
// are examined, so cost tracks that node's key count rather than all keys.
func (c *Cluster) RemoveNode(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.nodes[id]; !ok {
		return ErrNodeNotFound
	}
	c.lastRemoveScanned = 0
	rest := c.nodesLocked(id)
	for k := range c.owned[id] {
		c.lastRemoveScanned++
		delete(c.owner, k)
		if nb, _, ok := rnd.Best(k, rest); ok {
			c.owner[k], c.owned[nb][k] = nb, struct{}{}
		}
	}
	delete(c.nodes, id)
	delete(c.owned, id)
	return nil
}

// Owner returns the owner of key; an unseen key is registered on first use
// by the same max-weight/smallest-ID rule.
func (c *Cluster) Owner(key string) (string, error) {
	if key == "" {
		return "", ErrEmptyKey
	}
	c.mu.RLock()
	if o, ok := c.owner[key]; ok {
		c.mu.RUnlock()
		return o, nil
	}
	nodes := c.nodesLocked("")
	c.mu.RUnlock()
	nb, _, ok := rnd.Best(key, nodes)
	if !ok {
		return "", ErrNodeNotFound // no node yet
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if o, ok := c.owner[key]; ok { // another goroutine registered it first
		return o, nil
	}
	c.owner[key], c.owned[nb][key] = nb, struct{}{}
	return nb, nil
}

// SelfCheck verifies the four invariants on a built-in scenario.
func (c *Cluster) SelfCheck() error {
	s := New()
	s.AddNode("n1")
	s.AddNode("n2")
	s.AddNode("n3")
	ks := []string{"a", "b", "c", "d", "e", "f"}
	prev := map[string]string{}
	for _, k := range ks { // inv.1 naive scan; inv.3 repeatability
		o, _ := s.Owner(k)
		b, _, _ := rnd.Best(k, []string{"n1", "n2", "n3"})
		x, _ := s.Owner(k)
		if o != b || x != o {
			return fmt.Errorf("invariant 1/3 violated for %s", k)
		}
		prev[k] = o
	}
	s.AddNode("n4")
	for _, k := range ks { // inv.2 on add: move iff new weight strictly greater
		o, _ := s.Owner(k)
		if (o == "n4") != (rnd.Weight(k, "n4") > rnd.Weight(k, prev[k])) {
			return fmt.Errorf("invariant 2 on add: %s", k)
		}
		prev[k] = o
	}
	s.RemoveNode("n1")
	for _, k := range ks { // inv.2 on remove: only n1-owned keys may move
		if o, _ := s.Owner(k); prev[k] != "n1" && o != prev[k] {
			return fmt.Errorf("invariant 2 on remove: %s", k)
		}
	}
	return nil
}
