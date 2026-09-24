// Package ver implements the per-key version chain: the committed latest
// version and latest-version selection, plus a transaction's pending set.
// It depends on nothing outside the standard library.
package ver

// Pending is one transaction's uncommitted write set: key -> value.
// Writes here are visible only to the owning transaction until commit.
type Pending map[string]string

// NewPending returns an empty pending set.
func NewPending() Pending { return make(Pending) }

// Set records (k, v) into the pending set, overwriting any earlier
// pending write of the same key by the same transaction.
func (p Pending) Set(k, v string) { p[k] = v }

// Get returns the pending value for k and whether it exists.
func (p Pending) Get(k string) (string, bool) {
	v, ok := p[k]
	return v, ok
}

// Chain is the committed version chain of a single key. Only the latest
// committed version (the one with the greatest commit number) is kept;
// older versions are discarded on commit, so reads are O(1).
type Chain struct {
	val string // latest committed value
	no  int64  // commit number of the latest committed version
	ok  bool   // whether any version has ever been committed
}

// Commit installs v as the latest committed version with commit number no.
// Because only the latest version is retained, this simply overwrites.
func (c *Chain) Commit(v string, no int64) {
	c.val, c.no, c.ok = v, no, true
}

// Latest returns the latest committed value and whether the key has ever
// been committed. Reading inspects exactly one version.
func (c *Chain) Latest() (string, bool) { return c.val, c.ok }
