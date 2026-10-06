package ftl

func (f *FTL) maybeWearLevelLocked() {
	minErase, maxErase, hasRange := f.eraseRangeLocked()
	if !hasRange || maxErase-minErase <= f.cfg.WearThreshold {
		return
	}

	blockID, ok := f.selectColdBlockLocked()
	if !ok || !f.canEvacuateLocked(blockID) {
		return
	}

	f.prepareEvacuationLocked(blockID)
	if !f.evacuateBlockLocked(blockID) {
		return
	}
	f.eraseBlockLocked(blockID)
}

func (f *FTL) eraseRangeLocked() (int, int, bool) {
	minErase := -1
	maxErase := -1
	for _, current := range f.blocks {
		if current.state == BlockRetired {
			continue
		}
		if minErase < 0 || current.eraseCount < minErase {
			minErase = current.eraseCount
		}
		if current.eraseCount > maxErase {
			maxErase = current.eraseCount
		}
	}
	return minErase, maxErase, minErase >= 0
}

func (f *FTL) selectColdBlockLocked() (int, bool) {
	for {
		id, ok := f.coldBlocks.peek()
		if !ok {
			return 0, false
		}
		if f.blocks[id].state != BlockFull {
			f.coldBlocks.pop()
			continue
		}
		return id, true
	}
}
