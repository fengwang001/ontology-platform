// Package api is the public face of the HRW key locator: node management,
// key ownership and a self-check. It depends only on cluster.
package api

import (
	"fmt"

	"ontology/cluster"
	"ontology/rnd"
)

// Re-exported sentinels: all failures stay decidable for callers.
var (
	ErrEmptyNodeID = cluster.ErrEmptyNodeID
	ErrDuplicate   = cluster.ErrDuplicate
	ErrNodeMissing = cluster.ErrNodeMissing
	ErrEmptyKey    = cluster.ErrEmptyKey
)

// Locator is an in-memory HRW key locator safe for concurrent use.
type Locator struct {
	c *cluster.Cluster
}

// New returns an empty locator using the deterministic rnd weight.
func New() *Locator {
	return &Locator{c: cluster.New()}
}

func (l *Locator) AddNode(id string) error    { return l.c.AddNode(id) }
func (l *Locator) RemoveNode(id string) error { return l.c.RemoveNode(id) }
func (l *Locator) Owner(key string) (string, error) {
	return l.c.Owner(key)
}

// naiveOwner scans every node, the independent specification of HRW.
func naiveOwner(key string, nodes []string) (string, uint64) {
	id, _ := rnd.Pick(key, nodes, rnd.Weight)
	return id, rnd.Weight(key, id)
}

// SelfCheck runs a built-in node/key script in a private cluster and
// verifies the four invariants: naive-scan equivalence, minimal
// relocation, determinism, and no-trace failures. It never mutates the
// receiver, so it is safe to call concurrently with Owner.
func (l *Locator) SelfCheck() error {
	c := cluster.New()
	for _, id := range []string{"n1", "n2", "n3", "n4"} {
		if err := c.AddNode(id); err != nil {
			return err
		}
	}
	keys := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		keys = append(keys, fmt.Sprintf("key-%02d", i))
	}
	owners := map[string]string{}
	for _, k := range keys {
		got, err := c.Owner(k)
		if err != nil {
			return err
		}
		want, _ := naiveOwner(k, c.Nodes()) // I1 + I3
		if got != want {
			return fmt.Errorf("selfcheck: owner %s=%s, naive=%s", k, got, want)
		}
		for j := 0; j < 5; j++ {
			again, _ := c.Owner(k)
			if again != got {
				return fmt.Errorf("selfcheck: nondeterministic owner for %s", k)
			}
		}
		owners[k] = got
	}

	// Add a node: only keys the newcomer wins may move (I2 add side).
	const nn = "n5"
	if err := c.AddNode(nn); err != nil {
		return err
	}
	for _, k := range keys {
		got, _ := c.Owner(k)
		want, _ := naiveOwner(k, c.Nodes())
		if got != want {
			return fmt.Errorf("selfcheck after add: %s=%s want %s", k, got, want)
		}
		if got != owners[k] && got != nn {
			return fmt.Errorf("selfcheck: key %s moved to %s, not newcomer", k, got)
		}
		owners[k] = got
	}

	// Rejected operations leave no trace (I4).
	snap := append([]string(nil), c.Nodes()...)
	bad := []func() error{
		func() error { return c.AddNode("") },
		func() error { return c.AddNode("n1") },
		func() error { return c.RemoveNode("ghost") },
	}
	for i, op := range bad {
		if err := op(); err == nil {
			return fmt.Errorf("selfcheck: bad op %d unexpectedly succeeded", i)
		}
		if ns := c.Nodes(); len(ns) != len(snap) {
			return fmt.Errorf("selfcheck: rejected op %d changed topology", i)
		}
		for _, k := range keys {
			got, _ := c.Owner(k)
			if got != owners[k] {
				return fmt.Errorf("selfcheck: rejected op %d moved key %s", i, k)
			}
		}
	}

	// Remove a node: only its former keys relocate (I2 remove side).
	const rm = "n2"
	if err := c.RemoveNode(rm); err != nil {
		return err
	}
	for _, k := range keys {
		got, _ := c.Owner(k)
		want, _ := naiveOwner(k, c.Nodes())
		if got != want {
			return fmt.Errorf("selfcheck after remove: %s=%s want %s", k, got, want)
		}
		if got != owners[k] && owners[k] != rm {
			return fmt.Errorf("selfcheck: key %s moved though %s stayed", k, owners[k])
		}
	}
	return nil
}
