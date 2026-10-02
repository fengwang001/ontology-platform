package vma

import (
	"sync"
	"sync/atomic"
)

const (
	permMask = 7
	allFlags = FlagFixed | FlagNoReplace | FlagGrowsDown
)

// Manager is a concurrent VMA allocator over the fixed page range [low, high).
type Manager struct {
	mu sync.RWMutex

	low     int64
	high    int64
	maxv    int
	guard   int64
	sm      int64
	root    *node
	count   int
	t       *tree
	visited atomic.Int64
}

// New validates configuration and constructs an empty manager.
func New(low, high int64, maxv int, guardGap, stackMax int64) (*Manager, error) {
	if low < 1 || high <= low || high > 1<<40 {
		return nil, ErrInvalid
	}
	if maxv < 1 || maxv > 1_000_000 {
		return nil, ErrInvalid
	}
	if guardGap < 1 || guardGap > 1<<20 || stackMax < 1 || stackMax > 1<<40 {
		return nil, ErrInvalid
	}
	return &Manager{
		low: low, high: high, maxv: maxv,
		guard: guardGap, sm: stackMax,
		t: &tree{low: low, guard: guardGap},
	}, nil
}

func (m *Manager) beginOp() *int { return new(int) }

func (m *Manager) endOp(vis *int) { m.visited.Add(int64(*vis)) }

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// cloneV returns the [s,e) slice of v with the correct file offset.
func cloneV(v VMA, s, e int64) VMA {
	off := v.Off
	if v.File != 0 {
		off = v.Off + (s - v.Start)
	}
	return VMA{Start: s, End: e, Perm: v.Perm, GrowsDown: v.GrowsDown, File: v.File, Off: off}
}

func mergeVMA(a, b VMA) VMA {
	return VMA{Start: a.Start, End: b.End, Perm: a.Perm, GrowsDown: a.GrowsDown, File: a.File, Off: a.Off}
}

func lowerNeighbor(root *node, start int64) *node {
	var res *node
	n := root
	for n != nil {
		if n.v.End <= start {
			res = n
			n = n.right
		} else {
			n = n.left
		}
	}
	return res
}

func upperNeighbor(root *node, end int64) *node {
	var res *node
	n := root
	for n != nil {
		if n.v.Start >= end {
			res = n
			n = n.left
		} else {
			n = n.right
		}
	}
	return res
}

// Mmap places a mapping and returns its start page.
func (m *Manager) Mmap(hint int64, length int64, perm int, flags int, file, off int64) (int64, error) {
	if length < 1 || perm < 0 || perm > permMask || flags&^allFlags != 0 {
		return 0, ErrInvalid
	}
	fixed := flags&FlagFixed != 0
	if flags&FlagNoReplace != 0 && !fixed {
		return 0, ErrInvalid
	}
	growsDown := flags&FlagGrowsDown != 0
	if growsDown && file != 0 {
		return 0, ErrInvalid
	}
	if file < 0 || off < 0 || hint < 0 {
		return 0, ErrInvalid
	}
	if fixed && (hint < m.low || hint+length > m.high || hint+length < hint) {
		return 0, ErrInvalid
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	vis := m.beginOp()
	defer m.endOp(vis)
	t := m.t

	var start int64
	if !fixed {
		start = m.autoPlace(vis, hint, length)
		if start < 0 {
			return 0, ErrNoSpace
		}
	} else {
		start = hint
		end := hint + length
		if t.overlapper(m.root, start, end, vis) != nil {
			if flags&FlagNoReplace != 0 {
				return 0, ErrExists
			}
			root, _, _, err := m.unmapRange(m.root, start, end, vis)
			if err != nil {
				return 0, err
			}
			m.root = root
			m.count = treeCount(root)
		}
	}

	nv := VMA{
		Start: start, End: start + length, Perm: perm,
		GrowsDown: growsDown, File: file, Off: off,
	}
	root, _, ok := m.placeAndMerge(m.root, nv, vis)
	if !ok {
		return 0, ErrTooMany
	}
	m.root = root
	m.count = treeCount(root)
	return start, nil
}

// autoPlace returns a start page, or -1 when nothing fits.
func (m *Manager) autoPlace(vis *int, hint, length int64) int64 {
	t := m.t
	end := hint + length
	if hint != 0 && end >= hint && hint >= m.low && end <= m.high &&
		t.overlapper(m.root, hint, end, vis) == nil {
		s := t.successor(m.root, hint-1, vis)
		if s == nil || !s.v.GrowsDown || end <= max64(m.low, s.v.Start-m.guard) {
			return hint
		}
	}
	lo, hi, ok := t.gapSearch(m.root, length, m.high, vis)
	if !ok {
		return -1
	}
	_ = lo
	return hi - length
}

// unmapRange removes every mapped page in [start, end) from immutable root.
// Returns new root, removed pages, split count and error.
func (m *Manager) unmapRange(root *node, start, end int64, vis *int) (*node, int64, int, error) {
	t := m.t
	var hit []VMA
	for _, v := range t.all(root, vis) {
		if v.Start < end && start < v.End {
			hit = append(hit, v)
		}
	}
	splits := 0
	if len(hit) > 0 {
		first, last := hit[0], hit[len(hit)-1]
		if start > first.Start && start < first.End {
			splits++
		}
		if end > last.Start && end < last.End {
			splits++
		}
	}
	if treeCount(root)+splits > m.maxv {
		return nil, 0, splits, ErrTooMany
	}

	removed := int64(0)
	r := root
	for _, ov := range hit {
		switch {
		case start <= ov.Start && end >= ov.End:
			r = t.erase(r, ov.Start, vis)
			removed += ov.End - ov.Start
		case start > ov.Start && end < ov.End:
			r = t.erase(r, ov.Start, vis)
			r = t.insert(r, cloneV(ov, ov.Start, start), vis)
			r = t.insert(r, cloneV(ov, end, ov.End), vis)
			removed += end - start
		case start > ov.Start:
			r = t.erase(r, ov.Start, vis)
			r = t.insert(r, cloneV(ov, ov.Start, start), vis)
			removed += ov.End - start
		default:
			r = t.erase(r, ov.Start, vis)
			r = t.insert(r, cloneV(ov, end, ov.End), vis)
			removed += end - ov.Start
		}
	}
	return r, removed, splits, nil
}

// placeAndMerge inserts nv and merges with compatible neighbours.
// ok=false means an unmerged insert would exceed the limit.
func (m *Manager) placeAndMerge(root *node, nv VMA, vis *int) (*node, int, bool) {
	t := m.t
	c := treeCount(root)

	lower := lowerNeighbor(root, nv.Start)
	upper := upperNeighbor(root, nv.End)
	mergeLow := lower != nil && compatible(lower.v, nv)
	mergeHigh := upper != nil && compatible(nv, upper.v)
	if !mergeLow && !mergeHigh {
		if c+1 > m.maxv {
			return nil, 0, false
		}
		return t.insert(root, nv, vis), 0, true
	}

	r := root
	newStart, newEnd := nv.Start, nv.End
	merged := 0
	if mergeLow {
		newStart = lower.v.Start
		nv = mergeVMA(lower.v, nv)
		r = t.erase(r, lower.v.Start, vis)
		merged++
	}
	if mergeHigh {
		newEnd = upper.v.End
		nv = mergeVMA(nv, upper.v)
		r = t.erase(r, upper.v.Start, vis)
		merged++
	}
	nv.Start, nv.End = newStart, newEnd
	r = t.insert(r, nv, vis)
	return r, merged, true
}

// Munmap unmaps [start, start+length) and returns removed pages.
func (m *Manager) Munmap(start, length int64) (int64, error) {
	if length < 1 || start < m.low || start+length > m.high || start+length < start {
		return 0, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	vis := m.beginOp()
	defer m.endOp(vis)

	root, removed, _, err := m.unmapRange(m.root, start, start+length, vis)
	if err != nil {
		return 0, err
	}
	m.root = root
	m.count = treeCount(root)
	return removed, nil
}

// Mprotect changes permissions over a fully mapped interval.
func (m *Manager) Mprotect(start, length int64, perm int) error {
	if length < 1 || perm < 0 || perm > permMask ||
		start < m.low || start+length > m.high || start+length < start {
		return ErrInvalid
	}
	end := start + length
	m.mu.Lock()
	defer m.mu.Unlock()
	vis := m.beginOp()
	defer m.endOp(vis)
	t := m.t

	// Coverage + split peak checks happen before any mutation.
	var hit []VMA
	cursor := start
	for cursor < end {
		nd := t.find(m.root, cursor, vis)
		if nd == nil {
			return ErrNoMem
		}
		hit = append(hit, nd.v)
		cursor = nd.v.End
	}
	splits := 0
	if len(hit) > 0 {
		first, last := hit[0], hit[len(hit)-1]
		if start > first.Start && first.Perm != perm {
			splits++
		}
		if end < last.End && last.Perm != perm {
			splits++
		}
	}
	if m.count+splits > m.maxv {
		return ErrTooMany
	}

	// Merge window reaches one VMA beyond each end.
	loBound, hiBound := start, end
	if p := lowerNeighbor(m.root, start); p != nil && p.v.End == start {
		loBound = p.v.Start
	}
	if s := upperNeighbor(m.root, end); s != nil && s.v.Start == end {
		hiBound = s.v.End
	}

	hitSet := make(map[int64]bool, len(hit))
	for _, v := range hit {
		hitSet[v.Start] = true
	}

	// Remove window VMAs, rebuild ordered pieces with updated permissions.
	r := m.root
	var pieces []VMA
	for _, v := range t.all(m.root, vis) {
		if v.End <= loBound || v.Start >= hiBound {
			continue
		}
		r = t.erase(r, v.Start, vis)
		if !hitSet[v.Start] {
			pieces = append(pieces, v)
			continue
		}
		s, e := max64(v.Start, start), min64(v.End, end)
		if v.Start < s {
			pieces = append(pieces, cloneV(v, v.Start, s))
		}
		piece := cloneV(v, s, e)
		if v.Perm != perm {
			piece.Perm = perm
		}
		pieces = append(pieces, piece)
		if e < v.End {
			pieces = append(pieces, cloneV(v, e, v.End))
		}
	}

	for _, p := range pieces {
		nr, _, ok := m.placeAndMerge(r, p, vis)
		if !ok {
			return ErrTooMany
		}
		r = nr
	}
	m.root = r
	m.count = treeCount(r)
	return nil
}

// Grow simulates a downward stack fault at addr.
func (m *Manager) Grow(addr int64) (int64, error) {
	if addr < m.low || addr >= m.high {
		return 0, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	vis := m.beginOp()
	defer m.endOp(vis)
	t := m.t

	if t.find(m.root, addr, vis) != nil {
		return 0, ErrMapped
	}
	v := t.successor(m.root, addr, vis)
	if v == nil || !v.v.GrowsDown {
		return 0, ErrSegv
	}
	if v.v.End-addr > m.sm {
		return 0, ErrStackLimit
	}
	below := m.low
	if p := t.predecessor(m.root, addr, vis); p != nil {
		below = p.v.End
	}
	if addr-below < m.guard {
		return 0, ErrNoRoom
	}

	nv := v.v
	nv.Start = addr
	r := t.erase(m.root, v.v.Start, vis)
	r = t.insert(r, nv, vis)
	m.root = r
	m.count = treeCount(r)
	return addr, nil
}

// Find returns the VMA covering addr.
func (m *Manager) Find(addr int64) (VMA, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	vis := new(int)
	n := m.t.find(m.root, addr, vis)
	m.visited.Add(int64(*vis))
	if n == nil {
		return VMA{}, false
	}
	return n.v, true
}

// VMAs returns the full ordered region table.
func (m *Manager) VMAs() []VMA {
	m.mu.RLock()
	defer m.mu.RUnlock()
	vis := new(int)
	out := m.t.all(m.root, vis)
	m.visited.Add(int64(*vis))
	return out
}

// Count returns the number of VMAs.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.count
}

// Visited is the non-exported-style cumulative node-visit counter exposed for
// complexity verification.
func (m *Manager) Visited() int64 { return m.visited.Load() }
