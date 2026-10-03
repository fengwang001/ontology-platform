// Package pagetable implements a three-level radix page-table mapper over a
// 512 virtual-page address space.
//
// Layout: the root table has 8 slots of 64 pages each, every L1 table has 8
// slots of 8 pages each, and every L0 table has 8 slots of 1 page each. A
// slot at any level may be empty, point to a next-level table, or hold a leaf
// mapping 64 (root), 8 (L1) or 1 (L0) pages. Each leaf carries a writable
// bit W plus one shared accessed bit A and one shared dirty bit D.
//
// Structure invariants: the whole structure is uniquely determined by the
// leaf set. A non-root table whose slots are all empty is reclaimed
// immediately and the reclamation cascades upward, so no empty tables exist
// and no two leaves overlap. The number of allocated table pages U (root
// excluded) never exceeds the quota P configured with New.
//
// Split/merge semantics: partially unmapping a leaf splits it into 8
// next-level leaves that inherit W, A and D unchanged (child i gets
// pfn + i*childSize). Promote merges 8 contiguous same-size leaves back into
// one bigger leaf whose A (resp. D) is the logical OR of the children's A
// (resp. D) and whose W is the (uniform) children's W. Quota checks for
// Unmap are done against the peak usage U+s before any table is freed, so
// tables reclaimed later in the same operation cannot offset the check.
//
// Concurrency: every exported method is safe for concurrent use; the result
// is equivalent to some serial execution and snapshot queries (Leaves,
// ScanDirty) observe a consistent state.
package pagetable

import (
	"errors"
	"sync"
)

// Address-space geometry.
const (
	VpnCount  = 512       // total virtual pages
	RootSlots = 8         // slots per table at every level
	MaxPFN    = 1<<20 - 1 // largest valid physical page number
	MaxQuota  = 1_000_000 // largest acceptable table-page quota
	rootCover = 64        // pages covered by one root slot
	l1Cover   = 8         // pages covered by one L1 slot
	l0Cover   = 1         // pages covered by one L0 slot
)

// Operation errors. Each operation reports only the first applicable error,
// in the order the checks are listed for that operation.
var (
	ErrInvalidParam   = errors.New("pagetable: invalid parameter")
	ErrMisaligned     = errors.New("pagetable: vpn or pfn not aligned to size")
	ErrOverlap        = errors.New("pagetable: region overlaps an existing mapping")
	ErrQuota          = errors.New("pagetable: table-page quota exceeded")
	ErrUnmapped       = errors.New("pagetable: vpn is not mapped")
	ErrWriteProtected = errors.New("pagetable: write to a read-only leaf")
	ErrNotTable       = errors.New("pagetable: slot does not point to a table")
	ErrNotAllLeaves   = errors.New("pagetable: child slots are not all leaves")
	ErrPFN            = errors.New("pagetable: child pfns not contiguous or base pfn misaligned")
	ErrWInconsistent  = errors.New("pagetable: child W bits differ")
)

// LeafInfo describes one leaf of the page table.
type LeafInfo struct {
	Start int  // first virtual page number
	Size  int  // leaf size in pages: 1, 8 or 64
	PFN   int  // physical page number of the first page
	W     bool // writable
	A     bool // accessed
	D     bool // dirty
}

// Translation is the result of a successful Translate call.
type Translation struct {
	PFN  int // physical page number of the queried page
	Size int // size of the leaf covering the queried page
	W    bool
	A    bool
	D    bool
}

// slotKind distinguishes empty, leaf and table slots.
type slotKind uint8

const (
	kindEmpty slotKind = iota
	kindLeaf
	kindTable
)

// slot is one entry of a table.
type slot struct {
	kind  slotKind
	child *table // valid when kind == kindTable
	pfn   int    // leaf: physical page number of the first page
	size  int    // leaf: size in pages
	w     bool   // leaf: writable
	a     bool   // leaf: accessed
	d     bool   // leaf: dirty
}

// table is a page-table page: 8 slots plus aggregates over its subtree.
// tables counts the tables in the subtree including this one; pages counts
// the mapped pages in the subtree. Aggregates make whole-subtree drops and
// the derivation of U and Mapped() O(1) per affected table.
type table struct {
	slots  [RootSlots]slot
	tables int
	pages  int
}

// recompute refreshes the aggregates from the (already valid) aggregates of
// the children. Only tables on a mutation path are recomputed, bottom-up.
func (t *table) recompute() {
	t.tables = 1
	t.pages = 0
	for i := range t.slots {
		s := &t.slots[i]
		switch s.kind {
		case kindLeaf:
			t.pages += s.size
		case kindTable:
			t.tables += s.child.tables
			t.pages += s.child.pages
		}
	}
}

// empty reports whether all slots are empty.
func (t *table) empty() bool { return t.tables == 1 && t.pages == 0 }

// Mapper is a concurrent three-level radix page-table mapper.
type Mapper struct {
	mu    sync.RWMutex
	quota int
	root  table
	epoch int

	// Non-exported complexity counters, reset at the start of the matching
	// operation and verified by white-box tests.
	cTranslate int // slots examined by the last Translate
	cUnmap     int // slots examined by the last Unmap
	cPromote   int // slots examined by the last Promote
}

// New creates a mapper whose table-page quota (root table excluded) is P.
// A quota outside [1, MaxQuota] is rejected as an illegal configuration.
func New(P int) (*Mapper, error) {
	if P < 1 || P > MaxQuota {
		return nil, ErrInvalidParam
	}
	m := &Mapper{quota: P}
	m.root.recompute()
	return m, nil
}

// Tables returns U, the number of allocated table pages (root excluded).
func (m *Mapper) Tables() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.root.tables - 1
}

// Mapped returns the total number of mapped pages.
func (m *Mapper) Mapped() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.root.pages
}

// Epoch returns the number of successful structure-changing operations
// (Map, Unmap removing at least one page, Promote).
func (m *Mapper) Epoch() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.epoch
}

// findLeaf returns the leaf slot covering vpn, or nil. Caller must hold the
// lock. Examines at most 3 slots.
func (m *Mapper) findLeaf(vpn int) *slot {
	t := &m.root
	for _, cover := range [3]int{rootCover, l1Cover, l0Cover} {
		s := &t.slots[vpn/cover%RootSlots]
		switch s.kind {
		case kindLeaf:
			return s
		case kindTable:
			t = s.child
		default:
			return nil
		}
	}
	return nil
}

// Translate resolves vpn to its physical page and leaf attributes. The
// second return value is false when vpn is invalid or unmapped. At most 3
// slots are examined.
func (m *Mapper) Translate(vpn int) (Translation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cTranslate = 0
	if vpn < 0 || vpn >= VpnCount {
		return Translation{}, false
	}
	t := &m.root
	for _, cover := range [3]int{rootCover, l1Cover, l0Cover} {
		m.cTranslate++
		s := &t.slots[vpn/cover%RootSlots]
		switch s.kind {
		case kindLeaf:
			return Translation{
				PFN:  s.pfn + vpn%cover,
				Size: s.size,
				W:    s.w,
				A:    s.a,
				D:    s.d,
			}, true
		case kindTable:
			t = s.child
		default:
			return Translation{}, false
		}
	}
	return Translation{}, false
}

// Leaves returns all leaves ordered by start address. It observes a
// consistent snapshot of the structure.
func (m *Mapper) Leaves() []LeafInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []LeafInfo
	var walk func(t *table, base, cover int)
	walk = func(t *table, base, cover int) {
		for i := range t.slots {
			s := &t.slots[i]
			b := base + i*cover
			switch s.kind {
			case kindLeaf:
				out = append(out, LeafInfo{Start: b, Size: s.size, PFN: s.pfn, W: s.w, A: s.a, D: s.d})
			case kindTable:
				walk(s.child, b, cover/RootSlots)
			}
		}
	}
	walk(&m.root, 0, rootCover)
	return out
}
