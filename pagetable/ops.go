package pagetable

// Map maps the region [vpn, vpn+size) to contiguous physical pages starting
// at pfn. size must be 1, 8 or 64 and both vpn and pfn must be aligned to
// size. The region must not overlap any existing mapping. Missing tables on
// the path are created on demand; the operation is rejected when the peak
// table usage would exceed the quota. Errors are reported in the order:
// invalid parameter, misaligned, overlap, quota.
func (m *Mapper) Map(vpn, size, pfn int, w bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if size != 1 && size != 8 && size != 64 {
		return ErrInvalidParam
	}
	if vpn < 0 || vpn >= VpnCount || pfn < 0 || pfn > MaxPFN {
		return ErrInvalidParam
	}
	if vpn%size != 0 || pfn%size != 0 {
		return ErrMisaligned
	}
	// Overlap check and count of tables that must be created.
	t := &m.root
	missing := 0
scan:
	for _, cover := range [3]int{rootCover, l1Cover, l0Cover} {
		s := &t.slots[vpn/cover%RootSlots]
		if cover == size {
			if s.kind != kindEmpty {
				return ErrOverlap
			}
			break
		}
		switch s.kind {
		case kindLeaf:
			return ErrOverlap
		case kindEmpty:
			missing++
			// All deeper levels down to the target are missing too.
			for _, c2 := range [3]int{rootCover, l1Cover, l0Cover} {
				if c2 >= cover {
					continue
				}
				if c2 == size {
					break scan
				}
				missing++
			}
			break scan
		default:
			t = s.child
		}
	}
	if m.root.tables-1+missing > m.quota {
		return ErrQuota
	}
	// Create the path and insert the leaf.
	var chain [3]*table
	t = &m.root
	depth := 0
	for _, cover := range [3]int{rootCover, l1Cover, l0Cover} {
		chain[depth] = t
		depth++
		s := &t.slots[vpn/cover%RootSlots]
		if cover == size {
			*s = slot{kind: kindLeaf, pfn: pfn, size: size, w: w}
			break
		}
		if s.kind == kindEmpty {
			nt := &table{}
			nt.recompute()
			*s = slot{kind: kindTable, child: nt}
		}
		t = s.child
	}
	for i := depth - 1; i >= 0; i-- {
		chain[i].recompute()
	}
	m.epoch++
	return nil
}

// Touch marks the leaf covering vpn as accessed; a write touch additionally
// requires the leaf to be writable and marks it dirty. Errors in order:
// invalid parameter, unmapped, write protected. A rejected touch changes
// nothing.
func (m *Mapper) Touch(vpn int, write bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if vpn < 0 || vpn >= VpnCount {
		return ErrInvalidParam
	}
	s := m.findLeaf(vpn)
	if s == nil {
		return ErrUnmapped
	}
	if write && !s.w {
		return ErrWriteProtected
	}
	s.a = true
	if write {
		s.d = true
	}
	return nil
}

// ScanDirty returns every dirty leaf ordered by start address and clears its
// D bit. It does not change any other state.
func (m *Mapper) ScanDirty() []LeafInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []LeafInfo
	var walk func(t *table, base, cover int)
	walk = func(t *table, base, cover int) {
		for i := range t.slots {
			s := &t.slots[i]
			b := base + i*cover
			switch s.kind {
			case kindLeaf:
				if s.d {
					out = append(out, LeafInfo{Start: b, Size: s.size, PFN: s.pfn, W: s.w, A: s.a, D: true})
					s.d = false
				}
			case kindTable:
				walk(s.child, b, cover/RootSlots)
			}
		}
	}
	walk(&m.root, 0, rootCover)
	return out
}

// Promote merges the size-page region aligned to size that contains vpn into
// a single leaf. size must be 8 or 64. The slot must point to a table whose
// 8 children are all leaves of size/8 pages with contiguous pfns, a base pfn
// aligned to size and identical W bits. The merged leaf's A (resp. D) is the
// logical OR of the children's; W is unchanged. Errors in order: invalid
// parameter, misaligned, not a table, not all leaves, pfn, W inconsistent.
func (m *Mapper) Promote(vpn, size int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cPromote = 0
	if size != 8 && size != 64 {
		return ErrInvalidParam
	}
	if vpn < 0 || vpn >= VpnCount {
		return ErrInvalidParam
	}
	if vpn%size != 0 {
		return ErrMisaligned
	}
	var parent *table
	var idx int
	if size == 64 {
		parent, idx = &m.root, vpn/rootCover
	} else {
		rs := &m.root.slots[vpn/rootCover]
		if rs.kind != kindTable {
			return ErrNotTable
		}
		parent, idx = rs.child, vpn/l1Cover%RootSlots
	}
	s := &parent.slots[idx]
	if s.kind != kindTable {
		return ErrNotTable
	}
	t := s.child
	// Snapshot the 8 children once, then evaluate every rejection class from
	// the snapshot so the slot examination stays within 8.
	var kids [RootSlots]slot
	for i := 0; i < RootSlots; i++ {
		m.cPromote++
		kids[i] = t.slots[i]
	}
	for i := 0; i < RootSlots; i++ {
		if kids[i].kind != kindLeaf {
			return ErrNotAllLeaves
		}
	}
	childSize := size / RootSlots
	base := kids[0].pfn
	for i := 0; i < RootSlots; i++ {
		if kids[i].pfn != base+i*childSize {
			return ErrPFN
		}
	}
	if base%size != 0 {
		return ErrPFN
	}
	for i := 1; i < RootSlots; i++ {
		if kids[i].w != kids[0].w {
			return ErrWInconsistent
		}
	}
	a, d := false, false
	for i := 0; i < RootSlots; i++ {
		a = a || kids[i].a
		d = d || kids[i].d
	}
	*s = slot{kind: kindLeaf, pfn: base, size: size, w: kids[0].w, a: a, d: d}
	parent.recompute()
	m.root.recompute()
	m.epoch++
	return nil
}
