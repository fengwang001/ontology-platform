package liveness

// LiveIn returns the lexicographically sorted live-in variable set of a block.
// It fails with ErrNotSealed before sealing or ErrNoSuchBlock for an unknown id.
func (a *Analyzer) LiveIn(id int) ([]string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.sealed {
		return nil, ErrNotSealed
	}
	b, ok := a.blocks[id]
	if !ok {
		return nil, ErrNoSuchBlock
	}
	return sortedSlice(b.in), nil
}

// LiveOut returns the lexicographically sorted live-out variable set of a block.
func (a *Analyzer) LiveOut(id int) ([]string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.sealed {
		return nil, ErrNotSealed
	}
	b, ok := a.blocks[id]
	if !ok {
		return nil, ErrNoSuchBlock
	}
	return sortedSlice(b.out), nil
}

// EntryLiveIn returns the live-in set of the entry block (the first block
// successfully added), i.e. variables that may be used while undefined.
func (a *Analyzer) EntryLiveIn() ([]string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.sealed {
		return nil, ErrNotSealed
	}
	return sortedSlice(a.blocks[a.entry].in), nil
}

// UpwardExposed returns the UE set of a block (uses occurring before any
// definition inside the block), lexicographically sorted.
func (a *Analyzer) UpwardExposed(id int) ([]string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.sealed {
		return nil, ErrNotSealed
	}
	b, ok := a.blocks[id]
	if !ok {
		return nil, ErrNoSuchBlock
	}
	return sortedSlice(b.ue), nil
}

// Defined returns the Def set of a block, lexicographically sorted.
func (a *Analyzer) Defined(id int) ([]string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.sealed {
		return nil, ErrNotSealed
	}
	b, ok := a.blocks[id]
	if !ok {
		return nil, ErrNoSuchBlock
	}
	return sortedSlice(b.def), nil
}

// EntryID returns the id of the entry (first added) block.
func (a *Analyzer) EntryID() (int, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if len(a.order) == 0 {
		return 0, ErrNoBlocks
	}
	return a.entry, nil
}

// Results returns liveness results for every block in insertion order. All
// variable slices are lexicographically sorted.
func (a *Analyzer) Results() ([]BlockResult, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.sealed {
		return nil, ErrNotSealed
	}

	out := make([]BlockResult, 0, len(a.order))
	for _, id := range a.order {
		b := a.blocks[id]
		out = append(out, BlockResult{
			ID:            id,
			UpwardExposed: sortedSlice(b.ue),
			Defined:       sortedSlice(b.def),
			LiveIn:        sortedSlice(b.in),
			LiveOut:       sortedSlice(b.out),
			Successors:    append([]int(nil), b.spec.Successors...),
		})
	}
	return out, nil
}
