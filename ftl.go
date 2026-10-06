package ftl

import "sync"

type PageState uint8

const (
	PageFree PageState = iota
	PageValid
	PageInvalid
)

type BlockState uint8

const (
	BlockFree BlockState = iota
	BlockActive
	BlockFull
	BlockRetired
)

type PhysicalPage struct {
	Block int
	Page  int
}

type PageInfo struct {
	State PageState
	LPN   int
}

type BlockInfo struct {
	State      BlockState
	EraseCount int
	NextPage   int
	ValidPages int
}

type Stats struct {
	LogicalWrites    int
	PhysicalPrograms int
	FreeBlocks       int
	RetiredBlocks    int
	EraseCounts      []int
}

type block struct {
	state      BlockState
	eraseCount int
	nextPage   int
	validPages int
}

type page struct {
	state PageState
	lpn   int
}

type FTL struct {
	mu sync.RWMutex

	cfg     Config
	blocks  []block
	pages   [][]page
	mapping []int

	freeBlocks   bucketSet
	victimBlocks indexedHeap
	coldBlocks   indexedHeap

	active          int
	mappedPages     int
	retiredBlocks   int
	logicalWrites   int
	physicalProgram int
}

func New(cfg Config) (*FTL, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	f := &FTL{
		cfg:     cfg,
		blocks:  make([]block, cfg.BlockCount),
		pages:   make([][]page, cfg.BlockCount),
		mapping: make([]int, cfg.LogicalPages),
		active:  -1,
	}
	for blockID := range f.blocks {
		f.blocks[blockID].state = BlockFree
		f.pages[blockID] = make([]page, cfg.PagesPerBlock)
		for pageID := range f.pages[blockID] {
			f.pages[blockID][pageID] = page{state: PageFree, lpn: -1}
		}
	}
	for lpn := range f.mapping {
		f.mapping[lpn] = -1
	}
	f.freeBlocks = newBucketSet(cfg.BlockCount, cfg.EraseLimit)
	f.victimBlocks = newIndexedHeap(cfg.BlockCount, f.lessVictimBlock)
	f.coldBlocks = newIndexedHeap(cfg.BlockCount, f.lessColdBlock)
	for blockID := range f.blocks {
		f.freeBlocks.add(0, blockID)
	}
	return f, nil
}

func (f *FTL) Write(lpn int) error {
	if lpn < 0 || lpn >= f.cfg.LogicalPages {
		return ErrInvalidArgument
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.mapping[lpn] < 0 && f.mappedPages >= f.admissionLimitLocked() {
		return ErrNoSpace
	}
	if f.active < 0 && f.freeBlocks.len() == 0 {
		return ErrNoSpace
	}

	if err := f.programLogicalLocked(lpn); err != nil {
		return err
	}
	f.logicalWrites++
	f.collectGarbageLocked()
	return nil
}

func (f *FTL) Read(lpn int) (PhysicalPage, error) {
	if lpn < 0 || lpn >= f.cfg.LogicalPages {
		return PhysicalPage{}, ErrInvalidArgument
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	physical := f.mapping[lpn]
	if physical < 0 {
		return PhysicalPage{}, ErrUnwritten
	}
	blockID := physical / f.cfg.PagesPerBlock
	pageID := physical % f.cfg.PagesPerBlock
	return PhysicalPage{Block: blockID, Page: pageID}, nil
}

func (f *FTL) Discard(lpn int) error {
	if lpn < 0 || lpn >= f.cfg.LogicalPages {
		return ErrInvalidArgument
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.invalidateMappingLocked(lpn)
	return nil
}

func (f *FTL) Stats() Stats {
	f.mu.RLock()
	defer f.mu.RUnlock()

	eraseCounts := append([]int(nil), f.eraseCountsLocked()...)
	return Stats{
		LogicalWrites:    f.logicalWrites,
		PhysicalPrograms: f.physicalProgram,
		FreeBlocks:       f.freeBlocks.len(),
		RetiredBlocks:    f.retiredBlocks,
		EraseCounts:      eraseCounts,
	}
}

func (f *FTL) WriteAmplification() (int, int) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.physicalProgram, f.logicalWrites
}

func (f *FTL) Blocks() []BlockInfo {
	f.mu.RLock()
	defer f.mu.RUnlock()

	infos := make([]BlockInfo, len(f.blocks))
	for i, current := range f.blocks {
		infos[i] = BlockInfo{
			State:      current.state,
			EraseCount: current.eraseCount,
			NextPage:   current.nextPage,
			ValidPages: current.validPages,
		}
	}
	return infos
}

func (f *FTL) PhysicalPages() [][]PageInfo {
	f.mu.RLock()
	defer f.mu.RUnlock()

	result := make([][]PageInfo, len(f.pages))
	for blockID := range f.pages {
		result[blockID] = make([]PageInfo, len(f.pages[blockID]))
		for pageID, current := range f.pages[blockID] {
			result[blockID][pageID] = PageInfo{State: current.state, LPN: current.lpn}
		}
	}
	return result
}

func (f *FTL) lessFreeBlock(a, b int) bool {
	if f.blocks[a].eraseCount != f.blocks[b].eraseCount {
		return f.blocks[a].eraseCount < f.blocks[b].eraseCount
	}
	return a < b
}

func (f *FTL) lessVictimBlock(a, b int) bool {
	if f.blocks[a].validPages != f.blocks[b].validPages {
		return f.blocks[a].validPages < f.blocks[b].validPages
	}
	if f.blocks[a].eraseCount != f.blocks[b].eraseCount {
		return f.blocks[a].eraseCount < f.blocks[b].eraseCount
	}
	return a < b
}

func (f *FTL) lessColdBlock(a, b int) bool {
	if f.blocks[a].eraseCount != f.blocks[b].eraseCount {
		return f.blocks[a].eraseCount < f.blocks[b].eraseCount
	}
	return a < b
}

func (f *FTL) eraseCountsLocked() []int {
	counts := make([]int, len(f.blocks))
	for i := range f.blocks {
		counts[i] = f.blocks[i].eraseCount
	}
	return counts
}

func (f *FTL) admissionLimitLocked() int {
	usableBlocks := f.cfg.BlockCount - f.retiredBlocks
	availableBlocks := usableBlocks - f.cfg.LowWatermark
	if availableBlocks < 0 {
		availableBlocks = 0
	}
	return availableBlocks * f.cfg.PagesPerBlock
}

func (f *FTL) programLogicalLocked(lpn int) error {
	oldPhysical := f.mapping[lpn]
	if err := f.programAtActiveLocked(lpn); err != nil {
		return err
	}
	if oldPhysical >= 0 {
		f.markPhysicalInvalidLocked(oldPhysical)
	} else {
		f.mappedPages++
	}
	return nil
}

func (f *FTL) programAtActiveLocked(lpn int) error {
	if f.active < 0 || f.blocks[f.active].nextPage >= f.cfg.PagesPerBlock {
		if !f.allocateActiveLocked() {
			return ErrNoSpace
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
	return nil
}

func (f *FTL) allocateActiveLocked() bool {
	blockID, ok := f.freeBlocks.pop()
	if !ok {
		return false
	}
	f.blocks[blockID].state = BlockActive
	f.active = blockID
	return true
}

func (f *FTL) invalidateMappingLocked(lpn int) {
	physical := f.mapping[lpn]
	if physical < 0 {
		return
	}
	f.markPhysicalInvalidLocked(physical)
	f.mapping[lpn] = -1
	f.mappedPages--
}

func (f *FTL) markPhysicalInvalidLocked(physical int) {
	blockID := physical / f.cfg.PagesPerBlock
	pageID := physical % f.cfg.PagesPerBlock
	if f.pages[blockID][pageID].state != PageValid {
		return
	}
	f.pages[blockID][pageID].state = PageInvalid
	f.pages[blockID][pageID].lpn = -1
	f.blocks[blockID].validPages--
	if f.victimBlocks.contains(blockID) {
		f.victimBlocks.fix(blockID)
	}
}
