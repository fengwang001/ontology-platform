// Package cluster manages n orset replicas: routing by id, pairwise merge,
// full sync and per-replica snapshots. A single mutex serializes everything,
// so concurrent Merge(x,y) and Merge(y,x) cannot deadlock.
package cluster

import (
	"sync"

	"ontology/orset"
)

type Cluster struct {
	mu   sync.Mutex
	reps []*orset.State
}

func New(n, maxTags int) (*Cluster, error) {
	if n <= 0 || maxTags <= 0 {
		return nil, orset.ErrInvalidArgument
	}
	c := &Cluster{reps: make([]*orset.State, n)}
	for i := range c.reps {
		s, err := orset.New(i, maxTags)
		if err != nil {
			return nil, err
		}
		c.reps[i] = s
	}
	return c, nil
}

func (c *Cluster) rep(r int) (*orset.State, error) {
	if r < 0 || r >= len(c.reps) {
		return nil, orset.ErrInvalidArgument
	}
	return c.reps[r], nil
}

func (c *Cluster) Add(r int, e string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.rep(r)
	if err != nil {
		return err
	}
	_, err = s.Add(e)
	return err
}

func (c *Cluster) Remove(r int, e string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.rep(r)
	if err != nil {
		return err
	}
	return s.Remove(e)
}

func (c *Cluster) Contains(r int, e string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.rep(r)
	if err != nil {
		return false, err
	}
	if e == "" {
		return false, orset.ErrEmptyElement
	}
	return s.Contains(e), nil
}

// Merge folds src into dst only; dst == src is legal and a no-op.
func (c *Cluster) Merge(dst, src int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, err := c.rep(dst)
	if err != nil {
		return err
	}
	s, err := c.rep(src)
	if err != nil {
		return err
	}
	if dst == src {
		return nil
	}
	return d.Merge(s)
}

// SyncAll merges every replica into every other; afterwards all replicas
// hold the union of all add records and tombstones.
func (c *Cluster) SyncAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.reps {
		for j := range c.reps {
			if i != j {
				if err := c.reps[i].Merge(c.reps[j]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Elements snapshots replica r: each present element with its live tags.
func (c *Cluster) Elements(r int) (map[string][]orset.Tag, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.rep(r)
	if err != nil {
		return nil, err
	}
	return s.Elements(), nil
}
