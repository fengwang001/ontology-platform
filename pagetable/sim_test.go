package pagetable

// sim is a deliberately naive reference model: it keeps a flat sorted list
// of leaves and derives the table count from the leaf set after every
// operation, mirroring the specification step by step. The real
// implementation is checked against it on random operation sequences.

import (
	"errors"
	"fmt"
	"sort"
)

type simLeaf struct {
	start, size, pfn int
	w, a, d          bool
}

type sim struct {
	quota  int
	leaves []simLeaf
	epoch  int
}

func newSim(quota int) *sim { return &sim{quota: quota} }

// tablesUsed derives the table count from the leaf set alone: an L1 table
// exists for every 64-page region holding a leaf smaller than 64, an L0
// table for every 8-page region holding a 1-page leaf.
func (s *sim) tablesUsed() int {
	used := 0
	for r := 0; r < 8; r++ {
		base := r * 64
		for _, l := range s.leaves {
			if l.size < 64 && l.start < base+64 && base < l.start+l.size {
				used++
				break
			}
		}
	}
	for r := 0; r < 64; r++ {
		base := r * 8
		for _, l := range s.leaves {
			if l.size == 1 && base <= l.start && l.start < base+8 {
				used++
				break
			}
		}
	}
	return used
}

func (s *sim) mapped() int {
	n := 0
	for _, l := range s.leaves {
		n += l.size
	}
	return n
}

func (s *sim) insert(l simLeaf) {
	i := sort.Search(len(s.leaves), func(i int) bool { return s.leaves[i].start >= l.start })
	s.leaves = append(s.leaves, simLeaf{})
	copy(s.leaves[i+1:], s.leaves[i:])
	s.leaves[i] = l
}

func (s *sim) overlaps(vpn, size int) bool {
	for _, l := range s.leaves {
		if l.start < vpn+size && vpn < l.start+l.size {
			return true
		}
	}
	return false
}

func (s *sim) mapLeaf(vpn, size, pfn int, w bool) error {
	if size != 1 && size != 8 && size != 64 {
		return ErrInvalidParam
	}
	if vpn < 0 || vpn >= 512 || pfn < 0 || pfn > (1<<20)-1 {
		return ErrInvalidParam
	}
	if vpn%size != 0 || pfn%size != 0 {
		return ErrMisaligned
	}
	if s.overlaps(vpn, size) {
		return ErrOverlap
	}
	trial := &sim{quota: s.quota, leaves: append(append([]simLeaf{}, s.leaves...), simLeaf{vpn, size, pfn, w, false, false})}
	if trial.tablesUsed() > s.quota {
		return ErrQuota
	}
	s.insert(simLeaf{vpn, size, pfn, w, false, false})
	s.epoch++
	return nil
}

// splitsNeeded counts the tables a partial unmap of [lo,hi) would create.
func (s *sim) splitsNeeded(lo, hi int) int {
	n := 0
	for _, l := range s.leaves {
		if l.start >= hi || l.start+l.size <= lo {
			continue
		}
		if lo <= l.start && l.start+l.size <= hi {
			continue
		}
		n++ // the leaf itself splits into a table
		if l.size == 64 {
			for i := 0; i < 8; i++ {
				cb := l.start + i*8
				if cb+8 <= lo || cb >= hi {
					continue
				}
				if lo <= cb && cb+8 <= hi {
					continue
				}
				n++ // a partially covered 8-page child splits too
			}
		}
	}
	return n
}

func (s *sim) unmap(vpn, n int) (int, error) {
	if vpn < 0 || vpn >= 512 || n < 1 || vpn+n > 512 {
		return 0, ErrInvalidParam
	}
	lo, hi := vpn, vpn+n
	if sp := s.splitsNeeded(lo, hi); sp > 0 && s.tablesUsed()+sp > s.quota {
		return 0, ErrQuota
	}
	removed := 0
	var out []simLeaf
	var handle func(l simLeaf)
	handle = func(l simLeaf) {
		if l.start >= hi || l.start+l.size <= lo {
			out = append(out, l)
			return
		}
		if lo <= l.start && l.start+l.size <= hi {
			removed += l.size
			return
		}
		cs := l.size / 8
		for i := 0; i < 8; i++ {
			handle(simLeaf{l.start + i*cs, cs, l.pfn + i*cs, l.w, l.a, l.d})
		}
	}
	for _, l := range s.leaves {
		handle(l)
	}
	s.leaves = out
	if removed > 0 {
		s.epoch++
	}
	return removed, nil
}

func (s *sim) find(vpn int) *simLeaf {
	for i := range s.leaves {
		l := &s.leaves[i]
		if l.start <= vpn && vpn < l.start+l.size {
			return l
		}
	}
	return nil
}

func (s *sim) touch(vpn int, write bool) error {
	if vpn < 0 || vpn >= 512 {
		return ErrInvalidParam
	}
	l := s.find(vpn)
	if l == nil {
		return ErrUnmapped
	}
	if write && !l.w {
		return ErrWriteProtected
	}
	l.a = true
	if write {
		l.d = true
	}
	return nil
}

func (s *sim) promote(vpn, size int) error {
	if size != 8 && size != 64 {
		return ErrInvalidParam
	}
	if vpn < 0 || vpn >= 512 {
		return ErrInvalidParam
	}
	if vpn%size != 0 {
		return ErrMisaligned
	}
	base := vpn
	// Covered by a same-size or bigger leaf, or nothing there at all.
	for _, l := range s.leaves {
		if l.start <= base && base+size <= l.start+l.size {
			return ErrNotTable
		}
	}
	var inside []simLeaf
	for _, l := range s.leaves {
		if base <= l.start && l.start < base+size {
			inside = append(inside, l)
		}
	}
	if len(inside) == 0 {
		return ErrNotTable
	}
	if len(inside) != 8 {
		return ErrNotAllLeaves
	}
	for _, l := range inside {
		if l.size != size/8 {
			return ErrNotAllLeaves
		}
	}
	for i, l := range inside {
		if l.pfn != inside[0].pfn+i*(size/8) {
			return ErrPFN
		}
	}
	if inside[0].pfn%size != 0 {
		return ErrPFN
	}
	for _, l := range inside {
		if l.w != inside[0].w {
			return ErrWInconsistent
		}
	}
	a, d := false, false
	for _, l := range inside {
		a = a || l.a
		d = d || l.d
	}
	// Replace the 8 children with the merged leaf.
	var out []simLeaf
	for _, l := range s.leaves {
		if base <= l.start && l.start < base+size {
			continue
		}
		out = append(out, l)
	}
	s.leaves = out
	s.insert(simLeaf{base, size, inside[0].pfn, inside[0].w, a, d})
	s.epoch++
	return nil
}

func (s *sim) scanDirty() []LeafInfo {
	var out []LeafInfo
	for i := range s.leaves {
		l := &s.leaves[i]
		if l.d {
			out = append(out, LeafInfo{Start: l.start, Size: l.size, PFN: l.pfn, W: l.w, A: l.a, D: true})
			l.d = false
		}
	}
	return out
}

func (s *sim) translate(vpn int) (Translation, bool) {
	if vpn < 0 || vpn >= 512 {
		return Translation{}, false
	}
	l := s.find(vpn)
	if l == nil {
		return Translation{}, false
	}
	return Translation{PFN: l.pfn + vpn - l.start, Size: l.size, W: l.w, A: l.a, D: l.d}, true
}

func (s *sim) leafInfos() []LeafInfo {
	var out []LeafInfo
	for _, l := range s.leaves {
		out = append(out, LeafInfo{Start: l.start, Size: l.size, PFN: l.pfn, W: l.w, A: l.a, D: l.d})
	}
	return out
}

// errString normalizes an error for comparison and logging.
func errString(err error) string {
	if err == nil {
		return "nil"
	}
	for _, e := range []error{ErrInvalidParam, ErrMisaligned, ErrOverlap, ErrQuota, ErrUnmapped,
		ErrWriteProtected, ErrNotTable, ErrNotAllLeaves, ErrPFN, ErrWInconsistent} {
		if errors.Is(err, e) {
			return fmt.Sprintf("%v", e)
		}
	}
	return fmt.Sprintf("UNKNOWN(%v)", err)
}
