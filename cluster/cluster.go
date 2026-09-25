// Package cluster holds nodes, key ownership and the reverse index
// node -> owned keys, performing AddNode/RemoveNode with minimal move.
package cluster

import (
	"errors"
	"sort"
	"sync"

	"ontology/rnd"
)

// Sentinel errors are pairwise distinct so every failure is decidable.
var (
	ErrEmptyNodeID = errors.New("cluster: node id must not be empty")
	ErrDuplicate   = errors.New("cluster: node id already exists")
	ErrNodeMissing = errors.New("cluster: no such node")
	ErrEmptyKey    = errors.New("cluster: key must not be empty")
)

// Cluster is the in-memory HRW topology; use New. The optional weight
// overrides w(key, node) so tests can inject a fixed table.
type Cluster struct {
	mu    sync.RWMutex
	nodes map[string]struct{}
	owner map[string]string // key -> owning node
	bestw map[string]uint64 // key -> current winning weight
	owned map[string]map[string]struct{}
	// lastRemoveScanned counts keys examined for relocation during the
	// most recent RemoveNode. It is unexported and no method returns it.
	lastRemoveScanned int
	weight            func(key, node string) uint64
}

// New creates an empty cluster; the optional arg replaces the weight.
func New(weight ...func(key, node string) uint64) *Cluster {
	w := rnd.Weight
	if len(weight) > 0 && weight[0] != nil {
		w = weight[0]
	}
	return &Cluster{
		nodes: map[string]struct{}{}, owner: map[string]string{},
		bestw: map[string]uint64{}, owned: map[string]map[string]struct{}{}, weight: w,
	}
}

func (c *Cluster) sliceNodes() []string {
	ns := make([]string, 0, len(c.nodes))
	for id := range c.nodes {
		ns = append(ns, id)
	}
	sort.Strings(ns)
	return ns
}

// AddNode registers a node: only keys the newcomer wins move (equal
// weight goes to the smaller ID). Validation precedes every mutation, so
// rejected calls leave no trace.
func (c *Cluster) AddNode(id string) error {
	if id == "" {
		return ErrEmptyNodeID
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.nodes[id]; ok {
		return ErrDuplicate
	}
	c.nodes[id] = struct{}{}
	c.owned[id] = map[string]struct{}{}
	for k, cur := range c.owner {
		if wn := c.weight(k, id); rnd.Prefer(wn, id, c.bestw[k], cur) {
			delete(c.owned[cur], k)
			c.owned[id][k], c.owner[k], c.bestw[k] = struct{}{}, id, wn
		}
	}
	return nil
}

// RemoveNode deletes a node and relocates only the keys it owned by
// re-running HRW over the remaining nodes. lastRemoveScanned equals that
// owned-key count and is therefore independent of the total key count.
func (c *Cluster) RemoveNode(id string) error {
	if id == "" {
		return ErrNodeMissing
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.nodes[id]; !ok {
		return ErrNodeMissing
	}
	c.lastRemoveScanned = len(c.owned[id])
	keys := c.owned[id]
	delete(c.nodes, id)
	delete(c.owned, id)
	for k := range keys {
		delete(c.owner, k)
		delete(c.bestw, k)
		win, ok := rnd.Pick(k, c.sliceNodes(), c.weight)
		if !ok {
			continue // no nodes remain; next Owner locates the key again
		}
		c.owner[k], c.bestw[k] = win, c.weight(k, win)
		c.owned[win][k] = struct{}{}
	}
	return nil
}

// Owner returns the HRW owner of key; unseen keys are located once and
// memoized. The read path takes only a read lock.
func (c *Cluster) Owner(key string) (string, error) {
	if key == "" {
		return "", ErrEmptyKey
	}
	c.mu.RLock()
	if n, ok := c.owner[key]; ok {
		c.mu.RUnlock()
		return n, nil
	}
	c.mu.RUnlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.owner[key]; ok {
		return n, nil
	}
	if len(c.nodes) == 0 {
		return "", ErrNodeMissing
	}
	win, _ := rnd.Pick(key, c.sliceNodes(), c.weight)
	c.owner[key], c.bestw[key] = win, c.weight(key, win)
	c.owned[win][key] = struct{}{}
	return win, nil
}

// Nodes and Keys return sorted snapshots for checks and tests.
func (c *Cluster) Nodes() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sliceNodes()
}

func (c *Cluster) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ks := make([]string, 0, len(c.owner))
	for k := range c.owner {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
