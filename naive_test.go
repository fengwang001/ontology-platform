package clock

type naiveFrame struct {
	page       int
	occupied   bool
	referenced bool
	dirty      bool
}

type naiveEviction struct {
	frame        int
	page         int
	dirty        bool
	writeBacks   int
	selectedPass string
}

type naiveAccessResult struct {
	page       int
	write      bool
	hit        bool
	frame      int
	eviction   *naiveEviction
	writeBacks int
}

type naiveFlushResult struct {
	page       int
	frame      int
	writeBacks int
}

type naiveSnapshot struct {
	frames     []naiveFrame
	pointer    int
	writeBacks int
}

type naiveClock struct {
	frames     []naiveFrame
	pointer    int
	writeBacks int
}

func newNaive(n int) *naiveClock {
	return &naiveClock{frames: make([]naiveFrame, n)}
}

func (c *naiveClock) access(page int, write bool) (naiveAccessResult, error) {
	if page < 0 {
		return naiveAccessResult{}, ErrNegativePage
	}

	result := naiveAccessResult{
		page:       page,
		write:      write,
		writeBacks: c.writeBacks,
	}

	for frame := range c.frames {
		if c.frames[frame].occupied && c.frames[frame].page == page {
			result.hit = true
			result.frame = frame
			c.frames[frame].referenced = true
			if write {
				c.frames[frame].dirty = true
			}
			result.writeBacks = c.writeBacks
			return result, nil
		}
	}

	for frame := range c.frames {
		if !c.frames[frame].occupied {
			c.frames[frame] = naiveFrame{
				page:       page,
				occupied:   true,
				referenced: true,
				dirty:      write,
			}
			result.frame = frame
			result.writeBacks = c.writeBacks
			return result, nil
		}
	}

	eviction := c.selectVictim()
	c.frames[eviction.frame] = naiveFrame{
		page:       page,
		occupied:   true,
		referenced: true,
		dirty:      write,
	}
	c.pointer = (eviction.frame + 1) % len(c.frames)

	result.frame = eviction.frame
	result.eviction = &eviction
	result.writeBacks = c.writeBacks
	return result, nil
}

func (c *naiveClock) flush(page int) (naiveFlushResult, error) {
	if page < 0 {
		return naiveFlushResult{}, ErrNegativePage
	}

	for frame := range c.frames {
		if c.frames[frame].occupied && c.frames[frame].page == page {
			if !c.frames[frame].dirty {
				return naiveFlushResult{}, ErrPageNotDirty
			}

			c.frames[frame].dirty = false
			c.writeBacks++
			return naiveFlushResult{
				page:       page,
				frame:      frame,
				writeBacks: c.writeBacks,
			}, nil
		}
	}

	return naiveFlushResult{}, ErrPageNotResident
}

func (c *naiveClock) snapshot() naiveSnapshot {
	frames := append([]naiveFrame(nil), c.frames...)
	return naiveSnapshot{
		frames:     frames,
		pointer:    c.pointer,
		writeBacks: c.writeBacks,
	}
}

func (c *naiveClock) selectVictim() naiveEviction {
	for {
		for offset := 0; offset < len(c.frames); offset++ {
			frame := (c.pointer + offset) % len(c.frames)
			if !c.frames[frame].referenced && !c.frames[frame].dirty {
				return c.removeVictim(frame, "A")
			}
		}

		for offset := 0; offset < len(c.frames); offset++ {
			frame := (c.pointer + offset) % len(c.frames)
			if !c.frames[frame].referenced && c.frames[frame].dirty {
				return c.removeVictim(frame, "B")
			}
			if c.frames[frame].referenced {
				c.frames[frame].referenced = false
			}
		}
	}
}

func (c *naiveClock) removeVictim(frame int, selectedPass string) naiveEviction {
	victim := c.frames[frame]
	if victim.dirty {
		c.writeBacks++
	}

	return naiveEviction{
		frame:        frame,
		page:         victim.page,
		dirty:        victim.dirty,
		writeBacks:   c.writeBacks,
		selectedPass: selectedPass,
	}
}
