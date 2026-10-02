package vma

import (
	"fmt"
	"sort"
)

// naiveMgr is a page-by-page, linear-scan reimplementation of the spec used as
// the reference oracle in differential tests.
type naiveMgr struct {
	low, high       int64
	maxv            int
	guard, stackMax int64
	vmas            []VMA
	log             []string
}

func newNaive(low, high int64, maxv int, guard, stackMax int64) *naiveMgr {
	return &naiveMgr{low: low, high: high, maxv: maxv, guard: guard, stackMax: stackMax}
}

func (n *naiveMgr) record(format string, args ...interface{}) {
	n.log = append(n.log, fmt.Sprintf(format, args...))
}

func (n *naiveMgr) count() int { return len(n.vmas) }

func (n *naiveMgr) table() []VMA {
	out := make([]VMA, len(n.vmas))
	copy(out, n.vmas)
	return out
}

// guardLo returns the lower bound of v's guard zone.
func (n *naiveMgr) guardLo(v VMA) int64 {
	lo := v.Start - n.guard
	if lo < n.low {
		lo = n.low
	}
	return lo
}

// blockedAt reports whether page p belongs to any VMA or guard zone.
func (n *naiveMgr) blockedAt(p int64) bool {
	for _, v := range n.vmas {
		if p >= v.Start && p < v.End {
			return true
		}
		if v.GrowsDown && p >= n.guardLo(v) && p < v.Start {
			return true
		}
	}
	return false
}

func (n *naiveMgr) overlapsVMA(s, e int64) bool {
	for _, v := range n.vmas {
		if s < v.End && e > v.Start {
			return true
		}
	}
	return false
}

func (n *naiveMgr) intersectsBlock(s, e int64) bool {
	for p := s; p < e; p++ {
		if n.blockedAt(p) {
			return true
		}
	}
	return false
}

func (n *naiveMgr) compatibleAt(i int) bool {
	if i+1 >= len(n.vmas) {
		return false
	}
	return compatible(n.vmas[i], n.vmas[i+1])
}

// mergeAll folds every compatible adjacent pair repeatedly.
func (n *naiveMgr) mergeAll() {
	for {
		did := false
		out := make([]VMA, 0, len(n.vmas))
		sorted := append([]VMA(nil), n.vmas...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
		for i := 0; i < len(sorted); {
			cur := sorted[i]
			for i+1 < len(sorted) && cur.End == sorted[i+1].Start && compatible(cur, sorted[i+1]) {
				cur.End = sorted[i+1].End
				i++
				did = true
			}
			out = append(out, cur)
			i++
		}
		n.vmas = out
		if !did {
			return
		}
	}
}

// overlappersIn returns the sorted VMAs intersecting [s,e).
func (n *naiveMgr) overlappersIn(s, e int64) []VMA {
	var out []VMA
	for _, v := range n.vmas {
		if s < v.End && e > v.Start {
			out = append(out, v)
		}
	}
	return out
}

// splitCountFor counts endpoint splits required to carve [s,e) out.
func splitCountFor(vs []VMA, s, e int64) int {
	if len(vs) == 0 {
		return 0
	}
	c := 0
	first, last := vs[0], vs[len(vs)-1]
	if s > first.Start && s < first.End {
		c++
	}
	if e > last.Start && e < last.End {
		c++
	}
	return c
}

func (n *naiveMgr) removeRange(s, e int64) (int64, int, error) {
	hit := n.overlappersIn(s, e)
	splits := splitCountFor(hit, s, e)
	n.record("unmap plan: count=%d splits=%d peak=%d maxv=%d", len(n.vmas), splits, len(n.vmas)+splits, n.maxv)
	if len(n.vmas)+splits > n.maxv {
		return 0, splits, ErrTooMany
	}
	removed := int64(0)
	var keep []VMA
	for _, v := range n.vmas {
		if s >= v.End || e <= v.Start {
			keep = append(keep, v)
			continue
		}
		lo, hi := max64(v.Start, s), min64(v.End, e)
		removed += hi - lo
		if v.Start < lo {
			keep = append(keep, cloneV(v, v.Start, lo))
		}
		if hi < v.End {
			keep = append(keep, cloneV(v, hi, v.End))
		}
	}
	n.vmas = keep
	n.mergeAll()
	return removed, splits, nil
}

func (n *naiveMgr) mmap(hint, length int64, perm int, flags int, file, off int64) (int64, error) {
	fixed := flags&FlagFixed != 0
	grows := flags&FlagGrowsDown != 0
	var start int64
	if !fixed {
		end := hint + length
		if hint != 0 && hint >= n.low && end <= n.high && !n.intersectsBlock(hint, end) {
			start = hint
			n.record("mmap hint accepted start=%d", start)
		} else {
			bestLo, bestHi := int64(-1), int64(-1)
			var p int64 = n.low
			for p < n.high {
				if n.blockedAt(p) {
					p++
					continue
				}
				q := p
				for q < n.high && !n.blockedAt(q) {
					q++
				}
				if q-p >= length && (bestHi < 0 || q > bestHi) {
					bestLo, bestHi = p, q
				}
				p = q
			}
			if bestHi < 0 {
				n.record("mmap no space")
				return 0, ErrNoSpace
			}
			start = bestHi - length
			n.record("mmap topdown gap=[%d,%d) start=%d", bestLo, bestHi, start)
		}
	} else {
		start = hint
		if n.overlapsVMA(start, start+length) {
			if flags&FlagNoReplace != 0 {
				n.record("mmap fixed exists")
				return 0, ErrExists
			}
			if _, _, err := n.removeRange(start, start+length); err != nil {
				return 0, err
			}
		}
	}

	nv := VMA{Start: start, End: start + length, Perm: perm, GrowsDown: grows, File: file, Off: off}
	// Insert then check merge count against the pre-insert table.
	before := len(n.vmas)
	lower := -1
	upper := -1
	for i, v := range n.vmas {
		if v.End <= nv.Start && (lower == -1 || v.Start > n.vmas[lower].Start) {
			lower = i
		}
		if v.Start >= nv.End && (upper == -1 || v.Start < n.vmas[upper].Start) {
			upper = i
		}
	}
	ml := lower != -1 && compatible(n.vmas[lower], nv)
	mh := upper != -1 && compatible(nv, n.vmas[upper])
	if !ml && !mh && before+1 > n.maxv {
		n.record("mmap toomany before=%d maxv=%d", before, n.maxv)
		return 0, ErrTooMany
	}
	n.vmas = append(n.vmas, nv)
	n.mergeAll()
	return start, nil
}

func (n *naiveMgr) munmap(start, length int64) (int64, error) {
	removed, _, err := n.removeRange(start, start+length)
	return removed, err
}

func (n *naiveMgr) mprotect(start, length, perm int64) error {
	end := start + length
	for p := start; p < end; {
		var hit *VMA
		for i := range n.vmas {
			if p >= n.vmas[i].Start && p < n.vmas[i].End {
				hit = &n.vmas[i]
				break
			}
		}
		if hit == nil {
			n.record("mprotect hole at %d", p)
			return ErrNoMem
		}
		p = hit.End
	}
	hit := n.overlappersIn(start, end)
	splits := 0
	first, last := hit[0], hit[len(hit)-1]
	if start > first.Start && first.Perm != int(perm) {
		splits++
	}
	if end < last.End && last.Perm != int(perm) {
		splits++
	}
	n.record("mprotect count=%d splits=%d peak=%d", len(n.vmas), splits, len(n.vmas)+splits)
	if len(n.vmas)+splits > n.maxv {
		return ErrTooMany
	}
	var out []VMA
	for _, v := range n.vmas {
		if v.End <= start || v.Start >= end {
			out = append(out, v)
			continue
		}
		s, e := max64(v.Start, start), min64(v.End, end)
		if v.Start < s {
			out = append(out, cloneV(v, v.Start, s))
		}
		piece := cloneV(v, s, e)
		if v.Perm != int(perm) {
			piece.Perm = int(perm)
		}
		out = append(out, piece)
		if e < v.End {
			out = append(out, cloneV(v, e, v.End))
		}
	}
	n.vmas = out
	n.mergeAll()
	return nil
}

func (n *naiveMgr) grow(addr int64) (int64, error) {
	for _, v := range n.vmas {
		if addr >= v.Start && addr < v.End {
			n.record("grow mapped %d", addr)
			return 0, ErrMapped
		}
	}
	var st *VMA
	for i := range n.vmas {
		if n.vmas[i].Start > addr {
			if st == nil || n.vmas[i].Start < st.Start {
				st = &n.vmas[i]
			}
		}
	}
	if st == nil || !st.GrowsDown {
		n.record("grow segv %d", addr)
		return 0, ErrSegv
	}
	if st.End-addr > n.stackMax {
		n.record("grow stacklimit %d-%d=%d sm=%d", st.End, addr, st.End-addr, n.stackMax)
		return 0, ErrStackLimit
	}
	below := n.low
	for _, v := range n.vmas {
		if v.End <= addr && v.End > below {
			below = v.End
		}
	}
	if addr-below < n.guard {
		n.record("grow noroom gap=%d guard=%d", addr-below, n.guard)
		return 0, ErrNoRoom
	}
	for i := range n.vmas {
		if n.vmas[i].Start == st.Start {
			n.vmas[i].Start = addr
		}
	}
	n.record("grow ok %d -> vma start now %d", addr, addr)
	return addr, nil
}
