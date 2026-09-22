package version

import "ontology/txid"

// Chain is one key's oldest-to-newest version list.
//
// A Chain is not internally synchronized: the store serializes access to a
// given key. Different keys are independent Chains, so a long transaction
// touching one key never blocks another key (the store holds no per-call lock
// across keys beyond short critical sections).
type Chain struct {
	versions []V
}

// NewChain returns an empty chain.
func NewChain() *Chain { return &Chain{} }

// Len returns the number of retained versions (including tombstones).
func (c *Chain) Len() int { return len(c.versions) }

// Append adds a version at the tip. maxLen <= 0 means unbounded. The payload
// is copied so later caller mutation cannot corrupt retained state.
func (c *Chain) Append(v V, maxLen int) (Outcome, error) {
	if maxLen > 0 && len(c.versions) >= maxLen {
		return Outcome{}, ErrChainLimit
	}
	out := Outcome{NewTip: v.Creator}
	if len(c.versions) > 0 {
		out.Shadowed = c.versions[len(c.versions)-1].Creator
	}
	stored := V{Creator: v.Creator, Deleted: v.Deleted}
	if !v.Deleted && v.Value != nil {
		stored.Value = append([]byte(nil), v.Value...)
	}
	c.versions = append(c.versions, stored)
	return out, nil
}

// Visible walks from the tip backwards and returns the first version the
// reader is allowed to see. An empty chain, or a chain whose newest visible
// version is a tombstone, yields a Result describing the distinction:
//
//	empty chain              -> Result{} (never existed)
//	tombstone newest visible -> {Found: true, Deleted: true}
//	normal version           -> {Found: true, Value: copy}
func (c *Chain) Visible(canSee VisibleBy) Result {
	for i := len(c.versions) - 1; i >= 0; i-- {
		v := c.versions[i]
		if !canSee(v.Creator) {
			continue
		}
		if v.Deleted {
			return Result{Found: true, Deleted: true}
		}
		return Result{Found: true, Value: append([]byte(nil), v.Value...)}
	}
	return Result{}
}

// Reclaim removes every version strictly older than the keeper (the newest
// version every reader can see). It returns the number removed and the
// creators removed so callers can maintain counters/candidate indexes.
// keeper == Zero reclaims nothing.
func (c *Chain) Reclaim(keeper txid.TxID) (removed []V) {
	if keeper == txid.Zero || len(c.versions) == 0 {
		return nil
	}
	keep := 0
	for i := range c.versions {
		if c.versions[i].Creator == keeper {
			keep = i
			break
		}
	}
	if keep == 0 {
		return nil
	}
	removed = append(removed, c.versions[:keep]...)
	c.versions = append([]V(nil), c.versions[keep:]...)
	return removed
}
