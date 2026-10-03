package pagetable

// Unmap removes every mapped page in [vpn, vpn+n). Unmapped parts of the
// range are ignored. Leaves only partially covered are split first: a leaf
// becomes 8 next-level leaves inheriting pfn, W, A and D. Fully covered
// tables and leaves are released wholesale without walking their slots, and
// tables that become empty are reclaimed immediately, cascading upward.
//
// The peak quota check happens before any mutation: if the split needs s>0
// new tables, the operation is rejected with ErrQuota when U+s exceeds the
// quota; tables freed later in the same operation do not offset the peak.
// Returns the number of pages actually unmapped (0 leaves state untouched).
func (m *Mapper) Unmap(vpn, n int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cUnmap = 0
	if vpn < 0 || vpn >= VpnCount || n < 1 || vpn+n > VpnCount {
		return 0, ErrInvalidParam
	}
	lo, hi := vpn, vpn+n
	rootPlan := &tablePlan{tbl: &m.root}
	splits := m.planUnmap(rootPlan, 0, VpnCount, lo, hi, false)
	if splits > 0 && m.root.tables-1+splits > m.quota {
		return 0, ErrQuota
	}
	removed := applyPlan(rootPlan)
	if removed == 0 {
		return 0, nil
	}
	m.epoch++
	return removed, nil
}

// Action kinds for an unmap plan.
const (
	actRemove  = iota // clear the slot (leaf or whole table subtree)
	actSplit          // split the leaf into 8 children, then apply sub-plan
	actRecurse        // apply sub-plan inside the existing child table
)

type slotAction struct {
	kind int
	idx  int
	sub  *tablePlan
}

// tablePlan describes the actions to perform inside one table. tbl is nil
// for a virtual table that a split will create.
type tablePlan struct {
	tbl     *table
	actions []slotAction
}

// planUnmap inspects the slots of the table covering [base, base+size) that
// intersect [lo, hi) and records the required actions. It returns the number
// of tables that splits would create. Only intersecting slots are examined.
func (m *Mapper) planUnmap(tp *tablePlan, base, size, lo, hi int, virtual bool) int {
	splits := 0
	childSize := size / RootSlots
	for i := 0; i < RootSlots; i++ {
		childBase := base + i*childSize
		if childBase+childSize <= lo || childBase >= hi {
			continue
		}
		m.cUnmap++
		kind := kindLeaf // a virtual table consists of leaves
		var child *table
		if !virtual {
			s := &tp.tbl.slots[i]
			kind = s.kind
			child = s.child
		}
		if kind == kindEmpty {
			continue
		}
		covered := lo <= childBase && childBase+childSize <= hi
		if covered {
			tp.actions = append(tp.actions, slotAction{kind: actRemove, idx: i})
			continue
		}
		// Partial intersection. A 1-page leaf cannot be partial, so any
		// partial leaf here has childSize > 1 and gets split.
		if kind == kindLeaf {
			sub := &tablePlan{}
			splits += 1 + m.planUnmap(sub, childBase, childSize, lo, hi, true)
			tp.actions = append(tp.actions, slotAction{kind: actSplit, idx: i, sub: sub})
		} else {
			sub := &tablePlan{tbl: child}
			splits += m.planUnmap(sub, childBase, childSize, lo, hi, false)
			tp.actions = append(tp.actions, slotAction{kind: actRecurse, idx: i, sub: sub})
		}
	}
	return splits
}

// applyPlan executes a plan and returns the number of pages removed. After
// all actions the table's aggregates are recomputed; callers reclaim tables
// that became empty.
func applyPlan(tp *tablePlan) (removed int) {
	t := tp.tbl
	for _, a := range tp.actions {
		s := &t.slots[a.idx]
		switch a.kind {
		case actRemove:
			if s.kind == kindLeaf {
				removed += s.size
			} else {
				removed += s.child.pages
			}
			*s = slot{}
		case actSplit:
			leaf := *s
			nt := &table{}
			cs := leaf.size / RootSlots
			for i := 0; i < RootSlots; i++ {
				nt.slots[i] = slot{
					kind: kindLeaf,
					pfn:  leaf.pfn + i*cs,
					size: cs,
					w:    leaf.w,
					a:    leaf.a,
					d:    leaf.d,
				}
			}
			nt.recompute()
			*s = slot{kind: kindTable, child: nt}
			a.sub.tbl = nt
			removed += applyPlan(a.sub)
			if nt.empty() {
				*s = slot{}
			}
		case actRecurse:
			child := s.child
			removed += applyPlan(a.sub)
			if child.empty() {
				*s = slot{}
			}
		}
	}
	t.recompute()
	return removed
}
