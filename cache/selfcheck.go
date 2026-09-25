package cache

import "fmt"
import "ontology/slab"

// SelfCheck verifies the four invariants on built-in sequences (offsets per
// the naive rule, NOTES.md) plus the complexity bound.
func SelfCheck() error {
	for _, f := range []func() error{checkSequence, checkFailures, checkComplexity} {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

// checkSequence runs the eight built-in ops, checking inv.1/2/3 after each.
func checkSequence() error {
	c, _ := New(10, 8, 60)
	wantOff := []int{0, 16, 32, 60, -1, -1, 16, 60}
	wantSt := [][3]int{{0, 1, 0}, {0, 1, 0}, {1, 0, 0}, {1, 1, 0},
		{0, 2, 0}, {0, 1, 1}, {1, 0, 1}, {1, 1, 0}} // full, partial, empty
	for step := 0; step < 8; step++ {
		var off int
		if step == 4 || step == 5 {
			_ = c.Free([]int{16, 60}[step-4]) // cannot fail at these steps
		} else {
			off, _ = c.Alloc()
		}
		if wantOff[step] >= 0 && off != wantOff[step] {
			return fmt.Errorf("invariant2: step %d got %d want %d", step, off, wantOff[step])
		}
		st := c.Stats()
		if [3]int{st.Full, st.Partial, st.Empty} != wantSt[step] ||
			st.Allocated+st.Free != c.perSlab*st.Slabs {
			return fmt.Errorf("invariant1/2: step %d states %+v", step, st)
		}
		if off >= 0 && ((off%c.slabSize)%c.sz != 0 || off%c.slabSize+c.sz > c.slabSize) {
			return fmt.Errorf("invariant3: step %d bad offset %d", step, off)
		}
	}
	for _, off := range []int{0, 16, 32, 60} { // drain: no full slab remains
		_ = c.Free(off)
	}
	if st := c.Stats(); st.Allocated != 0 || st.Full != 0 {
		return fmt.Errorf("invariant2: drained cache has %d live, %d full", st.Allocated, st.Full)
	}
	return nil
}

type badCtor struct {
	raw, align, slab int
	want             error
}

// checkFailures: invariant 4 — distinct sentinels, no state change.
func checkFailures() error {
	ctors := []badCtor{
		{0, 8, 60, ErrRawSize}, {10, 3, 60, ErrAlign}, {10, 8, 8, ErrTooBig},
	}
	for _, b := range ctors {
		if _, err := New(b.raw, b.align, b.slab); err != b.want {
			return fmt.Errorf("invariant4: New(%d,%d,%d) got %v want %v", b.raw, b.align, b.slab, err, b.want)
		}
	}
	c, _ := New(10, 8, 60)
	live, _ := c.Alloc()
	before := c.Stats()
	for _, off := range []int{999, 48} { // never allocated; tail waste
		if err := c.Free(off); err != ErrBadFree {
			return fmt.Errorf("invariant4: Free(%d) got %v", off, err)
		}
	}
	if c.Stats() != before {
		return fmt.Errorf("invariant4: rejected free changed state")
	}
	if err := c.Free(live); err != nil { // real free, then double free
		return err
	}
	if err := c.Free(live); err != ErrBadFree {
		return fmt.Errorf("invariant4: double free got %v", err)
	}
	_, err := c.Alloc()
	return err
}

// checkComplexity: Alloc/Free inspect O(1) slabs regardless of m.
func checkComplexity() error {
	for _, m := range []int{100, 1000, 10000} {
		c, _ := New(10, 8, 60)
		offs := make([]int, 0, m*c.perSlab)
		for i := 0; i < m*c.perSlab; i++ { // fill m slabs completely
			off, _ := c.Alloc()
			offs = append(offs, off)
		}
		for j := 0; j < m; j++ { // free one slot per slab: m partial slabs
			_ = c.Free(offs[j*c.perSlab])
		}
		_, _ = c.Alloc()
		if c.checked > 4 {
			return fmt.Errorf("complexity: Alloc checked %d slabs with m=%d", c.checked, m)
		}
		_ = c.Free(offs[1])
		if c.checked > 1 {
			return fmt.Errorf("complexity: Free checked %d slabs", c.checked)
		}
	}
	return nil
}

// naive is the reference model used by the tests: per-slab used bits.
type naive struct {
	used                  [][]bool
	slabSize, sz, perSlab int
}

func (n *naive) alloc() int {
	empty := -1
	for j, slots := range n.used {
		cnt := 0
		for _, u := range slots {
			if u {
				cnt++
			}
		}
		if cnt == 0 {
			if empty < 0 {
				empty = j
			}
			continue
		}
		if cnt < n.perSlab { // partial: smallest free slot
			for i, u := range slots {
				if !u {
					slots[i] = true
					return j*n.slabSize + i*n.sz
				}
			}
		}
	}
	if empty >= 0 { // smallest empty slab, slot 0
		n.used[empty][0] = true
		return empty * n.slabSize
	}
	n.used = append(n.used, make([]bool, n.perSlab)) // fresh slab, slot 0
	n.used[len(n.used)-1][0] = true
	return (len(n.used) - 1) * n.slabSize
}

func (n *naive) free(off int) { j, i := slab.Locate(off, n.slabSize, n.sz); n.used[j][i] = false }
