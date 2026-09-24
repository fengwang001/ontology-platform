// Package lc holds the single-node Lamport clock rules and the
// (timestamp, nodeID) total-order comparison. It depends on no other
// package in this module.
package lc

// Clock is one node's integer Lamport clock L. The zero value is L=0.
type Clock struct {
	L int64
}

// Tick advances the clock for a local event or a send event: L = L+1.
// It returns the new value, which is the event timestamp.
func (c *Clock) Tick() int64 {
	c.L++
	return c.L
}

// Recv advances the clock for a receive event carrying timestamp tMsg:
// L = max(L, tMsg) + 1. It returns the new value (the event timestamp).
func (c *Clock) Recv(tMsg int64) int64 {
	if tMsg > c.L {
		c.L = tMsg
	}
	c.L++
	return c.L
}

// Less reports whether (ts1, node1) precedes (ts2, node2) in the total
// order: timestamps are compared first; equal timestamps are ordered by
// the smaller node ID. Execution order is intentionally irrelevant.
func Less(ts1 int64, node1 int, ts2 int64, node2 int) bool {
	if ts1 != ts2 {
		return ts1 < ts2
	}
	return node1 < node2
}
