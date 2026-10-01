package raid

import (
	"sync"
	"sync/atomic"
)

// Volume 是带分布式奇偶校验的条带化块卷（RAID-5 风格，左对称布局）。
//
// 布局：共 n 块盘，条带 s 含 n-1 个数据块与 1 个校验块。
// 校验块位于盘 P(s) = n-1-(s mod n)；数据块 k 位于盘 (P(s)+1+k) mod n。
// 逻辑块 b 映射：s = b/(n-1)，k = b%(n-1)，物理块号即条带号 s。
type Volume struct {
	n         int
	stripes   int
	blockSize int
	disks     []Device
	j         *journal

	mu            sync.Mutex // 保护 failed/rebuilding/rebuildTarget
	failed        int        // -1 表示无失效盘
	rebuilding    bool
	rebuildTarget int
	progress      atomic.Int64 // 已重建完成的条带数
	rebuildDone   chan struct{}

	stripeMu []sync.RWMutex // 每条带一把锁：同条带串行，不同条带并发
}

// Open 打开卷：读取日志超级块恢复失效盘状态，并执行崩溃恢复。
func Open(disks []Device, journalDev Device) (*Volume, error) {
	n := len(disks)
	if n < 3 {
		return nil, ErrTooFewDisks
	}
	stripes := disks[0].NumBlocks()
	bs := disks[0].BlockSize()
	for _, d := range disks {
		if d.NumBlocks() != stripes || d.BlockSize() != bs {
			return nil, ErrGeometryMismatch
		}
	}
	j, err := openJournal(journalDev, n, stripes)
	if err != nil {
		return nil, err
	}
	v := &Volume{
		n:         n,
		stripes:   stripes,
		blockSize: bs,
		disks:     disks,
		j:         j,
		failed:    j.failed,
		stripeMu:  make([]sync.RWMutex, stripes),
	}
	if err := v.recover(); err != nil {
		return nil, err
	}
	return v, nil
}

// NumBlocks 返回逻辑块总数 = 条带数 * (n-1)。
func (v *Volume) NumBlocks() int { return v.stripes * (v.n - 1) }

// BlockSize 返回块大小。
func (v *Volume) BlockSize() int { return v.blockSize }

// N 返回盘数。
func (v *Volume) N() int { return v.n }

// ParityDisk 返回条带 s 的校验盘：n-1-(s mod n)。
func (v *Volume) ParityDisk(s int) int { return v.n - 1 - (s % v.n) }

// DataDisk 返回条带 s 第 k 个数据块所在盘：从校验盘下一块起依次回绕。
func (v *Volume) DataDisk(s, k int) int { return (v.ParityDisk(s) + 1 + k) % v.n }

// Locate 把逻辑块号映射为 (条带, 数据块序号, 盘号, 物理块号)。
func (v *Volume) Locate(b int) (stripe, k, disk, phys int, err error) {
	if b < 0 || b >= v.NumBlocks() {
		return 0, 0, 0, 0, ErrBlockOutOfRange
	}
	stripe = b / (v.n - 1)
	k = b % (v.n - 1)
	return stripe, k, v.DataDisk(stripe, k), stripe, nil
}

func xorInto(dst, src []byte) {
	for i := range dst {
		dst[i] ^= src[i]
	}
}

// diskLive 报告盘 d 上条带 s 的块当前是否可直接读写：
// 未失效，或虽失效但重建已覆盖该条带（已重建的块立即参与读写）。
func (v *Volume) diskLive(d, s int) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failed != d {
		return true
	}
	return v.rebuilding && v.rebuildTarget == d && int64(s) < v.progress.Load()
}

// xorOthers 用其余 n-1 块盘异或还原盘 d 在条带 s 的块（降级读）。
func (v *Volume) xorOthers(s, d int) ([]byte, error) {
	acc := make([]byte, v.blockSize)
	for disk := 0; disk < v.n; disk++ {
		if disk == d {
			continue
		}
		blk, err := v.disks[disk].ReadBlock(s)
		if err != nil {
			return nil, err
		}
		xorInto(acc, blk)
	}
	return acc, nil
}

// Read 读逻辑块；目标盘失效时由其余块异或还原。
func (v *Volume) Read(b int) ([]byte, error) {
	s, _, d, _, err := v.Locate(b)
	if err != nil {
		return nil, err
	}
	v.stripeMu[s].RLock()
	defer v.stripeMu[s].RUnlock()
	if v.diskLive(d, s) {
		return v.disks[d].ReadBlock(s)
	}
	return v.xorOthers(s, d)
}

// Write 写单个逻辑块（部分条带写，读改写）：
// newParity = oldParity ^ oldData ^ newData。
// 先落意图记录，再写数据块与校验块，最后清除意图。
func (v *Volume) Write(b int, data []byte) error {
	s, _, d, _, err := v.Locate(b)
	if err != nil {
		return err
	}
	if len(data) != v.blockSize {
		return ErrBlockSizeMismatch
	}
	pd := v.ParityDisk(s)
	v.stripeMu[s].Lock()
	defer v.stripeMu[s].Unlock()

	v.mu.Lock()
	degraded := v.failed != -1
	v.mu.Unlock()
	dataLive := v.diskLive(d, s)
	parityLive := v.diskLive(pd, s)

	var oldData []byte
	if dataLive {
		if oldData, err = v.disks[d].ReadBlock(s); err != nil {
			return err
		}
	} else {
		if oldData, err = v.xorOthers(s, d); err != nil {
			return err
		}
	}
	newParity := make([]byte, v.blockSize)
	if parityLive {
		oldParity, err := v.disks[pd].ReadBlock(s)
		if err != nil {
			return err
		}
		copy(newParity, oldParity)
		xorInto(newParity, oldData)
		xorInto(newParity, data)
	}

	entry := journalEntry{stripe: s}
	if !degraded {
		entry.recompute = true
	} else {
		// 降级状态：意图记录携带新数据与新校验，恢复时整体重放。
		if dataLive {
			entry.payloads = append(entry.payloads, payload{disk: d, block: s, data: append([]byte(nil), data...)})
		}
		if parityLive {
			entry.payloads = append(entry.payloads, payload{disk: pd, block: s, data: newParity})
		}
	}
	if err := v.j.set(s, &entry); err != nil {
		return err
	}
	if dataLive {
		if err := v.disks[d].WriteBlock(s, data); err != nil {
			return err
		}
	}
	if parityLive {
		if err := v.disks[pd].WriteBlock(s, newParity); err != nil {
			return err
		}
	}
	return v.j.clear(s)
}

// WriteStripe 整条带写入：data 含 n-1 个数据块，校验直接由新数据异或计算。
func (v *Volume) WriteStripe(s int, data [][]byte) error {
	if s < 0 || s >= v.stripes {
		return ErrStripeOutOfRange
	}
	if len(data) != v.n-1 {
		return ErrBlockSizeMismatch
	}
	for _, blk := range data {
		if len(blk) != v.blockSize {
			return ErrBlockSizeMismatch
		}
	}
	pd := v.ParityDisk(s)
	v.stripeMu[s].Lock()
	defer v.stripeMu[s].Unlock()

	v.mu.Lock()
	degraded := v.failed != -1
	v.mu.Unlock()

	parity := make([]byte, v.blockSize)
	for _, blk := range data {
		xorInto(parity, blk)
	}

	entry := journalEntry{stripe: s}
	if !degraded {
		entry.recompute = true
	} else {
		for k, blk := range data {
			if d := v.DataDisk(s, k); v.diskLive(d, s) {
				entry.payloads = append(entry.payloads, payload{disk: d, block: s, data: append([]byte(nil), blk...)})
			}
		}
		if v.diskLive(pd, s) {
			entry.payloads = append(entry.payloads, payload{disk: pd, block: s, data: append([]byte(nil), parity...)})
		}
	}
	if err := v.j.set(s, &entry); err != nil {
		return err
	}
	for k, blk := range data {
		if d := v.DataDisk(s, k); v.diskLive(d, s) {
			if err := v.disks[d].WriteBlock(s, blk); err != nil {
				return err
			}
		}
	}
	if v.diskLive(pd, s) {
		if err := v.disks[pd].WriteBlock(s, parity); err != nil {
			return err
		}
	}
	return v.j.clear(s)
}

// recover 崩溃恢复：逐条意图记录处理。
// 正常状态记录：按数据块重算校验；降级状态记录：整体重放新数据与新校验。
func (v *Volume) recover() error {
	entries, err := v.j.entries()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.recompute {
			if err := v.recomputeParity(e.stripe); err != nil {
				return err
			}
		} else {
			for _, p := range e.payloads {
				if p.disk == v.failed {
					continue // 失效盘留待重建
				}
				if err := v.disks[p.disk].WriteBlock(p.block, p.data); err != nil {
					return err
				}
			}
		}
		if err := v.j.clear(e.slot); err != nil {
			return err
		}
	}
	return nil
}

// recomputeParity 按数据块重算条带校验（仅无失效盘时可完备执行）。
func (v *Volume) recomputeParity(s int) error {
	v.mu.Lock()
	failed := v.failed
	v.mu.Unlock()
	if failed != -1 {
		// 降级下无法仅靠数据块重算（失效数据块需旧校验还原，循环依赖），
		// 该情形由携带负载的意图记录覆盖，这里无需动作。
		return nil
	}
	parity := make([]byte, v.blockSize)
	for k := 0; k < v.n-1; k++ {
		blk, err := v.disks[v.DataDisk(s, k)].ReadBlock(s)
		if err != nil {
			return err
		}
		xorInto(parity, blk)
	}
	return v.disks[v.ParityDisk(s)].WriteBlock(s, parity)
}

// FailDisk 标记盘 d 失效；已有一块盘失效时整体拒绝。
func (v *Volume) FailDisk(d int) error {
	if d < 0 || d >= v.n {
		return ErrDiskOutOfRange
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failed != -1 {
		return ErrDiskAlreadyFailed
	}
	if err := v.j.setFailedDisk(d); err != nil {
		return err
	}
	v.failed = d
	return nil
}

// Rebuild 在后台把失效盘 target 重建到 replacement 上。
// 目标盘未失效或重建进行中时整体拒绝。重建期间读写不中断。
func (v *Volume) Rebuild(target int, replacement Device) error {
	if target < 0 || target >= v.n {
		return ErrDiskOutOfRange
	}
	v.mu.Lock()
	if v.rebuilding {
		v.mu.Unlock()
		return ErrRebuildInProgress
	}
	if v.failed == -1 || v.failed != target {
		v.mu.Unlock()
		return ErrDiskNotFailed
	}
	if replacement != nil {
		if replacement.NumBlocks() != v.stripes || replacement.BlockSize() != v.blockSize {
			v.mu.Unlock()
			return ErrGeometryMismatch
		}
		v.disks[target] = replacement
	}
	v.rebuilding = true
	v.rebuildTarget = target
	v.progress.Store(0)
	v.rebuildDone = make(chan struct{})
	done := v.rebuildDone
	v.mu.Unlock()

	go func() {
		for s := 0; s < v.stripes; s++ {
			v.stripeMu[s].Lock()
			blk, err := v.xorOthers(s, target)
			if err == nil {
				err = v.disks[target].WriteBlock(s, blk)
			}
			v.stripeMu[s].Unlock()
			if err != nil {
				break // 设备错误（如模拟断电），保留失效状态等待下次重建
			}
			v.progress.Store(int64(s + 1))
		}
		if v.progress.Load() == int64(v.stripes) {
			v.mu.Lock()
			v.failed = -1
			v.rebuilding = false
			v.j.setFailedDisk(-1)
			v.mu.Unlock()
		} else {
			v.mu.Lock()
			v.rebuilding = false
			v.mu.Unlock()
		}
		close(done)
	}()
	return nil
}

// WaitRebuild 等待当前重建完成（无重建时立即返回）。
func (v *Volume) WaitRebuild() {
	v.mu.Lock()
	done := v.rebuildDone
	v.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Status 返回 (失效盘, 是否重建中, 已重建条带数)。
func (v *Volume) Status() (failed int, rebuilding bool, progress int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.failed, v.rebuilding, int(v.progress.Load())
}

// VerifyStripe 校验条带 s：校验块 == 全部数据块异或。要求无失效盘。
func (v *Volume) VerifyStripe(s int) (bool, error) {
	if s < 0 || s >= v.stripes {
		return false, ErrStripeOutOfRange
	}
	v.stripeMu[s].RLock()
	defer v.stripeMu[s].RUnlock()
	parity := make([]byte, v.blockSize)
	for k := 0; k < v.n-1; k++ {
		blk, err := v.disks[v.DataDisk(s, k)].ReadBlock(s)
		if err != nil {
			return false, err
		}
		xorInto(parity, blk)
	}
	stored, err := v.disks[v.ParityDisk(s)].ReadBlock(s)
	if err != nil {
		return false, err
	}
	for i := range parity {
		if parity[i] != stored[i] {
			return false, nil
		}
	}
	return true, nil
}
