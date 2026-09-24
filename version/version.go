// Package version maintains the committed version chain of a single key.
package version

import (
	"sort"

	"ontology/txid"
)

// Version is one committed version of a key's value.
type Version struct {
	CommitTx txid.ID // transaction that committed this version
	Value    []byte
	Deleted  bool // tombstone: CommitTx deleted the key
}

// Chain holds the committed versions of one key, sorted by CommitTx.
// The zero value is ready to use.
type Chain struct{ versions []*Version }

// Insert adds v, keeping the chain sorted by CommitTx, and returns the
// position at which v was inserted.
func (c *Chain) Insert(v *Version) int {
	pos := sort.Search(len(c.versions), func(i int) bool {
		return c.versions[i].CommitTx > v.CommitTx
	})
	c.versions = append(c.versions, nil)
	copy(c.versions[pos+1:], c.versions[pos:])
	c.versions[pos] = v
	return pos
}

// Select returns the newest version whose CommitTx satisfies eligible,
// i.e. the version a reader with that eligibility predicate observes.
func (c *Chain) Select(eligible func(txid.ID) bool) (*Version, bool) {
	for i := len(c.versions) - 1; i >= 0; i-- {
		if eligible(c.versions[i].CommitTx) {
			return c.versions[i], true
		}
	}
	return nil, false
}

// Remove deletes the version committed by tx, if present.
func (c *Chain) Remove(tx txid.ID) {
	for i, v := range c.versions {
		if v.CommitTx == tx {
			c.versions = append(c.versions[:i], c.versions[i+1:]...)
			return
		}
	}
}

// Len returns the number of committed versions currently retained.
func (c *Chain) Len() int { return len(c.versions) }

// At returns the i-th version in CommitTx order.
func (c *Chain) At(i int) *Version { return c.versions[i] }

// Versions returns the versions in CommitTx order.
func (c *Chain) Versions() []*Version { return c.versions }
