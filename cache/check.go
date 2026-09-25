package cache

import (
	"fmt"
	"math/rand"
)

type Stats struct {
	Size, PerSlab, SlabSize, Waste int
	Slabs, Allocated               int
	Full, Partial, Empty           int
}

func (c *Cache) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	st := Stats{Size: c.geom.Size, PerSlab: c.geom.PerSlab, SlabSize: c.geom.SlabSize,
		Waste: c.geom.Waste(), Slabs: len(c.slabs), Allocated: len(c.live)}
	for _, s := range c.slabs {
		if s.used == c.geom.PerSlab {
			st.Full++
		} else if s.used == 0 {
			st.Empty++
		} else {
			st.Partial++
		}
	}
	return st
}

func (c *Cache) verify() error {
	g, st := c.geom, c.Stats()
	free := 0
	for j, s := range c.slabs {
		if s.used < 0 || s.used > g.PerSlab {
			return fmt.Errorf("slab %d used %d out of range", j, s.used)
		}
		free += g.PerSlab - s.used
	}
	if st.Allocated+free != g.PerSlab*st.Slabs {
		return fmt.Errorf("conservation live=%d free=%d cap=%d", st.Allocated, free, g.PerSlab*st.Slabs)
	}
	if st.Full+st.Partial+st.Empty != st.Slabs {
		return fmt.Errorf("census does not partition slabs")
	}
	for off := range c.live {
		if !g.Contains(off) {
			return fmt.Errorf("offset %d illegally packed", off)
		}
	}
	return nil
}

type naiveRef struct {
	occ             [][]bool
	size, per, slab int
}

func (n *naiveRef) alloc() int {
	for j := range n.occ {
		for i, u := range n.occ[j] {
			if !u {
				n.occ[j][i] = true
				return j*n.slab + i*n.size
			}
		}
	}
	j := len(n.occ)
	n.occ = append(n.occ, make([]bool, n.per))
	n.occ[j][0] = true
	return j * n.slab
}

func (n *naiveRef) free(off int) {
	j, i := off/n.slab, (off%n.slab)/n.size
	n.occ[j][i] = false
}

// SelfCheck runs the script, random interleavings, drain and invariant checks.
func (c *Cache) SelfCheck() error {
	cc, _ := NewCache(10, 8, 60) // valid constants: never errors
	for k, w := range []int{0, 16, 32, 60} {
		off, e := cc.Alloc()
		if e != nil || off != w {
			return fmt.Errorf("script alloc %d = %d,%v want %d", k, off, e, w)
		}
	}
	if err := cc.Free(16); err != nil {
		return err
	}
	if st := cc.Stats(); st.Full != 0 || st.Partial != 2 {
		return fmt.Errorf("after Free(16): %+v", st)
	}
	if err := cc.Free(60); err != nil || cc.Stats().Empty != 1 {
		return fmt.Errorf("after Free(60): %+v %v", cc.Stats(), err)
	}
	for k, w := range []int{16, 60} {
		if off, _ := cc.Alloc(); off != w {
			return fmt.Errorf("realloc %d = %d want %d", k, off, w)
		}
	}
	if err := cc.verify(); err != nil {
		return err
	}
	for off := range cc.live {
		_ = cc.Free(off)
	}
	if len(cc.live) != 0 || cc.Stats().Full != 0 {
		return fmt.Errorf("drain left live=%d full=%d", len(cc.live), cc.Stats().Full)
	}
	return cc.randomAgainstNaive()
}

func (c *Cache) randomAgainstNaive() error {
	cfgs := [][3]int{{10, 8, 60}, {8, 8, 64}, {1, 1, 5}, {12, 4, 40}, {3, 8, 30}}
	for seed, cfg := range cfgs {
		real, _ := NewCache(cfg[0], cfg[1], cfg[2])
		g := real.geom
		ref := &naiveRef{size: g.Size, per: g.PerSlab, slab: g.SlabSize}
		rng := rand.New(rand.NewSource(int64(seed + 1)))
		var live []int
		for step := 0; step < 2000; step++ {
			if len(live) > 0 && rng.Intn(2) == 1 {
				k := rng.Intn(len(live))
				off := live[k]
				live = append(live[:k], live[k+1:]...)
				if err := real.Free(off); err != nil {
					return err
				}
				ref.free(off)
				continue
			}
			o1, e := real.Alloc()
			if e != nil || o1 != ref.alloc() {
				return fmt.Errorf("cfg%v step%d alloc %d,%v vs naive", cfg, step, o1, e)
			}
			live = append(live, o1)
		}
		if err := real.verify(); err != nil {
			return err
		}
		for _, off := range live {
			_ = real.Free(off)
		}
		if len(real.live) != 0 || real.Stats().Full != 0 {
			return fmt.Errorf("cfg%v drain left state", cfg)
		}
	}
	return nil
}
