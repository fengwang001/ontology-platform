// Package pip implements the priority-inheritance core: holder, FIFO wait
// queue, and effective-priority computation (inheritance and handoff).
// It depends on no other package in this module.
package pip

import "container/heap"

// waiter is one queued task: its id and static priority.
type waiter struct{ id, prio int }

// waitHeap is a max-heap of waiters ordered by static priority.
type waitHeap []waiter

func (h waitHeap) Len() int           { return len(h) }
func (h waitHeap) Less(i, j int) bool { return h[i].prio > h[j].prio }
func (h waitHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *waitHeap) Push(x any)        { *h = append(*h, x.(waiter)) }
func (h *waitHeap) Pop() any {
	old := *h
	n := len(old)
	w := old[n-1]
	*h = old[:n-1]
	return w
}

// Core holds the lock state. Not goroutine-safe; callers serialize.
type Core struct {
	hasHolder  bool
	holder     int
	holderPrio int         // static priority of the holder, never overwritten
	fifo       []int       // waiter ids in arrival order
	prios      map[int]int // waiter id -> static priority
	top        waitHeap    // max-heap over waiters by static priority
	lastChecks int         // waiters examined by the last locateMax call
}

// NewCore returns an idle core.
func NewCore() *Core { return &Core{prios: make(map[int]int)} }

// locateMax returns the highest-static-priority waiter. A max-heap makes
// this O(1): only the root is examined, recorded in lastChecks.
func (c *Core) locateMax() (waiter, bool) {
	c.lastChecks = 0
	if len(c.top) == 0 {
		return waiter{}, false
	}
	c.lastChecks = 1
	return c.top[0], true
}

// Acquire takes the lock if idle, otherwise enqueues the task (FIFO) and
// triggers inheritance: the holder's effective priority is recomputed
// against the highest-priority waiter.
func (c *Core) Acquire(id, prio int) {
	if !c.hasHolder {
		c.hasHolder, c.holder, c.holderPrio = true, id, prio
		return
	}
	c.fifo = append(c.fifo, id)
	c.prios[id] = prio
	heap.Push(&c.top, waiter{id, prio})
	c.locateMax()
}

// Release hands the lock to the highest-static-priority waiter, or to
// nobody when the queue is empty.
func (c *Core) Release() {
	w, ok := c.locateMax()
	if !ok {
		c.hasHolder, c.holder, c.holderPrio = false, 0, 0
		return
	}
	heap.Pop(&c.top) // removes the root, which is w
	for i, id := range c.fifo {
		if id == w.id {
			c.fifo = append(c.fifo[:i], c.fifo[i+1:]...)
			break
		}
	}
	delete(c.prios, w.id)
	c.holder, c.holderPrio = w.id, w.prio
}

// Holder reports the current holder.
func (c *Core) Holder() (int, bool) { return c.holder, c.hasHolder }

// IsHolder reports whether id currently holds the lock.
func (c *Core) IsHolder(id int) bool { return c.hasHolder && c.holder == id }

// Waiting reports whether id is in the wait queue.
func (c *Core) Waiting(id int) bool { _, ok := c.prios[id]; return ok }

// Effective is max(holder static, max over waiters' static), or 0 if idle.
// The heap root gives the waiters' maximum in O(1), matching a naive scan.
func (c *Core) Effective() int {
	if !c.hasHolder {
		return 0
	}
	eff := c.holderPrio
	if len(c.top) > 0 && c.top[0].prio > eff {
		eff = c.top[0].prio
	}
	return eff
}
