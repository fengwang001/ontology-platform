package version

import "ontology/txid"

// PruneReport counts what one pruning pass did.
type PruneReport struct {
	Examined int // chain nodes actually visited
	Removed  int // committed versions detached
}

// PruneBelow detaches versions that are provably invisible to every snapshot
// whose point is at or beyond watermark.
//
// A committed node v may be removed iff:
//  1. v.Commit < watermark, and
//  2. a strictly newer committed node u exists with u.Commit < watermark,
//     i.e. v is completely shadowed by a version every such snapshot sees.
//
// Pending nodes (active writers) are never touched, and they do not count as
// shadowing versions. The newest committed node below watermark is always
// retained as the common floor.
func (c *Chain) PruneBelow(watermark txid.ID) PruneReport {
	var rep PruneReport
	// First pass (newest -> oldest): mark doomed nodes.
	seenFloor := false
	for v := c.head; v != nil; v = v.prev {
		rep.Examined++
		if v.State == StateCommitted && v.Commit.Less(watermark) {
			if seenFloor {
				v.doomed = true
				rep.Removed++
			} else {
				seenFloor = true
			}
		}
	}
	if rep.Removed == 0 {
		return rep
	}
	// Second pass: unlink marked nodes.
	for c.head != nil && c.head.doomed {
		c.head = c.head.prev
		c.len--
	}
	var prev *Version
	for cur := c.head; cur != nil; cur = cur.prev {
		if cur.doomed {
			prev.prev = cur.prev
			c.len--
			continue
		}
		prev = cur
	}
	return rep
}

// HasCommittedBelow reports whether the chain contains any committed version
// with commit < watermark; the store uses it to skip pointless pruning.
func (c *Chain) HasCommittedBelow(watermark txid.ID) bool {
	for v := c.head; v != nil; v = v.prev {
		if v.State == StateCommitted && v.Commit.Less(watermark) {
			return true
		}
	}
	return false
}
