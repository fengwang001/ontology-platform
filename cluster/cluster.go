// Package cluster manages n LWW replicas addressed by id.
package cluster

import (
	"errors"

	"ontology/lww"
)

var ErrParam = errors.New("cluster: invalid parameter (n, maxElems or replica id)")

type Cluster struct {
	n    int
	reps []*lww.State
}

func New(n, maxElems int) (*Cluster, error) {
	if n <= 0 || maxElems <= 0 {
		return nil, ErrParam
	}
	c := &Cluster{n: n, reps: make([]*lww.State, n)}
	for i := range c.reps {
		c.reps[i] = lww.New(maxElems)
	}
	return c, nil
}

func (c *Cluster) N() int { return c.n }

// Replica returns the state of replica i, or ErrParam for an out-of-range id.
func (c *Cluster) Replica(i int) (*lww.State, error) {
	if i < 0 || i >= c.n {
		return nil, ErrParam
	}
	return c.reps[i], nil
}

// Merge folds replica src into replica dst.
func (c *Cluster) Merge(dst, src int) error {
	if dst < 0 || dst >= c.n || src < 0 || src >= c.n {
		return ErrParam
	}
	return lww.Merge(c.reps[dst], c.reps[src])
}

// SyncAll merges every replica into every other replica; afterwards all
// replicas hold the union of all records.
func (c *Cluster) SyncAll() error {
	for i := 0; i < c.n; i++ {
		for j := 0; j < c.n; j++ {
			if i != j {
				if err := c.Merge(i, j); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
