package ftl

type NaiveFTL struct {
	cfg     Config
	blocks  []BlockInfo
	pages   [][]PageInfo
	mapping []int
	active  int

	mappedPages      int
	retiredBlocks    int
	logicalWrites    int
	physicalPrograms int
}

func NewNaive(cfg Config) (*NaiveFTL, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	n := &NaiveFTL{
		cfg:     cfg,
		blocks:  make([]BlockInfo, cfg.BlockCount),
		pages:   make([][]PageInfo, cfg.BlockCount),
		mapping: make([]int, cfg.LogicalPages),
		active:  -1,
	}
	for blockID := range n.blocks {
		n.blocks[blockID] = BlockInfo{State: BlockFree}
		n.pages[blockID] = make([]PageInfo, cfg.PagesPerBlock)
		for pageID := range n.pages[blockID] {
			n.pages[blockID][pageID] = PageInfo{LPN: -1}
		}
	}
	for lpn := range n.mapping {
		n.mapping[lpn] = -1
	}
	return n, nil
}

func (n *NaiveFTL) Write(lpn int) error {
	if lpn < 0 || lpn >= n.cfg.LogicalPages {
		return ErrInvalidArgument
	}
	limit := (n.cfg.BlockCount - n.retiredBlocks - n.cfg.LowWatermark) * n.cfg.PagesPerBlock
	if limit < 0 {
		limit = 0
	}
	if n.mapping[lpn] < 0 && n.mappedPages >= limit {
		return ErrNoSpace
	}
	if n.active < 0 && n.freeCount() == 0 {
		return ErrNoSpace
	}

	oldPhysical := n.mapping[lpn]
	n.program(lpn)
	if oldPhysical >= 0 {
		n.invalidate(oldPhysical)
	} else {
		n.mappedPages++
	}
	n.logicalWrites++
	n.collectGarbage()
	return nil
}

func (n *NaiveFTL) Read(lpn int) (PhysicalPage, error) {
	if lpn < 0 || lpn >= n.cfg.LogicalPages {
		return PhysicalPage{}, ErrInvalidArgument
	}
	physical := n.mapping[lpn]
	if physical < 0 {
		return PhysicalPage{}, ErrUnwritten
	}
	return PhysicalPage{
		Block: physical / n.cfg.PagesPerBlock,
		Page:  physical % n.cfg.PagesPerBlock,
	}, nil
}

func (n *NaiveFTL) Discard(lpn int) error {
	if lpn < 0 || lpn >= n.cfg.LogicalPages {
		return ErrInvalidArgument
	}
	physical := n.mapping[lpn]
	if physical < 0 {
		return nil
	}
	n.invalidate(physical)
	n.mapping[lpn] = -1
	n.mappedPages--
	return nil
}

func (n *NaiveFTL) Stats() Stats {
	free := 0
	counts := make([]int, len(n.blocks))
	for i, block := range n.blocks {
		if block.State == BlockFree {
			free++
		}
		counts[i] = block.EraseCount
	}
	return Stats{
		LogicalWrites:    n.logicalWrites,
		PhysicalPrograms: n.physicalPrograms,
		FreeBlocks:       free,
		RetiredBlocks:    n.retiredBlocks,
		EraseCounts:      counts,
	}
}

func (n *NaiveFTL) Blocks() []BlockInfo {
	return append([]BlockInfo(nil), n.blocks...)
}

func (n *NaiveFTL) PhysicalPages() [][]PageInfo {
	result := make([][]PageInfo, len(n.pages))
	for i := range n.pages {
		result[i] = append([]PageInfo(nil), n.pages[i]...)
	}
	return result
}

func (n *NaiveFTL) WriteAmplification() (int, int) {
	return n.physicalPrograms, n.logicalWrites
}

func (n *NaiveFTL) collectGarbage() {
	recycled := false
	for n.freeCount() < n.cfg.LowWatermark {
		victim, ok := n.victim(-1)
		if !ok || !n.canMove(victim) {
			break
		}
		n.moveBlock(victim)
		n.erase(victim)
		recycled = true
		if n.freeCount() >= n.cfg.HighWatermark {
			break
		}
	}
	if recycled {
		n.wearLevel()
	}
}

func (n *NaiveFTL) wearLevel() {
	minErase, maxErase, found := n.eraseRange()
	if !found || maxErase-minErase <= n.cfg.WearThreshold {
		return
	}
	cold := -1
	for blockID, block := range n.blocks {
		if block.State != BlockFull {
			continue
		}
		if cold < 0 || block.EraseCount < n.blocks[cold].EraseCount ||
			(block.EraseCount == n.blocks[cold].EraseCount && blockID < cold) {
			cold = blockID
		}
	}
	if cold >= 0 && n.canMove(cold) {
		n.moveBlock(cold)
		n.erase(cold)
	}
}

func (n *NaiveFTL) victim(exclude int) (int, bool) {
	victim := -1
	for blockID, block := range n.blocks {
		if blockID == exclude || block.State != BlockFull || block.ValidPages >= n.cfg.PagesPerBlock {
			continue
		}
		if victim < 0 ||
			block.ValidPages < n.blocks[victim].ValidPages ||
			(block.ValidPages == n.blocks[victim].ValidPages && block.EraseCount < n.blocks[victim].EraseCount) ||
			(block.ValidPages == n.blocks[victim].ValidPages &&
				block.EraseCount == n.blocks[victim].EraseCount && blockID < victim) {
			victim = blockID
		}
	}
	return victim, victim >= 0
}

func (n *NaiveFTL) eraseRange() (int, int, bool) {
	minErase := -1
	maxErase := -1
	for _, block := range n.blocks {
		if block.State == BlockRetired {
			continue
		}
		if minErase < 0 || block.EraseCount < minErase {
			minErase = block.EraseCount
		}
		if block.EraseCount > maxErase {
			maxErase = block.EraseCount
		}
	}
	return minErase, maxErase, minErase >= 0
}

func (n *NaiveFTL) canMove(blockID int) bool {
	available := 0
	if n.active >= 0 {
		available = n.cfg.PagesPerBlock - n.blocks[n.active].NextPage
	}
	return available+n.freeCount()*n.cfg.PagesPerBlock >= n.blocks[blockID].ValidPages
}

func (n *NaiveFTL) moveBlock(blockID int) {
	for pageID, current := range n.pages[blockID] {
		if current.State != PageValid {
			continue
		}
		lpn := current.LPN
		n.program(lpn)
		n.pages[blockID][pageID] = PageInfo{State: PageInvalid, LPN: -1}
		n.blocks[blockID].ValidPages--
	}
}

func (n *NaiveFTL) erase(blockID int) {
	for pageID := range n.pages[blockID] {
		n.pages[blockID][pageID] = PageInfo{State: PageFree, LPN: -1}
	}
	n.blocks[blockID].NextPage = 0
	n.blocks[blockID].ValidPages = 0
	n.blocks[blockID].EraseCount++
	if n.blocks[blockID].EraseCount >= n.cfg.EraseLimit {
		n.blocks[blockID].State = BlockRetired
		n.retiredBlocks++
		return
	}
	n.blocks[blockID].State = BlockFree
}

func (n *NaiveFTL) program(lpn int) {
	if n.active < 0 || n.blocks[n.active].NextPage >= n.cfg.PagesPerBlock {
		n.active = n.leastErasedFree()
		n.blocks[n.active].State = BlockActive
	}
	blockID := n.active
	pageID := n.blocks[blockID].NextPage
	n.pages[blockID][pageID] = PageInfo{State: PageValid, LPN: lpn}
	n.blocks[blockID].NextPage++
	n.blocks[blockID].ValidPages++
	n.mapping[lpn] = blockID*n.cfg.PagesPerBlock + pageID
	n.physicalPrograms++
	if n.blocks[blockID].NextPage == n.cfg.PagesPerBlock {
		n.blocks[blockID].State = BlockFull
		n.active = -1
	}
}

func (n *NaiveFTL) leastErasedFree() int {
	selected := -1
	for blockID, block := range n.blocks {
		if block.State != BlockFree {
			continue
		}
		if selected < 0 || block.EraseCount < n.blocks[selected].EraseCount ||
			(block.EraseCount == n.blocks[selected].EraseCount && blockID < selected) {
			selected = blockID
		}
	}
	return selected
}

func (n *NaiveFTL) invalidate(physical int) {
	blockID := physical / n.cfg.PagesPerBlock
	pageID := physical % n.cfg.PagesPerBlock
	if n.pages[blockID][pageID].State != PageValid {
		return
	}
	n.pages[blockID][pageID] = PageInfo{State: PageInvalid, LPN: -1}
	n.blocks[blockID].ValidPages--
}

func (n *NaiveFTL) freeCount() int {
	count := 0
	for _, block := range n.blocks {
		if block.State == BlockFree {
			count++
		}
	}
	return count
}
