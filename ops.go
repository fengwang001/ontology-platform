package ontology

func (m *Manager) Mmap(hint int64, length int64, perm int, flags int, file int64, off int64) (int64, error) {
	if length < 1 || perm < 0 || perm > 7 || flags < 0 || flags&^(Fixed|NoReplace|GrowsDown) != 0 ||
		(flags&NoReplace != 0 && flags&Fixed == 0) ||
		(flags&GrowsDown != 0 && file != 0) ||
		file < 0 || off < 0 || hint < 0 {
		return 0, invalidArgument()
	}
	end := hint + length
	if flags&Fixed != 0 && (hint < m.low || end > m.high) {
		return 0, invalidArgument()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.tree.list()

	if flags&Fixed != 0 {
		if intersectsAny(current, hint, end) {
			if flags&NoReplace != 0 {
				return 0, ErrExists
			}
			removed, splits, _, affected := removeRange(current, hint, end)
			if !affected || len(current)+splits > m.maxV {
				return 0, ErrTooMany
			}
			candidate := m.newVMA(hint, end, perm, flags, file, off)
			result, merges := mergeInserted(removed, candidate)
			if merges == 0 && len(removed)+1 > m.maxV {
				return 0, ErrTooMany
			}
			m.commit(current, result, hint, end)
			return hint, nil
		}
		candidate := m.newVMA(hint, end, perm, flags, file, off)
		result, merges := mergeInserted(current, candidate)
		if merges == 0 && len(current)+1 > m.maxV {
			return 0, ErrTooMany
		}
		m.commit(current, result, hint, end)
		return hint, nil
	}

	start := int64(0)
	haveStart := false
	if hint != 0 && hint >= m.low && end <= m.high && m.blocked.hasFreeRun(hint, end, length) {
		start = hint
		haveStart = true
	} else {
		candidate, ok := m.blocked.rightmostFree(length)
		if !ok || candidate < m.low || candidate+length > m.high {
			return 0, ErrNoSpace
		}
		start = candidate
		haveStart = true
	}
	if !haveStart {
		return 0, ErrNoSpace
	}

	candidate := m.newVMA(start, start+length, perm, flags, file, off)
	result, merges := mergeInserted(current, candidate)
	if merges == 0 && len(current)+1 > m.maxV {
		return 0, ErrTooMany
	}
	m.commit(current, result, start, start+length)
	return start, nil
}

func (m *Manager) Munmap(start, length int64) (int64, error) {
	if length < 1 || start < m.low || start+length > m.high {
		return 0, invalidArgument()
	}
	end := start + length
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.tree.list()
	result, splits, pages, affected := removeRange(current, start, end)
	if !affected {
		return 0, nil
	}
	if len(current)+splits > m.maxV {
		return 0, ErrTooMany
	}
	m.commit(current, result, start, end)
	return pages, nil
}

func (m *Manager) Mprotect(start, length int64, perm int) error {
	if length < 1 || perm < 0 || perm > 7 || start < m.low || start+length > m.high {
		return invalidArgument()
	}
	end := start + length
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.tree.list()
	if coveragePages(current, start, end) != length {
		return ErrNoMem
	}

	next := make([]VMA, 0, len(current)+2)
	splits := 0
	changed := false
	for _, v := range current {
		if !intervalsOverlap(v.Start, v.End, start, end) {
			next = append(next, v)
			continue
		}
		if v.Perm == perm {
			next = append(next, v)
			continue
		}
		changed = true
		parts := clipVMA(v, start, end)
		splits += len(parts) - 1
		if len(current)+splits > m.maxV {
			return ErrTooMany
		}
		for _, part := range parts {
			if intervalsOverlap(part.Start, part.End, start, end) {
				part.Perm = perm
			}
			next = append(next, part)
		}
	}
	if !changed {
		return nil
	}

	low, high := start, end
	if lower, ok := predecessor(current, start); ok && lower.End == start {
		low = lower.Start
	}
	if upper, ok := successor(current, end); ok && upper.Start == end {
		high = upper.End
	}
	merged := mergeWindow(next, low, high)
	m.commit(current, merged, low, high)
	return nil
}

func (m *Manager) Grow(addr int64) (int64, error) {
	if addr < m.low || addr >= m.high {
		return 0, invalidArgument()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.tree.list()
	if _, ok := findInList(current, addr); ok {
		return 0, ErrMapped
	}
	stackIndex := -1
	for i, v := range current {
		if v.Start > addr {
			stackIndex = i
			break
		}
	}
	if stackIndex < 0 || !current[stackIndex].GrowsDown {
		return 0, ErrSegv
	}
	stack := current[stackIndex]
	if stack.End-addr > m.maxStack {
		return 0, ErrStackLimit
	}
	lowerEnd := m.low
	if lower, ok := predecessor(current, addr); ok {
		lowerEnd = lower.End
	}
	if addr-lowerEnd < m.guard {
		return 0, ErrNoRoom
	}
	result := make([]VMA, 0, len(current))
	for i, v := range current {
		if i == stackIndex {
			v.Start = addr
			v.GrowsDown = true
		}
		result = append(result, v)
	}
	m.commit(current, result, addr, stack.End)
	return addr, nil
}

func (m *Manager) Find(addr int64) (VMA, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.tree.find(addr)
	m.visited += m.tree.visited
	return v, ok
}
