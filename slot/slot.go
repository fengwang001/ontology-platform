// Package slot manages connID slots: FREE/OPEN state, generation numbers,
// and min-free-slot location via a min-heap. It depends on nothing.
package slot

import (
	"errors"
	"math/bits"
)

var (
	ErrNoSlots = errors.New("slot: no free slot")
	ErrBadID   = errors.New("slot: conn id out of range")
)

// Handle identifies one opening of a slot. Gen >= 1 for a live handle.
type Handle struct {
	ID  int
	Gen int
}

type entry struct {
	open bool
	gen  int
}

// Table holds C slots plus a min-heap of free connIDs.
type Table struct {
	slots []entry
	free  []int // min-heap of free connIDs

	checked int  // slots inspected by the most recent Open
	boundOK bool // every Open so far stayed within ceil(log2 C)+1
	bound   int
}

// New returns a table with c slots, all FREE, gen 0.
func New(c int) *Table {
	t := &Table{slots: make([]entry, c), boundOK: true}
	t.bound = bits.Len(uint(c-1)) + 1 // ceil(log2 c) + 1
	for i := range t.slots {
		t.free = append(t.free, i)
	}
	return t
}

// C returns the slot count.
func (t *Table) C() int { return len(t.slots) }

// State reports whether id is OPEN and its current generation.
func (t *Table) State(id int) (open bool, gen int, err error) {
	if id < 0 || id >= len(t.slots) {
		return false, 0, ErrBadID
	}
	e := t.slots[id]
	return e.open, e.gen, nil
}

// Open allocates the smallest free connID, bumps its generation, and
// returns the handle. The number of slots inspected is recorded in the
// unexported checked counter and must stay logarithmic in C.
func (t *Table) Open() (Handle, error) {
	t.checked = 0
	if len(t.free) == 0 {
		return Handle{}, ErrNoSlots
	}
	t.checked = 1 // inspecting the heap root
	id := t.free[0]
	last := t.free[len(t.free)-1]
	t.free = t.free[:len(t.free)-1]
	if n := len(t.free); n > 0 {
		t.free[0] = last
		for i := 0; ; { // sift down, one inspected slot per level
			t.checked++
			c := 2*i + 1
			if c >= n {
				break
			}
			if c+1 < n && t.free[c+1] < t.free[c] {
				c++
			}
			if t.free[c] >= t.free[i] {
				break
			}
			t.free[i], t.free[c] = t.free[c], t.free[i]
			i = c
		}
	}
	if t.checked > t.bound {
		t.boundOK = false
	}
	t.slots[id].open = true
	t.slots[id].gen++
	return Handle{ID: id, Gen: t.slots[id].gen}, nil
}

// Free returns an OPEN slot to FREE. The generation is kept, not reset.
// The caller (demux) has already validated the handle.
func (t *Table) Free(id int) {
	t.slots[id].open = false
	t.free = append(t.free, id)
	for i := len(t.free) - 1; i > 0; { // sift up
		p := (i - 1) / 2
		if t.free[p] <= t.free[i] {
			break
		}
		t.free[p], t.free[i] = t.free[i], t.free[p]
		i = p
	}
}

// BoundOK reports whether every Open so far inspected at most
// ceil(log2 C)+1 slots. It exposes a verdict, never the counter value.
func (t *Table) BoundOK() bool { return t.boundOK }
