// Package pagetable implements a three-level radix page-table mapper over a
// 512-page virtual address space with 1/8/64-page leaves, on-demand table
// allocation under a table-page quota, split-on-partial-unmap with A/D
// inheritance, and promotion of full tables back into larger leaves.
package pagetable

import (
	"fmt"
	"sync"
)

const (
	// NumPages is the number of virtual pages in the address space.
	NumPages = 512
	// NumSlots is the number of slots per table at every level.
	NumSlots = 8
	// MaxPfn is the number of valid physical page numbers (0..MaxPfn-1).
	MaxPfn = 1 << 20
	// MaxQuota is the largest acceptable table-page quota.
	MaxQuota = 1_000_000
)

// spanAt[level] is the number of pages covered by one slot at that level:
// level 0 is the root (64 pages per slot), level 1 is L1 (8), level 2 is L0 (1).
var spanAt = [3]int{64, 8, 1}

// ErrCode identifies the single rejection reason reported by an operation.
type ErrCode int

const (
	ErrInvalidArgument  ErrCode = iota // invalid arguments
	ErrMisaligned                      // vpn or pfn not aligned to size
	ErrOverlap                         // region already has mappings
	ErrQuotaExceeded                   // table-page quota would be exceeded
	ErrUnmapped                        // page not mapped
	ErrWriteProtected                  // write to non-writable leaf
	ErrNotATable                       // slot is empty or covered by a leaf
	ErrNotAllLeaves                    // sub-slots are not all leaves
	ErrPfnMismatch                     // pfn not contiguous or base pfn misaligned
	ErrWritableMismatch                // writable bits differ
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrMisaligned:
		return "misaligned"
	case ErrOverlap:
		return "overlap"
	case ErrQuotaExceeded:
		return "table quota exceeded"
	case ErrUnmapped:
		return "unmapped"
	case ErrWriteProtected:
		return "write protected"
	case ErrNotATable:
		return "slot is not a table"
	case ErrNotAllLeaves:
		return "sub-slots are not all leaves"
	case ErrPfnMismatch:
		return "pfn not contiguous or base pfn misaligned"
	case ErrWritableMismatch:
		return "writable bits differ"
	}
	return "unknown error"
}

// Error is the single rejection reason of a failed operation.
type Error struct {
	Op   string
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("pagetable: %s: %s: %s", e.Op, e.Code, e.Msg)
}

// Leaf describes one leaf mapping.
type Leaf struct {
	Start int  // first virtual page number
	Size  int  // 1, 8 or 64
	Pfn   int  // physical page number of the first page
	W     bool // writable
	A     bool // accessed
	D     bool // dirty
}

// Translation is the result of translating one virtual page.
type Translation struct {
	Pfn  int
	Size int
	W    bool
	A    bool
	D    bool
}

// entry is one table slot: either empty, a leaf, or a pointer to a child table.
type entry struct {
	isLeaf  bool
	pfn     int
	w, a, d bool
	child   *table
}

// table is one table page. tables counts the tables in the subtree rooted
// here including this table; mapped counts the mapped pages in the subtree.
type table struct {
	entries [NumSlots]entry
	used    int // number of non-empty slots
	tables  int // tables in subtree, including this one
	mapped  int // mapped pages in subtree
}

// Mapper is a concurrent three-level radix page-table mapper.
type Mapper struct {
	mu       sync.Mutex
	quota    int
	root     *table
	epoch    int
	examined int // slots examined by the last counted operation (Translate/Unmap/Promote)
}

// New creates a mapper whose non-root table pages may not exceed quota.
// A quota outside [1, MaxQuota] is rejected as an invalid configuration.
func New(quota int) (*Mapper, error) {
	if quota < 1 || quota > MaxQuota {
		return nil, &Error{Op: "New", Code: ErrInvalidArgument,
			Msg: fmt.Sprintf("quota %d outside [1,%d]", quota, MaxQuota)}
	}
	return &Mapper{quota: quota, root: &table{tables: 1}}, nil
}

// Map establishes a leaf of size pages at [vpn, vpn+size) with physical base pfn.
func (m *Mapper) Map(vpn, size, pfn int, w bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if size != 1 && size != 8 && size != 64 {
		return &Error{Op: "Map", Code: ErrInvalidArgument, Msg: fmt.Sprintf("size %d not in {1,8,64}", size)}
	}
	if vpn < 0 || vpn >= NumPages {
		return &Error{Op: "Map", Code: ErrInvalidArgument, Msg: fmt.Sprintf("vpn %d outside [0,%d)", vpn, NumPages)}
	}
	if pfn < 0 || pfn >= MaxPfn {
		return &Error{Op: "Map", Code: ErrInvalidArgument, Msg: fmt.Sprintf("pfn %d outside [0,%d)", pfn, MaxPfn)}
	}
	if vpn%size != 0 || pfn%size != 0 {
		return &Error{Op: "Map", Code: ErrMisaligned, Msg: fmt.Sprintf("vpn=%d pfn=%d not aligned to size %d", vpn, pfn, size)}
	}
	if m.mapOverlaps(vpn, size) {
		return &Error{Op: "Map", Code: ErrOverlap, Msg: fmt.Sprintf("region [%d,%d) already mapped", vpn, vpn+size)}
	}
	if need := m.tablesNeeded(vpn, size); m.tablesUsed()+need > m.quota {
		return &Error{Op: "Map", Code: ErrQuotaExceeded,
			Msg: fmt.Sprintf("need %d new tables, used %d, quota %d", need, m.tablesUsed(), m.quota)}
	}
	m.insertLeaf(vpn, size, pfn, w)
	m.epoch++
	return nil
}

// mapOverlaps reports whether [vpn, vpn+size) contains any mapping. The
// caller guarantees vpn is size-aligned, so any overlapping mapping shows up
// as a leaf on the path to the target slot or as a non-empty target slot.
func (m *Mapper) mapOverlaps(vpn, size int) bool {
	e := &m.root.entries[vpn/64]
	if e.isLeaf {
		return true
	}
	if size == 64 {
		return e.child != nil
	}
	if e.child == nil {
		return false
	}
	e = &e.child.entries[(vpn/8)%NumSlots]
	if e.isLeaf {
		return true
	}
	if size == 8 {
		return e.child != nil
	}
	if e.child == nil {
		return false
	}
	e = &e.child.entries[vpn%NumSlots]
	return e.isLeaf || e.child != nil
}

// tablesNeeded counts the new table pages a Map of the given aligned,
// non-overlapping region would create.
func (m *Mapper) tablesNeeded(vpn, size int) int {
	if size == 64 {
		return 0
	}
	re := &m.root.entries[vpn/64]
	if re.child == nil {
		if size == 8 {
			return 1
		}
		return 2
	}
	if size == 8 {
		return 0
	}
	if re.child.entries[(vpn/8)%NumSlots].child == nil {
		return 1
	}
	return 0
}

// insertLeaf places the leaf, creating missing tables along the path and
// maintaining the used/tables/mapped aggregates on every ancestor.
func (m *Mapper) insertLeaf(vpn, size, pfn int, w bool) {
	path := []*table{m.root}
	t := m.root
	for level := 0; spanAt[level] != size; level++ {
		idx := (vpn / spanAt[level]) % NumSlots
		e := &t.entries[idx]
		if e.child == nil {
			e.child = &table{tables: 1}
			t.used++
			for _, ancestor := range path {
				ancestor.tables++
			}
		}
		t = e.child
		path = append(path, t)
	}
	idx := (vpn / size) % NumSlots
	t.entries[idx] = entry{isLeaf: true, pfn: pfn, w: w}
	t.used++
	for _, ancestor := range path {
		ancestor.mapped += size
	}
}

// findLeaf returns the leaf entry covering vpn, or nil.
func (m *Mapper) findLeaf(vpn int) *entry {
	t := m.root
	for level := 0; level < 3; level++ {
		e := &t.entries[(vpn/spanAt[level])%NumSlots]
		if e.isLeaf {
			return e
		}
		if e.child == nil {
			return nil
		}
		t = e.child
	}
	return nil
}

// tablesUsed is the number of non-root table pages in use.
func (m *Mapper) tablesUsed() int {
	return m.root.tables - 1
}

// Unmap removes all mappings in [vpn, vpn+n) and returns the number of pages removed.
func (m *Mapper) Unmap(vpn, n int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.examined = 0
	if vpn < 0 || vpn >= NumPages || n < 1 || vpn+n > NumPages {
		return 0, &Error{Op: "Unmap", Code: ErrInvalidArgument,
			Msg: fmt.Sprintf("invalid range [%d,%d)", vpn, vpn+n)}
	}
	// Copy-on-write traversal: builds the post-unmap structure in a single
	// pass over the slots intersecting the range, counting the tables that
	// splits would create, without touching the live structure.
	newRoot, pages, splits := m.cowTable(m.root, 0, 0, vpn, vpn+n)
	if newRoot == nil {
		newRoot = &table{tables: 1}
	}
	// Peak quota check: all split tables exist before any table is reclaimed,
	// so tables freed later in the same operation cannot offset them.
	if splits > 0 && m.tablesUsed()+splits > m.quota {
		return 0, &Error{Op: "Unmap", Code: ErrQuotaExceeded,
			Msg: fmt.Sprintf("splits need %d new tables, used %d, quota %d", splits, m.tablesUsed(), m.quota)}
	}
	if pages == 0 {
		return 0, nil // nothing mapped in the range: no state change
	}
	m.root = newRoot
	m.epoch++
	return pages, nil
}

// cowTable returns the rewritten version of table t restricted to the effect
// of unmapping [lo,hi), or nil if the result is empty (to be reclaimed by the
// caller). It reports the number of unmapped pages and the number of table
// pages created by splits. Only slots intersecting [lo,hi) are examined;
// fully covered subtrees are released wholesale without descending.
func (m *Mapper) cowTable(t *table, level, base, lo, hi int) (nt *table, pages, splits int) {
	span := spanAt[level]
	first, last := 0, NumSlots-1
	if lo > base {
		first = (lo - base) / span
	}
	if hi < base+NumSlots*span {
		last = (hi - 1 - base) / span
	}
	nt = &table{tables: 1}
	for i := 0; i < NumSlots; i++ {
		e := t.entries[i]
		if i >= first && i <= last {
			m.examined++
			var p, s int
			e, p, s = m.cowSlot(e, level, base+i*span, lo, hi)
			pages += p
			splits += s
		}
		nt.entries[i] = e
		switch {
		case e.isLeaf:
			nt.used++
			nt.mapped += span
		case e.child != nil:
			nt.used++
			nt.tables += e.child.tables
			nt.mapped += e.child.mapped
		}
	}
	if nt.used == 0 {
		return nil, pages, splits
	}
	return nt, pages, splits
}

// cowSlot computes the new state of one slot that intersects [lo,hi).
func (m *Mapper) cowSlot(e entry, level, slotLo, lo, hi int) (entry, int, int) {
	span := spanAt[level]
	slotHi := slotLo + span
	covered := lo <= slotLo && slotHi <= hi
	if e.isLeaf {
		if covered {
			return entry{}, span, 0
		}
		// Partially covered leaf: split into 8 child leaves that inherit
		// W/A/D and consecutive pfns, then unmap inside the new table.
		childSpan := span / 8
		child := &table{tables: 1, used: NumSlots, mapped: span}
		for j := 0; j < NumSlots; j++ {
			child.entries[j] = entry{isLeaf: true, pfn: e.pfn + j*childSpan, w: e.w, a: e.a, d: e.d}
		}
		nt, pages, splits := m.cowTable(child, level+1, slotLo, lo, hi)
		splits++ // the table page holding the split children
		if nt == nil {
			return entry{}, pages, splits
		}
		return entry{child: nt}, pages, splits
	}
	if e.child != nil {
		if covered {
			// Wholesale release: no descent into the subtree.
			return entry{}, e.child.mapped, 0
		}
		nt, pages, splits := m.cowTable(e.child, level+1, slotLo, lo, hi)
		if nt == nil {
			return entry{}, pages, splits
		}
		return entry{child: nt}, pages, splits
	}
	return e, 0, 0
}

// Touch marks the leaf covering vpn accessed, and dirty when write is true.
func (m *Mapper) Touch(vpn int, write bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if vpn < 0 || vpn >= NumPages {
		return &Error{Op: "Touch", Code: ErrInvalidArgument, Msg: fmt.Sprintf("vpn %d outside [0,%d)", vpn, NumPages)}
	}
	e := m.findLeaf(vpn)
	if e == nil {
		return &Error{Op: "Touch", Code: ErrUnmapped, Msg: fmt.Sprintf("vpn %d not mapped", vpn)}
	}
	if write && !e.w {
		// Rejected before setting A: the leaf keeps its previous A/D bits.
		return &Error{Op: "Touch", Code: ErrWriteProtected, Msg: fmt.Sprintf("vpn %d not writable", vpn)}
	}
	e.a = true
	if write {
		e.d = true
	}
	return nil
}

// ScanDirty returns all dirty leaves in start order and clears their D bits.
func (m *Mapper) ScanDirty() []Leaf {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Leaf
	scanDirty(m.root, 0, 0, &out)
	return out
}

// scanDirty appends the dirty leaves of the subtree in slot order and clears
// their D bits.
func scanDirty(t *table, level, base int, out *[]Leaf) {
	span := spanAt[level]
	for i := 0; i < NumSlots; i++ {
		e := &t.entries[i]
		switch {
		case e.isLeaf:
			if e.d {
				*out = append(*out, Leaf{Start: base + i*span, Size: span, Pfn: e.pfn, W: e.w, A: e.a, D: true})
				e.d = false
			}
		case e.child != nil:
			scanDirty(e.child, level+1, base+i*span, out)
		}
	}
}

// Promote merges the table at the size-aligned region containing vpn into one leaf.
func (m *Mapper) Promote(vpn, size int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.examined = 0
	if size != 8 && size != 64 {
		return &Error{Op: "Promote", Code: ErrInvalidArgument, Msg: fmt.Sprintf("size %d not in {8,64}", size)}
	}
	if vpn < 0 || vpn >= NumPages {
		return &Error{Op: "Promote", Code: ErrInvalidArgument, Msg: fmt.Sprintf("vpn %d outside [0,%d)", vpn, NumPages)}
	}
	if vpn%size != 0 {
		return &Error{Op: "Promote", Code: ErrMisaligned, Msg: fmt.Sprintf("vpn %d not aligned to size %d", vpn, size)}
	}
	// Locate the slot that must hold a table. Navigation follows child
	// pointers by index arithmetic; the examined counter below covers the
	// scan of the candidate table's slots.
	parent := m.root
	idx := vpn / 64
	if size == 8 {
		re := &m.root.entries[idx]
		if re.isLeaf || re.child == nil {
			return &Error{Op: "Promote", Code: ErrNotATable, Msg: "root slot empty or covered by a 64-page leaf"}
		}
		parent = re.child
		idx = (vpn / 8) % NumSlots
	}
	e := &parent.entries[idx]
	if e.isLeaf || e.child == nil {
		return &Error{Op: "Promote", Code: ErrNotATable, Msg: "slot empty or covered by a leaf of the same or larger size"}
	}
	child := e.child
	for i := 0; i < NumSlots; i++ {
		m.examined++
		if !child.entries[i].isLeaf {
			return &Error{Op: "Promote", Code: ErrNotAllLeaves, Msg: fmt.Sprintf("sub-slot %d is empty or a table", i)}
		}
	}
	sub := size / 8
	pfn0 := child.entries[0].pfn
	if pfn0%size != 0 {
		return &Error{Op: "Promote", Code: ErrPfnMismatch, Msg: fmt.Sprintf("base pfn %d not aligned to size %d", pfn0, size)}
	}
	for i := 1; i < NumSlots; i++ {
		if child.entries[i].pfn != pfn0+i*sub {
			return &Error{Op: "Promote", Code: ErrPfnMismatch, Msg: fmt.Sprintf("sub-slot %d pfn %d, want %d", i, child.entries[i].pfn, pfn0+i*sub)}
		}
	}
	w := child.entries[0].w
	for i := 1; i < NumSlots; i++ {
		if child.entries[i].w != w {
			return &Error{Op: "Promote", Code: ErrWritableMismatch, Msg: fmt.Sprintf("sub-slot %d writable differs", i)}
		}
	}
	var a, d bool
	for i := 0; i < NumSlots; i++ {
		a = a || child.entries[i].a
		d = d || child.entries[i].d
	}
	*e = entry{isLeaf: true, pfn: pfn0, w: w, a: a, d: d}
	// The merged table page is reclaimed; every ancestor's table count drops.
	parent.tables--
	if parent != m.root {
		m.root.tables--
	}
	m.epoch++
	return nil
}

// Translate resolves vpn to its physical page and leaf attributes.
func (m *Mapper) Translate(vpn int) (Translation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.examined = 0
	if vpn < 0 || vpn >= NumPages {
		return Translation{}, false
	}
	t := m.root
	base := 0
	for level := 0; level < 3; level++ {
		span := spanAt[level]
		idx := (vpn - base) / span
		m.examined++
		e := &t.entries[idx]
		if e.isLeaf {
			return Translation{
				Pfn:  e.pfn + (vpn - base - idx*span),
				Size: span,
				W:    e.w,
				A:    e.a,
				D:    e.d,
			}, true
		}
		if e.child == nil {
			return Translation{}, false
		}
		base += idx * span
		t = e.child
	}
	return Translation{}, false
}

// Leaves returns all leaves ordered by start.
func (m *Mapper) Leaves() []Leaf {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Leaf
	collectLeaves(m.root, 0, 0, &out)
	return out
}

// collectLeaves appends the leaves of the subtree in slot order.
func collectLeaves(t *table, level, base int, out *[]Leaf) {
	span := spanAt[level]
	for i := 0; i < NumSlots; i++ {
		e := &t.entries[i]
		switch {
		case e.isLeaf:
			*out = append(*out, Leaf{Start: base + i*span, Size: span, Pfn: e.pfn, W: e.w, A: e.a, D: e.d})
		case e.child != nil:
			collectLeaves(e.child, level+1, base+i*span, out)
		}
	}
}

// Tables returns the number of non-root table pages currently in use.
func (m *Mapper) Tables() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tablesUsed()
}

// Mapped returns the number of mapped virtual pages.
func (m *Mapper) Mapped() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.root.mapped
}

// Epoch returns the number of successful structure-changing operations.
func (m *Mapper) Epoch() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.epoch
}
