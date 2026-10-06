package ftl

import "testing"

func eraseOneBlock(t *testing.T, f *FTL) {
	t.Helper()
	fillBlock(t, f, 0)
	fillBlock(t, f, 4)
	fillBlock(t, f, 8)
	requireErrIs(t, f.Discard(0), nil)
	requireErrIs(t, f.Discard(4), nil)
	writes(t, f, 12, 13, 14, 15, 12)
}

func TestExactLifeEraseRetiresBlock(t *testing.T) {
	cfg := testConfig()
	cfg.EraseLimit = 1
	f := mustNew(t, cfg)

	eraseOneBlock(t, f)

	if f.Blocks()[0].State != BlockRetired || f.Blocks()[0].EraseCount != 1 {
		t.Fatalf("block 0 = %+v, want retired after exactly one erase", f.Blocks()[0])
	}
	if f.Stats().RetiredBlocks != 2 {
		t.Fatalf("retired blocks = %d, want 2", f.Stats().RetiredBlocks)
	}
}

func TestRetirementTightensAdmissionButAllowsOverwrite(t *testing.T) {
	cfg := testConfig()
	cfg.EraseLimit = 1
	f := mustNew(t, cfg)

	eraseOneBlock(t, f)
	retired := f.Stats().RetiredBlocks
	limit := (cfg.BlockCount - retired - cfg.LowWatermark) * cfg.PagesPerBlock
	for f.mappedPages < limit {
		lpn := f.mappedPages
		writes(t, f, lpn)
	}

	nextNew := -1
	for lpn := 0; lpn < cfg.LogicalPages; lpn++ {
		if f.mapping[lpn] < 0 {
			nextNew = lpn
			break
		}
	}
	if nextNew < 0 {
		t.Fatalf("test needs an unmapped LPN")
	}
	before := fullSnapshot(t, f)
	requireErrIs(t, f.Write(nextNew), ErrNoSpace)
	if fullSnapshot(t, f) != before {
		t.Fatalf("post-retirement admission rejection changed state")
	}

	mapped := 0
	for _, physical := range f.mapping {
		if physical >= 0 {
			mapped = physical
		}
	}
	lpn := f.PhysicalPages()[mapped/cfg.PagesPerBlock][mapped%cfg.PagesPerBlock].LPN
	writes(t, f, lpn)
	requirePhysical(t, f, lpn, PhysicalPage{
		Block: f.mapping[lpn] / cfg.PagesPerBlock,
		Page:  f.mapping[lpn] % cfg.PagesPerBlock,
	})
}

func TestWearThresholdEqualDoesNotMove(t *testing.T) {
	f := fullBlocksForWearTest(t)
	setEraseCountsForTest(t, f, []int{2, 2, 2, 0, 0, 0, 0, 0})

	before := fullSnapshot(t, f)
	f.mu.Lock()
	f.maybeWearLevelLocked()
	f.mu.Unlock()

	if fullSnapshot(t, f) != before {
		t.Fatalf("wear difference equal to threshold triggered cold move")
	}
}

func TestWearThresholdOneMoreMovesColdBlock(t *testing.T) {
	f := fullBlocksForWearTest(t)
	setEraseCountsForTest(t, f, []int{3, 3, 3, 0, 0, 0, 0, 0})

	f.mu.Lock()
	f.maybeWearLevelLocked()
	f.mu.Unlock()

	if f.Blocks()[3].EraseCount != 1 {
		t.Fatalf("cold block 3 erase count = %d, want 1", f.Blocks()[3].EraseCount)
	}
	for lpn := 12; lpn < 16; lpn++ {
		physical := f.mapping[lpn]
		if physical/cfgPagesPerBlock(f) == 3 {
			t.Fatalf("LPN %d still maps to erased cold block", lpn)
		}
	}
}

func fullBlocksForWearTest(t *testing.T) *FTL {
	t.Helper()
	cfg := testConfig()
	cfg.BlockCount = 8
	cfg.HighWatermark = 6
	cfg.LogicalPages = 24
	f := mustNew(t, cfg)
	fillBlock(t, f, 0)
	fillBlock(t, f, 4)
	fillBlock(t, f, 8)
	fillBlock(t, f, 12)
	fillBlock(t, f, 16)
	fillBlock(t, f, 20)
	return f
}

func TestRetiredBlockIsNotChosenAsColdOrFree(t *testing.T) {
	cfg := testConfig()
	cfg.EraseLimit = 1
	f := mustNew(t, cfg)
	eraseOneBlock(t, f)

	for _, info := range f.Blocks() {
		if info.State == BlockRetired && info.NextPage != 0 {
			t.Fatalf("retired block retained programmed pages: %+v", info)
		}
	}
	if f.freeBlocks.len() != 0 {
		t.Fatalf("retired blocks remain in allocation heap")
	}
}

func cfgPagesPerBlock(f *FTL) int {
	return f.cfg.PagesPerBlock
}

func setEraseCountsForTest(t *testing.T, f *FTL, counts []int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(counts) != len(f.blocks) {
		t.Fatalf("erase count vector length mismatch")
	}
	for i, count := range counts {
		if f.blocks[i].state == BlockFree {
			f.freeBlocks.remove(f.blocks[i].eraseCount, i)
		}
		f.blocks[i].eraseCount = count
		f.victimBlocks.remove(i)
		f.coldBlocks.remove(i)
		switch f.blocks[i].state {
		case BlockFree:
			f.freeBlocks.add(f.blocks[i].eraseCount, i)
		case BlockFull:
			f.victimBlocks.push(i)
			f.coldBlocks.push(i)
		}
	}
}
