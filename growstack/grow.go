package growstack

// nextSize returns the smallest legal size >= need (current*GrowthMul^k),
// or 0 when MaxPerStack cannot satisfy it.
func (r *Runtime) nextSize(cur, need int) int {
	size := cur
	for size < need {
		if size > r.cfg.MaxPerStack/r.cfg.GrowthMul {
			return 0
		}
		size *= r.cfg.GrowthMul
	}
	if size > r.cfg.MaxPerStack {
		return 0
	}
	return size
}

// shrinkSize returns the greatest legal size >= BaseSize that is still
// strictly above the shrink threshold for the given usage, or 0 when no
// shrink is possible. "Legal" sizes are BaseSize*GrowthMul^k.
func (r *Runtime) shrinkSize(used int) int {
	size := r.cfg.BaseSize
	best := 0
	for {
		if size >= used && float64(used) < r.cfg.ShrinkRatio*float64(size) {
			best = size
		}
		if size > r.cfg.MaxPerStack/r.cfg.GrowthMul {
			return best
		}
		size *= r.cfg.GrowthMul
	}
}

// relocate copies the live prefix of c into a freshly allocated backing
// array and fixes every live intra-stack pointer.
//
// Costs (instrumented for the complexity proof):
//   - cell copy: O(used)
//   - pointer fixup: O(number of pointer-bearing cells), independent of the
//     total slot count and of every other coroutine.
//
// On allocator failure nothing is touched and the old stack stays intact.
func (r *Runtime) relocate(c *coroutine, newSize int) bool {
	r.allocAttempts++
	nb, ok := r.alloc.Alloc(newSize)
	if !ok {
		return false
	}
	copy(nb, c.base[:c.used])

	// Rebuild frame starts: frames remain contiguous and ordered; the live
	// prefix starts at zero in every relocation target.
	start := 0
	for i := range c.frames {
		c.frames[i].start = start
		start += c.frameSlots
	}

	// Fix only actual pointers. Dead (dangling) pointers are skipped: they
	// carry no resolvable address and never block a relocation.
	fixed := 0
	c.inbound = map[cellKey]map[*stackPointer]struct{}{}
	sources := make([]cellKey, 0, len(c.ptrs))
	for src := range c.ptrs {
		sources = append(sources, src)
	}
	for _, src := range sources {
		sp := c.ptrs[src]
		if sp.dead {
			continue
		}
		if _, _, ok := c.frameIndex(sp.frame); ok {
			// Re-issue the pointer at the source cell's new physical
			// location. The logical target (frame id + slot) is unchanged,
			// so dereference results before and after are identical.
			fresh := *sp
			nb[src.frameOffset(c)+src.slot] = cell{kind: cellPtr, pval: &fresh}
			// Rebuild the inbound index against the new object, otherwise
			// later dangling marking would miss the relocated pointer.
			c.registerPointer(&fresh)
			fixed++
		}
	}

	old := c.base
	c.base = nb
	r.lastRelocFixed = fixed
	r.lastRelocCopied = c.used
	r.alloc.Free(old)
	return true
}
