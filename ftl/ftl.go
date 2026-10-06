// Package ftl 实现闪存转换层：逻辑页映射、顺序编程、按水位触发的
// 垃圾回收、静态磨损均衡、块退役与写放大统计。
//
// 并发安全：所有导出方法持有同一把互斥锁，效果等价于某个串行顺序。
// 确定性：实现不含任何随机源，相同操作序列重放得到完全相同的
// 物理布局与统计量。
package ftl

import "sync"

// FTL 闪存转换层服务。
type FTL struct {
	cfg Config

	mu     sync.Mutex
	blocks []block
	l2p    map[uint64]pa // 逻辑页 -> 物理页
	mapped int           // 已映射逻辑页数

	active    int // 活动块号，-1 表示无
	freeCount int // 空闲块数（不含退役块）
	retired   int // 退役块数

	logicalWrites      uint64 // 逻辑写入页数
	physicalProgrammed uint64 // 物理编程页数（含 GC / 均衡搬移）

	freeHeap   minHeap[freeEntry]   // 空闲块：(擦除次数, 块号) 最小堆
	victimHeap minHeap[victimEntry] // 写满块：(有效页, 擦除次数, 块号)，惰性删除
	coldHeap   minHeap[coldEntry]   // 写满块：(擦除次数, 块号)，惰性删除
	eraseMin   minHeap[eraseEntry]  // 未退役块擦除次数最小值，惰性删除
	eraseMax   minHeap[eraseEntry]  // 未退役块擦除次数最大值，惰性删除

	dbg debugCounters
}

// debugCounters 复杂度可验证性计数器，仅供测试与诊断。
type debugCounters struct {
	victimPops int // 受害块堆弹出次数（含惰性丢弃的过期项）
	heapPushes int // 所有选择堆的压入总次数
}

// New 校验配置并构造 FTL。初始所有块均为空闲，擦除次数为 0。
func New(cfg Config) (*FTL, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	f := &FTL{
		cfg:    cfg,
		blocks: make([]block, cfg.NumBlocks),
		l2p:    make(map[uint64]pa),
		active: -1,
		freeHeap: newMinHeap(func(a, b freeEntry) bool {
			if a.erases != b.erases {
				return a.erases < b.erases
			}
			return a.id < b.id
		}),
		victimHeap: newMinHeap(func(a, b victimEntry) bool {
			if a.valid != b.valid {
				return a.valid < b.valid
			}
			if a.erases != b.erases {
				return a.erases < b.erases
			}
			return a.id < b.id
		}),
		coldHeap: newMinHeap(func(a, b coldEntry) bool {
			if a.erases != b.erases {
				return a.erases < b.erases
			}
			return a.id < b.id
		}),
		eraseMin: newMinHeap(func(a, b eraseEntry) bool {
			return a.erases < b.erases
		}),
		eraseMax: newMinHeap(func(a, b eraseEntry) bool {
			return a.erases > b.erases
		}),
	}
	for i := range f.blocks {
		f.blocks[i] = newBlock(cfg.PagesPerBlock)
		f.freeHeap.push(freeEntry{erases: 0, id: i})
		f.eraseMin.push(eraseEntry{erases: 0, id: i})
		f.eraseMax.push(eraseEntry{erases: 0, id: i})
	}
	f.freeCount = cfg.NumBlocks
	return f, nil
}

// Write 写入一个逻辑页。未映射逻辑页的写入受准入限制；
// 覆盖写已映射逻辑页不受限制。写入完成后按水位触发垃圾回收。
func (f *FTL) Write(lpn uint64, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.write(lpn, data)
}

// Read 读取一个逻辑页。未写过或已丢弃报 ErrNotWritten。
func (f *FTL) Read(lpn uint64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if lpn >= uint64(f.cfg.LogicalPages) {
		return nil, ErrInvalidArgument
	}
	addr, ok := f.l2p[lpn]
	if !ok {
		return nil, ErrNotWritten
	}
	return append([]byte(nil), f.blocks[addr.block].data[addr.page]...), nil
}

// Discard 丢弃一个逻辑页：物理页失效并去除映射。
// 对未映射的逻辑页是无操作而非错误。
func (f *FTL) Discard(lpn uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if lpn >= uint64(f.cfg.LogicalPages) {
		return ErrInvalidArgument
	}
	addr, ok := f.l2p[lpn]
	if !ok {
		return nil
	}
	f.invalidate(addr.block, addr.page)
	delete(f.l2p, lpn)
	f.mapped--
	return nil
}

// Stats 统计量快照。
type Stats struct {
	LogicalWrites      uint64 // 逻辑写入页数
	PhysicalProgrammed uint64 // 物理编程页数（含搬移）
	FreeBlocks         int    // 空闲块数
	RetiredBlocks      int    // 退役块数
	EraseCounts        []int  // 各块擦除次数
}

// Stats 返回统计量快照。
func (f *FTL) Stats() Stats {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := Stats{
		LogicalWrites:      f.logicalWrites,
		PhysicalProgrammed: f.physicalProgrammed,
		FreeBlocks:         f.freeCount,
		RetiredBlocks:      f.retired,
		EraseCounts:        make([]int, len(f.blocks)),
	}
	for i := range f.blocks {
		s.EraseCounts[i] = f.blocks[i].erases
	}
	return s
}

// WriteAmplification 写放大 = 物理编程页数 / 逻辑写入页数，
// 以两个整数（分子、分母）返回，不使用浮点。分母为 0 表示尚无逻辑写入。
func (f *FTL) WriteAmplification() (num, den uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.physicalProgrammed, f.logicalWrites
}

// ---- 以下为内部实现，均须在持有 f.mu 时调用 ----

// nonRetired 未退役块数。
func (f *FTL) nonRetired() int { return f.cfg.NumBlocks - f.retired }

// write 写入主路径。
//
// 次序：参数非法 > 准入（空间耗尽）> 执行。准入失败不得改变任何状态、
// 不得触发垃圾回收。覆盖写已映射逻辑页不受准入限制。
func (f *FTL) write(lpn uint64, data []byte) error {
	if lpn >= uint64(f.cfg.LogicalPages) {
		return ErrInvalidArgument
	}
	old, mapped := f.l2p[lpn]
	if !mapped {
		// 准入：写入会使已映射逻辑页数超过
		// (未退役块数 - 低水位) * 每块页数 则拒绝。
		if limit := (f.nonRetired() - f.cfg.LowWatermark) * f.cfg.PagesPerBlock; f.mapped+1 > limit {
			return ErrNoSpace
		}
	}
	if !f.ensureActive() {
		// 无空闲块且无活动块：准入上界保证此时必存在含失效页的
		// 写满块，先强制回收一轮再重试；仍失败才报空间耗尽（防御性，
		// 正常不会到达）。
		f.collect()
		if !f.ensureActive() {
			return ErrNoSpace
		}
	}
	if mapped {
		f.invalidate(old.block, old.page)
	}
	f.l2p[lpn] = f.program(f.active, lpn, data)
	if !mapped {
		f.mapped++
	}
	f.logicalWrites++
	f.maybeGC()
	return nil
}

// ensureActive 保证存在可编程的活动块；失败返回 false。
// 活动块写满后，新的活动块取空闲块中擦除次数最少者，并列取块号较小者。
func (f *FTL) ensureActive() bool {
	if f.active >= 0 {
		return true
	}
	e, ok := f.freeHeap.pop()
	if !ok {
		return false
	}
	f.freeCount--
	f.active = e.id
	return true
}

// program 在块 b 的下一个空闲页编程，返回物理地址。
// 遵守顺序编程约束：只能写 nextFree 指向的页；写满后块进入
// 受害块堆与冷块堆，活动块指针清空。
func (f *FTL) program(b int, lpn uint64, data []byte) pa {
	blk := &f.blocks[b]
	page := blk.nextFree
	blk.pages[page] = pageValid
	blk.lpn[page] = lpn
	blk.data[page] = append([]byte(nil), data...)
	blk.nextFree++
	blk.valid++
	f.physicalProgrammed++
	if blk.full() {
		if f.active == b {
			f.active = -1
		}
		f.victimHeap.push(victimEntry{valid: blk.valid, erases: blk.erases, id: b})
		f.coldHeap.push(coldEntry{erases: blk.erases, id: b})
		f.dbg.heapPushes += 2
	}
	return pa{block: b, page: page}
}

// invalidate 使物理页失效并维护受害块堆。
// 写满且非活动、未退役的块有效页数变化时，向受害块堆压入新快照；
// 过期快照在弹出时被惰性丢弃。
func (f *FTL) invalidate(b, page int) {
	blk := &f.blocks[b]
	blk.pages[page] = pageInvalid
	blk.valid--
	if blk.full() && f.active != b && !blk.retired {
		f.victimHeap.push(victimEntry{valid: blk.valid, erases: blk.erases, id: b})
		f.dbg.heapPushes++
	}
}

// maybeGC 写入完成后调用：空闲块数严格小于低水位时触发回收。
func (f *FTL) maybeGC() {
	if f.freeCount >= f.cfg.LowWatermark {
		return
	}
	f.collect()
}

// collect 回收主循环 + 至多一次静态磨损均衡。
// 循环回收直到空闲块数不小于高水位，或没有可回收的块为止；
// 仅当本次调用确实执行过至少一轮回收时才考虑追加磨损均衡。
func (f *FTL) collect() {
	ran := false
	for f.freeCount < f.cfg.HighWatermark {
		victim, ok := f.pickVictim()
		if !ok {
			break
		}
		if !f.reclaim(victim) {
			break
		}
		ran = true
	}
	if ran {
		f.wearLevel()
	}
}

// pickVictim 选取受害块；无可回收块（含最优候选无收益）返回 false。
// 受害块取已写满、非活动、未退役的块中有效页数最少者，并列取擦除
// 次数较少者，再并列取块号较小者。有效页数等于每块页数的块没有回收
// 收益，不得被选；堆按有效页数有序，堆顶无收益即全体无收益。
// 对选中的候选只窥视不弹出：若回收中途因空闲页不足而停止，该块
// 仍可被后续回收选中（其最新快照由 reclaim 内的 invalidate 维护）。
func (f *FTL) pickVictim() (int, bool) {
	for {
		e, ok := f.victimHeap.peek()
		if !ok {
			return -1, false
		}
		f.dbg.victimPops++
		blk := &f.blocks[e.id]
		if blk.retired || f.active == e.id || !blk.full() ||
			blk.valid != e.valid || blk.erases != e.erases {
			f.victimHeap.pop() // 过期项：惰性丢弃
			continue
		}
		if blk.valid == f.cfg.PagesPerBlock {
			return -1, false // 最优候选也无收益，停止回收
		}
		return e.id, true
	}
}

// reclaim 把受害块有效页按页号升序搬到活动块并擦除受害块。
// 空闲页不足时停止（保持映射一致），返回是否完整回收。
// 搬移的页计为物理编程但不计为逻辑写入。
func (f *FTL) reclaim(victim int) bool {
	blk := &f.blocks[victim]
	for page := 0; page < f.cfg.PagesPerBlock; page++ {
		if blk.pages[page] != pageValid {
			continue
		}
		if !f.ensureActive() {
			return false // 空闲页不足（例如因退役）：停止而非出错
		}
		lpn := blk.lpn[page]
		data := blk.data[page]
		f.invalidate(victim, page) // 同时维护受害块堆的最新快照
		f.l2p[lpn] = f.program(f.active, lpn, data)
	}
	f.erase(victim)
	return true
}

// erase 擦除块；擦除次数达到寿命上限即退役。
// 退役块不再参与任何分配、回收与均衡；未退役块重新成为空闲块。
func (f *FTL) erase(b int) {
	blk := &f.blocks[b]
	for i := range blk.pages {
		blk.pages[i] = pageFree
		blk.data[i] = nil
	}
	blk.nextFree = 0
	blk.valid = 0
	blk.erases++
	f.eraseMin.push(eraseEntry{erases: blk.erases, id: b})
	f.eraseMax.push(eraseEntry{erases: blk.erases, id: b})
	f.dbg.heapPushes += 2
	if blk.erases >= f.cfg.EraseLimit {
		blk.retired = true
		f.retired++
		return
	}
	f.freeCount++
	f.freeHeap.push(freeEntry{erases: blk.erases, id: b})
	f.dbg.heapPushes++
}

// wearLevel 静态磨损均衡：至多一次冷块搬迁。
// 未退役块擦除次数最大值与最小值之差严格大于阈值时，取擦除次数最少
// 的已写满、非活动、未退役块（并列取块号较小者），搬走其有效页并擦除。
func (f *FTL) wearLevel() {
	if f.nonRetired() == 0 {
		return
	}
	lo, ok := f.peekErase(&f.eraseMin)
	if !ok {
		return
	}
	hi, ok := f.peekErase(&f.eraseMax)
	if !ok {
		return
	}
	if hi.erases-lo.erases <= f.cfg.WearThreshold {
		return
	}
	for {
		e, ok := f.coldHeap.peek()
		if !ok {
			return // 没有写满的冷块，放弃本次均衡
		}
		blk := &f.blocks[e.id]
		if blk.retired || f.active == e.id || !blk.full() || blk.erases != e.erases {
			f.coldHeap.pop() // 过期项：惰性丢弃
			continue
		}
		f.reclaim(e.id) // 空闲页不足时部分搬移并停止，映射保持一致
		return
	}
}

// peekErase 惰性清理过期项后读取堆顶（不弹出有效项）。
func (f *FTL) peekErase(h *minHeap[eraseEntry]) (eraseEntry, bool) {
	for {
		e, ok := h.peek()
		if !ok {
			return eraseEntry{}, false
		}
		blk := &f.blocks[e.id]
		if blk.retired || blk.erases != e.erases {
			h.pop()
			continue
		}
		return e, true
	}
}
