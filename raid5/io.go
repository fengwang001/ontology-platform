package raid5

import "errors"

// validateRange 校验连续逻辑块范围合法，合法时返回覆盖的条带区间。
func (v *Volume) validateRange(startBlock int, nblocks int) (firstStripe, lastStripe int, err error) {
	total := v.stripes * (v.n - 1)
	if startBlock < 0 || nblocks <= 0 || startBlock+nblocks > total {
		return 0, 0, ErrBlockOutOfRange
	}
	return startBlock / (v.n - 1), (startBlock + nblocks - 1) / (v.n - 1), nil
}

// lockStripes 按条带号升序加锁，保证同条带串行且避免死锁。
func (v *Volume) lockStripes(first, last int) {
	for s := first; s <= last; s++ {
		v.stripeLocks[s].Lock()
	}
}

func (v *Volume) unlockStripes(first, last int) {
	for s := last; s >= first; s-- {
		v.stripeLocks[s].Unlock()
	}
}

// Write 写入连续逻辑块 [startBlock, startBlock+len(blocks))。
// 可覆盖任意范围：整带覆盖直接算校验，部分覆盖走读-改-写。
func (v *Volume) Write(startBlock int, blocks [][]byte) error {
	if len(blocks) == 0 {
		return ErrBlockOutOfRange
	}
	for _, b := range blocks {
		if len(b) != BlockSize {
			return ErrBlockOutOfRange
		}
	}
	first, last, err := v.validateRange(startBlock, len(blocks))
	if err != nil {
		return err
	}
	v.mu.Lock()
	degraded := v.failedDisk >= 0
	v.mu.Unlock()

	v.lockStripes(first, last)
	defer v.unlockStripes(first, last)

	// 把输入按条带分组。
	dps := v.dataPerStripe()
	for s := first; s <= last; s++ {
		lo := s*dps - startBlock
		if lo < 0 {
			lo = 0
		}
		hi := (s+1)*dps - startBlock
		if hi > len(blocks) {
			hi = len(blocks)
		}
		seg := blocks[lo:hi]
		baseSlot := (startBlock + lo) - s*dps
		if degraded {
			if err := v.writeStripeDegraded(s, baseSlot, seg); err != nil {
				return err
			}
		} else {
			if err := v.writeStripeNormal(s, baseSlot, seg); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeStripeNormal 正常态单条带写入。
func (v *Volume) writeStripeNormal(s, baseSlot int, newData [][]byte) error {
	parity, data := stripeMap(v.n, s)
	full := len(newData) == v.n-1

	bitmap := make([]bool, v.n-1)
	for i := range newData {
		bitmap[baseSlot+i] = true
	}

	// 1) 意图先落盘（正常态只记录条带与覆盖位图）。
	if err := v.journal.AppendIntent(s, bitmap, false, nil); err != nil {
		return err
	}
	if v.crashAt(s, CrashAfterIntent) {
		return errCrashed
	}

	var newParity []byte
	if full {
		// 整带写：新校验直接由全部新数据异或得出。
		p := make([]byte, BlockSize)
		copy(p, newData[0])
		for _, b := range newData[1:] {
			xorBlocks(p, b)
		}
		newParity = p
	} else {
		// 部分写（读-改-写）：
		// 新校验 = 旧校验 XOR 旧数据 XOR 新数据（仅对被改写的数据块）。
		oldParity := make([]byte, BlockSize)
		if err := v.readDisk(parity, s, oldParity); err != nil {
			return err
		}
		p := make([]byte, BlockSize)
		copy(p, oldParity)
		for i, nd := range newData {
			slot := baseSlot + i
			old := make([]byte, BlockSize)
			if err := v.readDisk(data[slot], s, old); err != nil {
				return err
			}
			xorBlocks(p, old, nd)
		}
		newParity = p
	}

	// 2) 新数据块落盘。
	for i, nd := range newData {
		slot := baseSlot + i
		if err := v.writeDisk(data[slot], s, nd); err != nil {
			return err
		}
	}
	if v.crashAt(s, CrashAfterData) {
		return errCrashed
	}

	// 3) 新校验落盘。
	if err := v.writeDisk(parity, s, newParity); err != nil {
		return err
	}
	if v.crashAt(s, CrashAfterParity) {
		return errCrashed
	}

	// 4) 提交记录落盘。
	if err := v.journal.AppendCommit(s); err != nil {
		return err
	}
	if v.crashAt(s, CrashAfterCommit) {
		return errCrashed
	}
	// 已完成的写无需再保留意图；截断使日志长度有界。
	// 截断前崩溃也安全：重开解析到“意图+提交”即视为完成。
	return v.journal.Reset()
}

// writeStripeDegraded 降级态（含重建中）单条带写入。
//
// 一条盘失效时，先从其余 N-1 块异或还原缺失槽（重建期间该槽可能已由
// 重建或先前的降级写修复，则直接读新盘），得到条带完整内容；
// 覆盖新数据并重算校验后，把全部新数据与新校验写入意图记录，
// 再整体写到所有存活盘（含换入新盘的对应槽）。
// 意图整体重放保证断电不丢已确认写入、条带始终校验一致。
func (v *Volume) writeStripeDegraded(s, baseSlot int, newData [][]byte) error {
	parity, data := stripeMap(v.n, s)

	// 收集条带完整内容（按盘号）。
	full := make([][]byte, v.n)
	for d := 0; d < v.n; d++ {
		buf := make([]byte, BlockSize)
		err := v.disks[d].ReadBlockForRebuild(s, buf)
		if err == nil {
			full[d] = buf
			continue
		}
		if !errors.Is(err, ErrDiskDead) {
			return err
		}
		// 该槽正是失效/尚未重建的槽：从其余块异或还原。
		rec, rerr := v.xorOtherDisks(s, d)
		if rerr != nil {
			return rerr
		}
		full[d] = rec
	}
	// 覆盖新数据。
	for i, nd := range newData {
		full[data[baseSlot+i]] = append([]byte(nil), nd...)
	}
	// 整带重算校验。
	p := make([]byte, BlockSize)
	copy(p, full[data[0]])
	for k := 1; k < len(data); k++ {
		xorBlocks(p, full[data[k]])
	}
	full[parity] = p

	bitmap := make([]bool, v.n-1)
	for i := range newData {
		bitmap[baseSlot+i] = true
	}
	// 1) 意图（携带全部新数据与新校验）先落盘。
	if err := v.journal.AppendIntent(s, bitmap, true, full); err != nil {
		return err
	}
	if v.crashAt(s, CrashAfterIntent) {
		return errCrashed
	}
	// 2) 整体写存活盘（替换盘为存活但未就绪，走重建写以记录进度）。
	for d := 0; d < v.n; d++ {
		if v.disks[d].Failed() {
			continue
		}
		if err := v.disks[d].WriteBlockForRebuild(s, full[d]); err != nil {
			return err
		}
	}
	if v.crashAt(s, CrashAfterData) || v.crashAt(s, CrashAfterParity) {
		return errCrashed
	}
	// 3) 提交。
	if err := v.journal.AppendCommit(s); err != nil {
		return err
	}
	if v.crashAt(s, CrashAfterCommit) {
		return errCrashed
	}
	return v.journal.Reset()
}

// xorOtherDisks 异或条带 s 中除 missingDisk 外的全部块。
func (v *Volume) xorOtherDisks(s, missingDisk int) ([]byte, error) {
	out := make([]byte, BlockSize)
	first := true
	for d := 0; d < v.n; d++ {
		if d == missingDisk {
			continue
		}
		buf := make([]byte, BlockSize)
		if err := v.disks[d].ReadBlockForRebuild(s, buf); err != nil {
			if errors.Is(err, ErrDiskDead) {
				return nil, ErrDoubleFault
			}
			return nil, err
		}
		if first {
			copy(out, buf)
			first = false
		} else {
			xorBlocks(out, buf)
		}
	}
	if first {
		return nil, ErrDoubleFault
	}
	return out, nil
}

// Read 读取连续逻辑块到 dst（dst 长度即块数，每个元素长度须为 BlockSize）。
func (v *Volume) Read(startBlock int, dst [][]byte) error {
	if len(dst) == 0 {
		return ErrBlockOutOfRange
	}
	for _, b := range dst {
		if len(b) != BlockSize {
			return ErrBlockOutOfRange
		}
	}
	first, last, err := v.validateRange(startBlock, len(dst))
	if err != nil {
		return err
	}
	v.lockStripes(first, last)
	defer v.unlockStripes(first, last)

	for i := 0; i < len(dst); i++ {
		lb := startBlock + i
		s, _, disk := blockLocation(v.n, lb)
		err := v.disks[disk].ReadBlockForRebuild(s, dst[i])
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrDiskDead) {
			return err
		}
		// 降级读：由同条带其余所有块异或还原。
		recovered, rerr := v.xorOtherDisks(s, disk)
		if rerr != nil {
			return rerr
		}
		copy(dst[i], recovered)
	}
	return nil
}

var errCrashed = errors.New("raid5: simulated power loss")
