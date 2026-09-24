// Package cluster manages n OR-Set replicas addressed by index 0..n-1.
package cluster

import "ontology/orset"

// Cluster routes operations to per-index replicas and merges between them.
type Cluster struct {
	sets []*orset.Set
}

// New creates n replicas, each with add-record capacity maxTags.
func New(n, maxTags int) (*Cluster, error) {
	if n <= 0 || maxTags <= 0 {
		return nil, orset.ErrParam
	}
	c := &Cluster{sets: make([]*orset.Set, n)}
	for i := range c.sets {
		c.sets[i] = orset.New(i, maxTags)
	}
	return c, nil
}

func (c *Cluster) set(r int) (*orset.Set, error) {
	if r < 0 || r >= len(c.sets) {
		return nil, orset.ErrParam
	}
	return c.sets[r], nil
}

// Add appends a fresh tag for e on replica r.
func (c *Cluster) Add(r int, e string) error {
	s, err := c.set(r)
	if err != nil {
		return err
	}
	return s.Add(e)
}

// Remove tombstones e's live tags on replica r.
func (c *Cluster) Remove(r int, e string) error {
	s, err := c.set(r)
	if err != nil {
		return err
	}
	return s.Remove(e)
}

// Contains reports whether e is live on replica r.
func (c *Cluster) Contains(r int, e string) (bool, error) {
	s, err := c.set(r)
	if err != nil {
		return false, err
	}
	return s.Contains(e), nil
}

// Elements returns replica r's live elements with their live tags.
func (c *Cluster) Elements(r int) (map[string][]orset.Tag, error) {
	s, err := c.set(r)
	if err != nil {
		return nil, err
	}
	st := s.Snapshot()
	out := map[string][]orset.Tag{}
	for e, ts := range st.Adds {
		var live []orset.Tag
		for _, t := range ts {
			if _, dead := st.Tombs[t]; !dead {
				live = append(live, t)
			}
		}
		if len(live) > 0 {
			out[e] = live
		}
	}
	return out, nil
}

// Snapshot returns a deep copy of replica r's full state.
func (c *Cluster) Snapshot(r int) (orset.State, error) {
	s, err := c.set(r)
	if err != nil {
		return orset.State{}, err
	}
	return s.Snapshot(), nil
}

// Merge unions replica src's state into replica dst (dst only).
func (c *Cluster) Merge(dst, src int) error {
	d, err := c.set(dst)
	if err != nil {
		return err
	}
	s, err := c.set(src)
	if err != nil {
		return err
	}
	return d.MergeFrom(s)
}

// SyncAll merges every replica into every other replica, converging all.
func (c *Cluster) SyncAll() error {
	for i := range c.sets {
		for j := range c.sets {
			if i != j {
				if err := c.sets[i].MergeFrom(c.sets[j]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
