package ftl

func (f *FTL) collectGarbageLocked() {
	performedCollection := false
	for f.freeBlocks.len() < f.cfg.LowWatermark {
		victimID, ok := f.selectVictimLocked()
		if !ok {
			break
		}
		if !f.canEvacuateLocked(victimID) {
			break
		}

		f.prepareEvacuationLocked(victimID)
		if !f.evacuateBlockLocked(victimID) {
			break
		}
		f.eraseBlockLocked(victimID)
		performedCollection = true

		if f.freeBlocks.len() >= f.cfg.HighWatermark {
			break
		}
	}

	if performedCollection {
		f.maybeWearLevelLocked()
	}
}

func (f *FTL) selectVictimLocked() (int, bool) {
	for {
		id, ok := f.victimBlocks.peek()
		if !ok {
			return 0, false
		}
		if f.blocks[id].state != BlockFull || f.blocks[id].validPages >= f.cfg.PagesPerBlock {
			f.victimBlocks.pop()
			continue
		}
		return id, true
	}
}

func (f *FTL) canEvacuateLocked(blockID int) bool {
	available := 0
	if f.active >= 0 {
		available = f.cfg.PagesPerBlock - f.blocks[f.active].nextPage
	}
	available += f.freeBlocks.len() * f.cfg.PagesPerBlock
	return available >= f.blocks[blockID].validPages
}

func (f *FTL) prepareEvacuationLocked(blockID int) {
	f.victimBlocks.remove(blockID)
	f.blocks[blockID].state = BlockActive
	f.coldBlocks.remove(blockID)
}

func (f *FTL) evacuateBlockLocked(blockID int) bool {
	for pageID := 0; pageID < f.cfg.PagesPerBlock; pageID++ {
		current := f.pages[blockID][pageID]
		if current.state != PageValid {
			continue
		}
		lpn := current.lpn
		if !f.programRelocationLocked(lpn) {
			return false
		}
		f.pages[blockID][pageID] = page{state: PageInvalid, lpn: -1}
	}
	return true
}

func (f *FTL) programRelocationLocked(lpn int) bool {
	if f.active < 0 || f.blocks[f.active].nextPage >= f.cfg.PagesPerBlock {
		if !f.allocateActiveLocked() {
			return false
		}
	}

	blockID := f.active
	pageID := f.blocks[blockID].nextPage
	f.pages[blockID][pageID] = page{state: PageValid, lpn: lpn}
	f.blocks[blockID].nextPage++
	f.blocks[blockID].validPages++
	f.mapping[lpn] = blockID*f.cfg.PagesPerBlock + pageID
	f.physicalProgram++

	if f.blocks[blockID].nextPage == f.cfg.PagesPerBlock {
		f.blocks[blockID].state = BlockFull
		f.victimBlocks.push(blockID)
		f.coldBlocks.push(blockID)
		f.active = -1
	}
	return true
}

func (f *FTL) eraseBlockLocked(blockID int) {
	for pageID := range f.pages[blockID] {
		f.pages[blockID][pageID] = page{state: PageFree, lpn: -1}
	}
	f.blocks[blockID].nextPage = 0
	f.blocks[blockID].validPages = 0
	f.blocks[blockID].eraseCount++

	if f.blocks[blockID].eraseCount >= f.cfg.EraseLimit {
		f.blocks[blockID].state = BlockRetired
		f.retiredBlocks++
		f.victimBlocks.remove(blockID)
		f.coldBlocks.remove(blockID)
		return
	}

	f.blocks[blockID].state = BlockFree
	f.freeBlocks.add(f.blocks[blockID].eraseCount, blockID)
}
