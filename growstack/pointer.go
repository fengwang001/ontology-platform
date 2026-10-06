package growstack

// Pointer tracking: live-pointer set per coroutine, inbound index per
// coroutine/frame/slot, and dangling marking on pop.

// registerPointer indexes a pointer value stored in one of this coroutine's
// cells. Cost is O(1); only pointer-bearing cells are indexed.
func (c *coroutine) registerPointer(sp *stackPointer) {
	src := cellKey{sp.srcFrame, sp.srcSlot}
	c.ptrs[src] = sp
	dst := cellKey{sp.frame, sp.slot}
	set := c.inbound[dst]
	if set == nil {
		set = map[*stackPointer]struct{}{}
		c.inbound[dst] = set
	}
	set[sp] = struct{}{}
}

// dropCellPointer removes tracking for a pointer value being overwritten.
func (c *coroutine) dropCellPointer(k cellKey) {
	sp := c.ptrs[k]
	if sp == nil {
		return
	}
	delete(c.ptrs, k)
	dst := cellKey{sp.frame, sp.slot}
	if set := c.inbound[dst]; set != nil {
		delete(set, sp)
		if len(set) == 0 {
			delete(c.inbound, dst)
		}
	}
}

// dropFrameCells removes the pointer bookkeeping for cells that disappear
// with a popped frame (the popped cells themselves and their inbound links).
func (c *coroutine) dropFrameCells(frameID int64) {
	for slot := 0; slot < c.frameSlots; slot++ {
		c.dropCellPointer(cellKey{frameID, slot})
	}
}

// markFrameDangling invalidates every live pointer whose target is the
// popped frame. Dangling pointers stay indexed (they remain cell contents)
// and must neither block shrinkage nor be fixed during relocation.
func (c *coroutine) markFrameDangling(frameID int64) {
	for slot := 0; slot < c.frameSlots; slot++ {
		set := c.inbound[cellKey{frameID, slot}]
		for sp := range set {
			sp.dead = true
			delete(set, sp)
		}
		delete(c.inbound, cellKey{frameID, slot})
	}
}

// frameIndex returns the frame's index in the stack and its descriptor.
func (c *coroutine) frameIndex(frameID int64) (int, *frameInfo, bool) {
	for i := range c.frames {
		if c.frames[i].id == frameID {
			return i, &c.frames[i], true
		}
	}
	return 0, nil, false
}

// targetCell resolves a pointer to its physical cell. Dead pointers have no
// resolvable cell.
func (c *coroutine) targetCell(sp *stackPointer) (*cell, bool) {
	if sp.dead {
		return nil, false
	}
	_, fi, ok := c.frameIndex(sp.frame)
	if !ok || sp.slot < 0 || sp.slot >= c.frameSlots {
		sp.dead = true
		return nil, false
	}
	return &c.base[fi.start+sp.slot], true
}
