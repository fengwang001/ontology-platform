package ftl

import (
	"errors"
	"testing"
)

// baseCfg 8 块 × 4 页，低水位 2、高水位 3，寿命与磨损阈值默认不生效。
func baseCfg() Config {
	return Config{
		NumBlocks:     8,
		PagesPerBlock: 4,
		LogicalPages:  64,
		LowWatermark:  2,
		HighWatermark: 3,
		EraseLimit:    1000,
		WearThreshold: 1000,
	}
}

func mustNew(t *testing.T, cfg Config) *FTL {
	t.Helper()
	f, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return f
}

func mustWrite(t *testing.T, f *FTL, lpn uint64) {
	t.Helper()
	if err := f.Write(lpn, []byte{byte(lpn)}); err != nil {
		t.Fatalf("Write(%d): %v", lpn, err)
	}
}

func mustDiscard(t *testing.T, f *FTL, lpn uint64) {
	t.Helper()
	if err := f.Discard(lpn); err != nil {
		t.Fatalf("Discard(%d): %v", lpn, err)
	}
}

func mustRead(t *testing.T, f *FTL, lpn uint64, want byte) {
	t.Helper()
	got, err := f.Read(lpn)
	if err != nil {
		t.Fatalf("Read(%d): %v", lpn, err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Read(%d) = %v, want [%d]", lpn, got, want)
	}
}

func eraseCounts(f *FTL) []int { return f.Stats().EraseCounts }

func totalErases(f *FTL) int {
	sum := 0
	for _, c := range eraseCounts(f) {
		sum += c
	}
	return sum
}

// TestConfigValidation 配置合法性校验。
func TestConfigValidation(t *testing.T) {
	good := baseCfg()
	if _, err := New(good); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"NumBlocks=0", func(c *Config) { c.NumBlocks = 0 }},
		{"PagesPerBlock=0", func(c *Config) { c.PagesPerBlock = 0 }},
		{"LogicalPages=0", func(c *Config) { c.LogicalPages = 0 }},
		{"LowWatermark<2", func(c *Config) { c.LowWatermark = 1 }},
		{"High<=Low", func(c *Config) { c.HighWatermark = c.LowWatermark }},
		{"High>=NumBlocks", func(c *Config) { c.HighWatermark = c.NumBlocks }},
		{"EraseLimit=0", func(c *Config) { c.EraseLimit = 0 }},
		{"WearThreshold<0", func(c *Config) { c.WearThreshold = -1 }},
	}
	for _, tc := range cases {
		cfg := good
		tc.mutate(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

// TestErrorPrecedence 错误只报次序最靠前的一类：参数非法 > 未写入 > 空间耗尽。
func TestErrorPrecedence(t *testing.T) {
	f := mustNew(t, baseCfg())

	if _, err := f.Read(64); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Read out-of-range = %v, want ErrInvalidArgument", err)
	}
	if _, err := f.Read(0); !errors.Is(err, ErrNotWritten) {
		t.Fatalf("Read unwritten = %v, want ErrNotWritten", err)
	}
	if err := f.Write(100, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Write out-of-range = %v, want ErrInvalidArgument", err)
	}
	if err := f.Discard(100); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Discard out-of-range = %v, want ErrInvalidArgument", err)
	}
	if err := f.Discard(3); err != nil {
		t.Fatalf("Discard unmapped must be no-op, got %v", err)
	}

	// 填满准入上限 (8-2)*4 = 24 后，越界写仍报参数非法而非空间耗尽。
	for i := 0; i < 24; i++ {
		mustWrite(t, f, uint64(i))
	}
	if err := f.Write(100, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Write out-of-range at full admission = %v, want ErrInvalidArgument", err)
	}
	if err := f.Write(24, nil); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Write beyond admission = %v, want ErrNoSpace", err)
	}
	// 覆盖写已映射逻辑页不受准入限制。
	mustWrite(t, f, 0)
	mustRead(t, f, 0, 0)
}

// TestWatermarkBoundary 空闲块数恰等于低水位不触发回收，少一则触发。
func TestWatermarkBoundary(t *testing.T) {
	f := mustNew(t, baseCfg())

	// 24 次写入分配 6 个块，空闲块数 = 8-6 = 2，恰等于低水位。
	for i := 0; i < 24; i++ {
		mustWrite(t, f, uint64(i))
	}
	if got := f.Stats().FreeBlocks; got != 2 {
		t.Fatalf("FreeBlocks = %d, want 2 (== low watermark)", got)
	}
	if totalErases(f) != 0 {
		t.Fatalf("GC must not trigger at free == low watermark, erases = %v", eraseCounts(f))
	}

	// 再一次覆盖写：分配块 6，空闲块数降为 1 < 低水位，触发回收。
	// 块 0 中 LPN0 失效（有效页 3），成为唯一有收益的受害块；
	// 其 3 个有效页搬入块 6 后块 0 被擦除，空闲回到 2。
	mustWrite(t, f, 0)
	ec := eraseCounts(f)
	want := []int{1, 0, 0, 0, 0, 0, 0, 0}
	for i := range want {
		if ec[i] != want[i] {
			t.Fatalf("erases = %v, want %v (exactly one reclaim round)", ec, want)
		}
	}
	if got := f.Stats().FreeBlocks; got != 2 {
		t.Fatalf("FreeBlocks after GC = %d, want 2 (no further beneficial victim)", got)
	}
	// 覆盖写后的新数据可读，未覆盖的旧数据仍在。
	for i := 1; i < 24; i++ {
		mustRead(t, f, uint64(i), byte(i))
	}
}

// TestVictimTieBreakByBlockID 受害块有效页数并列、擦除次数并列时取块号较小者。
//
// 布局推演（8 块 × 4 页，低 2 高 3）：
// 写 LPN0-15 填满块 0-3；丢弃 LPN0、LPN4 使块 0、块 1 有效页均为 3；
// 写 LPN16-23 填满块 4、5（空闲=2，不触发）；覆盖写 LPN16 使块 4 有效页
// 也为 3，并分配块 6 使空闲=1 触发回收。三个候选有效页均为 3、擦除次数
// 均为 0，应按块号顺序回收 0、1、4。
func TestVictimTieBreakByBlockID(t *testing.T) {
	f := mustNew(t, baseCfg())

	for i := 0; i < 16; i++ {
		mustWrite(t, f, uint64(i))
	}
	mustDiscard(t, f, 0)
	mustDiscard(t, f, 4)
	for i := 16; i < 24; i++ {
		mustWrite(t, f, uint64(i))
	}
	if totalErases(f) != 0 {
		t.Fatalf("GC must not trigger at free == low, erases = %v", eraseCounts(f))
	}
	mustWrite(t, f, 16) // 覆盖写，分配块 6，空闲=1，触发回收

	ec := eraseCounts(f)
	want := []int{1, 1, 0, 0, 1, 0, 0, 0}
	for i := range want {
		if ec[i] != want[i] {
			t.Fatalf("erases = %v, want %v (tie broken by smaller block id)", ec, want)
		}
	}
	// 被搬移的有效页数据保持可读。
	for _, lpn := range []uint64{1, 2, 3, 5, 6, 7, 17, 18, 19} {
		mustRead(t, f, lpn, byte(lpn))
	}
	if _, err := f.Read(0); !errors.Is(err, ErrNotWritten) {
		t.Fatalf("discarded LPN 0 = %v, want ErrNotWritten", err)
	}
}

// TestVictimTieBreakByErases 有效页数并列时先取擦除次数较少者，
// 即使其块号更大。直接白盒构造候选集验证 pickVictim。
func TestVictimTieBreakByErases(t *testing.T) {
	f := mustNew(t, baseCfg())

	// 填满块 0-2 并各留 1 个失效页（有效页均为 3）。
	for i := 0; i < 12; i++ {
		mustWrite(t, f, uint64(i))
	}
	mustDiscard(t, f, 0) // 块 0 有效页 3
	mustDiscard(t, f, 4) // 块 1 有效页 3
	mustDiscard(t, f, 8) // 块 2 有效页 3

	// 白盒调整擦除次数：块 0 擦除 5 次，块 1 擦除 2 次，块 2 擦除 2 次。
	// 同步维护各选择堆（惰性删除使旧快照自动失效）。
	setErases(f, 0, 5)
	setErases(f, 1, 2)
	setErases(f, 2, 2)

	victim, ok := f.pickVictim()
	if !ok || victim != 1 {
		t.Fatalf("pickVictim = %d, %v; want 1 (fewer erases beats smaller id)", victim, ok)
	}
}

// setErases 白盒设置块擦除次数并维护所有相关堆（测试辅助）。
func setErases(f *FTL, id, erases int) {
	blk := &f.blocks[id]
	blk.erases = erases
	f.eraseMin.push(eraseEntry{erases: erases, id: id})
	f.eraseMax.push(eraseEntry{erases: erases, id: id})
	if blk.full() && f.active != id && !blk.retired {
		f.victimHeap.push(victimEntry{valid: blk.valid, erases: erases, id: id})
		f.coldHeap.push(coldEntry{erases: erases, id: id})
	}
}

// TestFullValidBlockNoBenefit 有效页数等于每块页数的块没有回收收益，
// 不得被选为受害块；最优候选也无收益时 pickVictim 返回 false，
// 回收循环停止（端到端效果见 TestWatermarkBoundary：空闲=2 < 高水位
// 时回收即停）。
func TestFullValidBlockNoBenefit(t *testing.T) {
	f := mustNew(t, baseCfg())

	// 填满块 0-2，全部 4/4 有效：候选存在但全无收益。
	for i := 0; i < 12; i++ {
		mustWrite(t, f, uint64(i))
	}
	if victim, ok := f.pickVictim(); ok {
		t.Fatalf("pickVictim = %d, true; want false (all candidates full-valid)", victim)
	}
	// 块 0 出现一个失效页后立即成为唯一有收益的受害块。
	mustDiscard(t, f, 0)
	if victim, ok := f.pickVictim(); !ok || victim != 0 {
		t.Fatalf("pickVictim = %d, %v; want 0, true", victim, ok)
	}
}

// wearCfg 磨损阈值 2，其余同 baseCfg。
func wearCfg() Config {
	cfg := baseCfg()
	cfg.WearThreshold = 2
	return cfg
}

// TestWearLevelAtThreshold 磨损差恰等于阈值时不追加冷块搬迁。
func TestWearLevelAtThreshold(t *testing.T) {
	f := mustNew(t, wearCfg())
	// 白盒让块 4 擦除次数为 2：回收后擦除次数为
	// [1,1,0,0,2,0,0,0]，最大最小差 = 2 == 阈值，不得触发均衡。
	driveGC(t, f, 4, 2)
	ec := eraseCounts(f)
	want := []int{1, 1, 0, 0, 2, 0, 0, 0}
	for i := range want {
		if ec[i] != want[i] {
			t.Fatalf("erases = %v, want %v (diff == threshold: no cold move)", ec, want)
		}
	}
}

// TestWearLevelAboveThreshold 磨损差比阈值多一时追加恰好一次冷块搬迁。
func TestWearLevelAboveThreshold(t *testing.T) {
	f := mustNew(t, wearCfg())
	// 块 4 擦除次数为 3：回收后差值 = 3 > 阈值 2，
	// 擦除次数最少的写满块（块 2，并列取块号较小者）被搬迁并擦除。
	driveGC(t, f, 4, 3)
	ec := eraseCounts(f)
	want := []int{1, 1, 1, 0, 3, 0, 0, 0}
	for i := range want {
		if ec[i] != want[i] {
			t.Fatalf("erases = %v, want %v (diff > threshold: exactly one cold move of block 2)", ec, want)
		}
	}
	// 块 3 同样擦除次数为 0 但未被搬迁：每次垃圾回收调用至多追加一次。
	// 被搬离块 2 的数据保持可读。
	for _, lpn := range []uint64{8, 9, 10, 11} {
		mustRead(t, f, lpn, byte(lpn))
	}
}

// driveGC 的公共布局 + 白盒设置指定块擦除次数。
func driveGC(t *testing.T, f *FTL, eraseBlock, erases int) {
	t.Helper()
	for i := 0; i < 20; i++ {
		mustWrite(t, f, uint64(i))
	}
	mustDiscard(t, f, 0)
	setErases(f, eraseBlock, erases)
	for _, lpn := range []uint64{1, 2, 3, 4, 5} {
		mustWrite(t, f, lpn)
	}
}

// TestRetireAtEraseLimit 块寿命恰好用尽的那次擦除成功后立即退役；
// 退役后准入收紧，覆盖写仍可进行；空间耗尽的拒绝不触发回收。
//
// 布局（5 块 × 2 页，低 2 高 3，寿命 2）：反复覆盖写驱动回收，
// 全程断言不变式：擦除次数不超过寿命上限；达到上限的块必已退役。
func TestRetireAtEraseLimit(t *testing.T) {
	cfg := Config{
		NumBlocks:     5,
		PagesPerBlock: 2,
		LogicalPages:  16,
		LowWatermark:  2,
		HighWatermark: 3,
		EraseLimit:    2,
		WearThreshold: 1000,
	}
	f := mustNew(t, cfg)

	for i := 0; i < 6; i++ {
		mustWrite(t, f, uint64(i)) // 块 0,1,2 写满
	}

	assertLifeInvariants := func() {
		t.Helper()
		st := f.Stats()
		for b, c := range st.EraseCounts {
			if c > cfg.EraseLimit {
				t.Fatalf("block %d erased %d times, limit %d", b, c, cfg.EraseLimit)
			}
			if retired := f.blocks[b].retired; retired != (c == cfg.EraseLimit) {
				t.Fatalf("block %d: retired=%v but erases=%d (limit %d)", b, retired, c, cfg.EraseLimit)
			}
		}
	}

	// 覆盖写驱动回收，直到第一个块退役。
	for i := 0; f.Stats().RetiredBlocks == 0; i++ {
		mustWrite(t, f, uint64(i%6))
		assertLifeInvariants()
		if i > 100 {
			t.Fatal("no block retired after 100 overwrites")
		}
	}
	if got := f.Stats().RetiredBlocks; got != 1 {
		t.Fatalf("RetiredBlocks = %d, want exactly 1", got)
	}

	// 退役后准入收紧：上限 (4-2)*2 = 4 < 已映射 6，新逻辑页写入被拒绝，
	// 且拒绝不得改变任何状态、不得触发回收。
	before := f.Stats()
	if err := f.Write(6, nil); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("new LPN after retirement = %v, want ErrNoSpace (admission tightened)", err)
	}
	after := f.Stats()
	if after.FreeBlocks != before.FreeBlocks || totalErases(f) != sumInts(before.EraseCounts) ||
		after.LogicalWrites != before.LogicalWrites ||
		after.PhysicalProgrammed != before.PhysicalProgrammed {
		t.Fatalf("rejected write must not change state or trigger GC: before=%+v after=%+v", before, after)
	}

	// 覆盖写已映射逻辑页不受准入限制：仍可成功。
	mustWrite(t, f, 0)
	mustRead(t, f, 0, 0)

	// 继续覆盖写直到第二个块退役：此时未退役块全部写满且无失效页、
	// 无空闲块，覆盖写物理上无法进行，报空间耗尽而非出错或死循环。
	for i := 0; f.Stats().RetiredBlocks == 1; i++ {
		mustWrite(t, f, uint64(i%6))
		assertLifeInvariants()
		if i > 100 {
			t.Fatal("second retirement did not happen")
		}
	}
	if got := f.Stats().FreeBlocks; got != 0 {
		t.Fatalf("FreeBlocks = %d, want 0 (two blocks retired, rest full-valid)", got)
	}
	if err := f.Write(4, nil); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("overwrite with no free/reclaimable space = %v, want ErrNoSpace", err)
	}
	// 退役块永不参与分配：擦除次数保持为寿命上限。
	for b, blk := range f.blocks {
		if blk.retired && blk.erases != cfg.EraseLimit {
			t.Fatalf("retired block %d erased %d times, want exactly %d", b, blk.erases, cfg.EraseLimit)
		}
	}
}

func sumInts(xs []int) int {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum
}

// TestStatsAndWriteAmplification 统计量可查询且守恒：
// 物理编程页数 = 逻辑写入页数 + 搬移页数；写放大以两个整数返回。
func TestStatsAndWriteAmplification(t *testing.T) {
	f := mustNew(t, baseCfg())

	num, den := f.WriteAmplification()
	if num != 0 || den != 0 {
		t.Fatalf("initial WA = %d/%d, want 0/0", num, den)
	}

	// 24 次不同写入：无回收，物理编程 == 逻辑写入。
	for i := 0; i < 24; i++ {
		mustWrite(t, f, uint64(i))
	}
	st := f.Stats()
	if st.LogicalWrites != 24 || st.PhysicalProgrammed != 24 {
		t.Fatalf("stats = %+v, want logical=24 physical=24", st)
	}
	if st.FreeBlocks != 2 || st.RetiredBlocks != 0 || len(st.EraseCounts) != 8 {
		t.Fatalf("stats = %+v, want free=2 retired=0 blocks=8", st)
	}

	// 触发一次回收：块 0 的 3 个有效页被搬移（3 次额外物理编程）。
	mustWrite(t, f, 0)
	st = f.Stats()
	if st.LogicalWrites != 25 || st.PhysicalProgrammed != 25+3 {
		t.Fatalf("stats = %+v, want logical=25 physical=28 (3 moved pages)", st)
	}
	num, den = f.WriteAmplification()
	if num != 28 || den != 25 {
		t.Fatalf("WA = %d/%d, want 28/25", num, den)
	}
	// 守恒：各块有效页数之和 == 已映射逻辑页数。
	validSum := 0
	for i := range f.blocks {
		validSum += f.blocks[i].valid
	}
	if validSum != f.mapped || f.mapped != 24 {
		t.Fatalf("valid pages = %d, mapped = %d, want both 24", validSum, f.mapped)
	}
}
