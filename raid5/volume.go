package raid5

import "sync"

// Options 描述卷的几何参数。
type Options struct {
	// N 成员盘数量，必须 >= 3。
	N int
	// Stripes 条带总数（每盘块数），必须 >= 1。
	Stripes int
}

// CrashHook 用于测试：在写入流水线的指定步骤完成后模拟断电。
// 钩子只触发一次，触发后立即清空。
type CrashHook struct {
	Point  CrashPoint
	Stripe int
}

// CrashPoint 标识写流程中的断电注入点。
type CrashPoint int

const (
	CrashNone CrashPoint = iota
	// CrashAfterIntent 意图记录落盘后、任何数据/校验块写入前断电。
	CrashAfterIntent
	// CrashAfterData 新数据块全部落盘后、校验块更新前断电（写洞点）。
	CrashAfterData
	// CrashAfterParity 校验块落盘后、提交记录落盘前断电。
	CrashAfterParity
	// CrashAfterCommit 提交记录落盘后断电。
	CrashAfterCommit
)

// Volume 是带分布式奇偶校验的条带化块卷。
type Volume struct {
	mu      sync.Mutex // 保护成员盘集合 / 失效 / 重建状态
	n       int
	stripes int
	disks   []Disk
	journal *Journal

	failedDisk  int // -1 表示无失效盘
	rebuildDisk int // 正在重建的盘号，-1 表示无重建
	rebuildWG   sync.WaitGroup

	stripeLocks []sync.Mutex // 同条带串行；不同条带可并发

	hookMu sync.Mutex
	hook   *CrashHook

	// rebuildGate 非空时，重建 goroutine 每处理一个条带前等待一个令牌，
	// 仅供测试把重建稳定停在“进行中”。
	rebuildGate chan bool
}

func (v *Volume) dataPerStripe() int { return v.n - 1 }

// setRebuildGate 安装重建门控（测试用）：每重建一个条带前取一个令牌；
// 收到 false 时中止重建。
func (v *Volume) setRebuildGate(ch chan bool) {
	v.mu.Lock()
	v.rebuildGate = ch
	v.mu.Unlock()
}

// crashAt 在指定点触发“断电”：返回 true 表示调用方必须立即中止，
// 且该进程不再接受后续 IO（测试通过重新打开卷模拟重启）。
func (v *Volume) crashAt(s int, p CrashPoint) bool {
	v.hookMu.Lock()
	h := v.hook
	fire := h != nil && h.Point == p && h.Stripe == s
	if fire {
		v.hook = nil
	}
	v.hookMu.Unlock()
	return fire
}

// readDisk 读指定盘的指定条带块。
func (v *Volume) readDisk(disk, s int, buf []byte) error {
	return v.disks[disk].ReadBlock(s, buf)
}

// writeDisk 写指定盘的指定条带块。
func (v *Volume) writeDisk(disk, s int, buf []byte) error {
	return v.disks[disk].WriteBlock(s, buf)
}

// Create 在给定成员盘上创建新卷并返回已打开的卷。
func Create(disks []Disk, journal *Journal, opts Options) (*Volume, error) {
	if opts.N < 3 {
		return nil, ErrFormat
	}
	if len(disks) != opts.N || opts.Stripes < 1 {
		return nil, ErrFormat
	}
	v := newVolume(disks, journal, opts)
	if err := v.recover(); err != nil {
		return nil, err
	}
	return v, nil
}

func newVolume(disks []Disk, journal *Journal, opts Options) *Volume {
	v := &Volume{
		n:           opts.N,
		stripes:     opts.Stripes,
		disks:       disks,
		journal:     journal,
		failedDisk:  -1,
		rebuildDisk: -1,
		stripeLocks: make([]sync.Mutex, opts.Stripes),
	}
	for i, d := range disks {
		if d.Failed() {
			v.failedDisk = i
		}
	}
	return v
}

// recover 打开时恢复：重放未完成意图，随后清空日志；
// 若检测到失效盘且已是替换新盘，则（重新）启动重建。
func (v *Volume) recover() error {
	pending, err := v.journal.Parse(v.n)
	if err != nil {
		return err
	}
	if pending != nil {
		if pending.degraded {
			if err := v.replayDegraded(pending); err != nil {
				return err
			}
		} else {
			if err := v.replayNormal(pending); err != nil {
				return err
			}
		}
	}
	if err := v.journal.Reset(); err != nil {
		return err
	}
	v.mu.Lock()
	failed := v.failedDisk
	v.mu.Unlock()
	if failed >= 0 && !v.disks[failed].Failed() {
		v.startRebuild(failed)
	}
	return nil
}

// replayNormal 正常态断电恢复：意图只记录了“准备改哪些块”。
// 此时数据盘/校验盘可能处于旧旧、新旧、新新中的任意组合（写洞），
// 直接以现存全部数据块重算校验即可得到一致条带。
func (v *Volume) replayNormal(pi *pendingIntent) error {
	s := pi.stripe
	parity, data := stripeMap(v.n, s)
	pbuf := make([]byte, BlockSize)
	for k, d := range data {
		buf := make([]byte, BlockSize)
		if err := v.readDisk(d, s, buf); err != nil {
			return err
		}
		if k == 0 {
			copy(pbuf, buf)
		} else {
			xorBlocks(pbuf, buf)
		}
	}
	return v.writeDisk(parity, s, pbuf)
}

// replayDegraded 降级态断电恢复：意图携带该条带全部新数据块与新校验，
// 整体重放到所有存活盘，保证“已确认的写入”不丢、条带一致。
func (v *Volume) replayDegraded(pi *pendingIntent) error {
	s := pi.stripe
	for d := 0; d < v.n; d++ {
		if v.disks[d].Failed() {
			continue
		}
		if err := v.disks[d].WriteBlockForRebuild(s, pi.blocks[d]); err != nil {
			return err
		}
	}
	return nil
}
