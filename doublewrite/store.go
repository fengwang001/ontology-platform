package doublewrite

import (
	"errors"
	"fmt"
	"sync"
)

// SectorSize 是磁盘扇区大小（字节）。扇区是原子写单位：一次掉电只会让
// “正在写的那个扇区”落下前若干字节，已写扇区保证完整。
const SectorSize = 512

var (
	errOffsetUnaligned  = errors.New("doublewrite: disk offset not sector aligned")
	errLengthUnaligned  = errors.New("doublewrite: write length not sector aligned")
	errOffsetOutOfRange = errors.New("doublewrite: disk offset out of range")
)

// SectorDisk 模拟一块按扇区原子落盘的磁盘。
//
// 掉电注入：设置 CrashAfterSectors=n 后，从本次设置开始计数扇区写，
// 第 1..n 个扇区完整落盘，下一个扇区（第 n+1 个）只落下 PartialBytes
// 个字节（0..SectorSize-1），随后 WriteSectors 返回 ErrPowerLoss。
// n 以本次 FlushBatch 发起的写序列为单位，因此可以逐扇区边界枚举掉电点。
type SectorDisk struct {
	mu sync.Mutex

	sectors [][]byte // 每个扇区长 SectorSize

	// 掉电注入状态（受 mu 保护）。
	armed        bool
	crashAfter   int // 还允许完整落盘的扇区数
	partialBytes int // 掉电扇区落下的前缀字节数
	sectorsDone  int
	crashed      bool

	// writes 记录自上次 ArmCrash 以来注入窗口内每次写入的 (扇区偏移, 扇区数)，
	// 供测试断言写入顺序与阶段划分。
	writes []WriteRecord
}

// WriteRecord 描述一次按顺序的扇区写。
type WriteRecord struct {
	SectorOffset int
	SectorCount  int
	Phase        string
}

// ErrPowerLoss 表示在本次写入过程中发生了模拟掉电。
var ErrPowerLoss = errors.New("doublewrite: simulated power loss")

// NewSectorDisk 创建 totalSectors 个扇区的空白磁盘。
func NewSectorDisk(totalSectors int) *SectorDisk {
	d := &SectorDisk{sectors: make([][]byte, totalSectors)}
	for i := range d.sectors {
		d.sectors[i] = make([]byte, SectorSize)
	}
	return d
}

// TotalSectors 返回磁盘扇区总数。
func (d *SectorDisk) TotalSectors() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sectors)
}

// TotalBytes 返回磁盘字节容量。
func (d *SectorDisk) TotalBytes() int { return d.TotalSectors() * SectorSize }

// Clone 返回内容完全一致的独立副本（不带掉电注入状态），用于让同一个
// 刷写序列在同一个掉电点可重复执行、得到逐字节相同的结果。
func (d *SectorDisk) Clone() *SectorDisk {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := &SectorDisk{sectors: make([][]byte, len(d.sectors))}
	for i, s := range d.sectors {
		c.sectors[i] = append([]byte(nil), s...)
	}
	return c
}

// ArmCrash 注入掉电：再完整落盘 crashAfter 个扇区后，在下一扇区只写
// partialBytes 字节即掉电。crashAfter 可以大于等于本序列总扇区数，
// 表示该序列不掉电（供覆盖“序列结束之后”的检查点）。
func (d *SectorDisk) ArmCrash(crashAfter, partialBytes int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if partialBytes < 0 || partialBytes >= SectorSize {
		panic(fmt.Sprintf("doublewrite: partialBytes must be in [0,%d)", SectorSize))
	}
	d.armed = true
	d.crashAfter = crashAfter
	d.partialBytes = partialBytes
	d.sectorsDone = 0
	d.crashed = false
	d.writes = nil
}

// Crashed 报告是否已经触发过模拟掉电。
func (d *SectorDisk) Crashed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.crashed
}

// WriteRecords 返回掉电窗口内记录到的写序列快照。
func (d *SectorDisk) WriteRecords() []WriteRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]WriteRecord, len(d.writes))
	copy(out, d.writes)
	return out
}

// WriteSectors 从字节偏移 off 起按扇区顺序写入 data（长度必须扇区对齐）。
func (d *SectorDisk) WriteSectors(off int, data []byte, phase string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if off%SectorSize != 0 {
		return errOffsetUnaligned
	}
	if len(data)%SectorSize != 0 {
		return errLengthUnaligned
	}
	first := off / SectorSize
	count := len(data) / SectorSize
	if off < 0 || first+count > len(d.sectors) {
		return errOffsetOutOfRange
	}
	// 只记录“掉电注入窗口”内的写序列（含掉电时部分落盘的那一次写）。
	if d.armed {
		d.writes = append(d.writes, WriteRecord{first, count, phase})
	}
	for i := 0; i < count; i++ {
		src := data[i*SectorSize : (i+1)*SectorSize]
		if d.armed && !d.crashed {
			if d.sectorsDone >= d.crashAfter {
				// 正在写的扇区：只落盘前 partialBytes 个字节，随后掉电。
				dst := d.sectors[first+i]
				for b := 0; b < d.partialBytes; b++ {
					dst[b] = src[b]
				}
				d.crashed = true
				return ErrPowerLoss
			}
			d.sectorsDone++
		}
		copy(d.sectors[first+i], src)
	}
	return nil
}

// ReadSectors 读取从 off 起的 len(data) 个字节（需扇区对齐）。
func (d *SectorDisk) ReadSectors(off int, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if off%SectorSize != 0 || len(data)%SectorSize != 0 {
		return errOffsetUnaligned
	}
	first := off / SectorSize
	count := len(data) / SectorSize
	if off < 0 || first+count > len(d.sectors) {
		return errOffsetOutOfRange
	}
	for i := 0; i < count; i++ {
		copy(data[i*SectorSize:(i+1)*SectorSize], d.sectors[first+i])
	}
	return nil
}

// CorruptByte 静默破坏指定字节偏移处的一个比特，且不写任何扇区头以外的
// 簿记信息——它直接改磁盘内容，用于制造“原位校验失败且无可用副本”。
func (d *SectorDisk) CorruptByte(byteOffset int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if byteOffset < 0 || byteOffset >= len(d.sectors)*SectorSize {
		return errOffsetOutOfRange
	}
	s := byteOffset / SectorSize
	b := byteOffset % SectorSize
	d.sectors[s][b] ^= 0xFF
	return nil
}

// Bytes 返回整个磁盘内容的拷贝，供测试做逐字节比对。
func (d *SectorDisk) Bytes() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]byte, len(d.sectors)*SectorSize)
	for i, s := range d.sectors {
		copy(out[i*SectorSize:], s)
	}
	return out
}
