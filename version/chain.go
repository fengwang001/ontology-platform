package version

import "ontology/txid"

// Chain is one key's version list, oldest -> newest via prev pointers.
//
// The store serializes access to a chain, so Chain itself carries no lock.
type Chain struct {
	head *Version // newest
	len  int
}

// Len returns the number of versions currently attached to the chain.
func (c *Chain) Len() int { return c.len }

// Append adds a new pending version for writer. Pending nodes are never
// visible to any other reader. maxLen <= 0 means unlimited.
func (c *Chain) Append(writer txid.ID, kind Kind, value []byte, maxLen int) (*Version, bool) {
	if maxLen > 0 && c.len >= maxLen {
		return nil, false
	}
	v := &Version{
		Writer: writer,
		Kind:   kind,
		State:  StatePending,
		Commit: txid.Invalid,
		prev:   c.head,
	}
	if kind == KindValue {
		v.Value = append([]byte(nil), value...)
	}
	c.head = v
	c.len++
	return v, true
}

// Lookup walks newest -> oldest and returns the first version visible to vis.
// Pending versions are visible only to their own writer (read-your-writes);
// aborted versions are skipped by everyone.
func (c *Chain) Lookup(vis Visibility) Result {
	self := vis.SelfTxn()
	for v := c.head; v != nil; v = v.prev {
		switch v.State {
		case StateAborted:
			continue
		case StatePending:
			if self.Valid() && v.Writer == self {
				return present(v)
			}
		case StateCommitted:
			if v.Writer == self || vis.Visible(v.Commit) {
				return present(v)
			}
		}
	}
	return Result{Outcome: Absent}
}

func present(v *Version) Result {
	if v.Kind == KindDelete {
		return Result{Outcome: Deleted}
	}
	return Result{Outcome: Present, Value: append([]byte(nil), v.Value...)}
}

// Commit marks every pending version of writer as committed at commit.
func (c *Chain) Commit(writer, commit txid.ID) {
	for v := c.head; v != nil; v = v.prev {
		if v.State == StatePending && v.Writer == writer {
			v.State = StateCommitted
			v.Commit = commit
		}
	}
}

// Abort detaches all versions owned by writer (rollback leaves no residue
// that the reclaimer could ever mistake for visible data).
func (c *Chain) Abort(writer txid.ID) int {
	removed := 0
	for c.head != nil && c.head.State == StatePending && c.head.Writer == writer {
		c.head = c.head.prev
		c.len--
		removed++
	}
	var prev *Version
	for cur := c.head; cur != nil; cur = cur.prev {
		if cur.State == StatePending && cur.Writer == writer {
			prev.prev = cur.prev
			c.len--
			removed++
			continue
		}
		prev = cur
	}
	return removed
}

// Empty reports whether the chain has no attached versions.
func (c *Chain) Empty() bool { return c.len == 0 }

// CommittedCount returns the number of committed versions on the chain.
func (c *Chain) CommittedCount() int {
	n := 0
	for v := c.head; v != nil; v = v.prev {
		if v.State == StateCommitted {
			n++
		}
	}
	return n
}

// VisibleCount counts versions on the chain that vis can see (including
// tombstones and the reader's own pending writes).
func (c *Chain) VisibleCount(vis Visibility) int {
	n := 0
	self := vis.SelfTxn()
	for v := c.head; v != nil; v = v.prev {
		switch v.State {
		case StateCommitted:
			if v.Writer == self || vis.Visible(v.Commit) {
				n++
			}
			// Older committed versions have smaller commits; once this one
			// is below the point but merely shadowed, earlier ones may still
			// count, so continue (visibility is by id, not by walk stop).
		case StatePending:
			if self.Valid() && v.Writer == self {
				n++
			}
		}
	}
	return n
}
