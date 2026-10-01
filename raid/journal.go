package raid

import (
	"encoding/binary"
	"fmt"
)

const (
	journalMagic   uint32 = 0x50524A4C // "PRJL"
	journalVersion uint32 = 1
	headerMagic    uint32 = 0x50524844 // "PRHD"
	flagRecompute  uint32 = 1 << 0
)

// payload 是降级状态下随意图记录落盘的一次确定重放写。
type payload struct {
	disk  int
	block int
	data  []byte
}

// journalEntry 单个条带的意图记录。
// recompute 为 true（正常状态）时，恢复按数据块重算该校验；
// 否则（降级状态）整体重放 payloads 中的新数据与新校验。
type journalEntry struct {
	slot      int
	stripe    int
	recompute bool
	payloads  []payload
}

// journal 固定槽位的写意图日志：每个条带一个槽。
// 布局：块 0 为超级块；槽 s 占 1 个头部块 + n 个负载块。
// 提交顺序：先写负载块，最后写头部块（带校验和），头部落盘即生效。
type journal struct {
	dev           Device
	n             int
	stripes       int
	blocksPerSlot int
	failed        int
}

func journalBlocks(n, stripes int) int {
	return 1 + stripes*(1+n)
}

func checksumBytes(buf []byte) uint32 {
	var sum uint32
	for _, b := range buf {
		sum = sum*31 + uint32(b)
	}
	return sum
}

// openJournal 打开日志；超级块不存在时格式化，校验失败时报错。
func openJournal(dev Device, n, stripes int) (*journal, error) {
	j := &journal{dev: dev, n: n, stripes: stripes, blocksPerSlot: 1 + n, failed: -1}
	if dev.NumBlocks() < journalBlocks(n, stripes) {
		return nil, fmt.Errorf("%w: journal device too small, need %d blocks", ErrGeometryMismatch, journalBlocks(n, stripes))
	}
	blk, err := dev.ReadBlock(0)
	if err != nil {
		return nil, err
	}
	magic := binary.LittleEndian.Uint32(blk[0:4])
	if magic != journalMagic {
		if err := j.format(); err != nil {
			return nil, err
		}
		return j, nil
	}
	sum := binary.LittleEndian.Uint32(blk[20:24])
	if sum != checksumBytes(blk[:20]) {
		return nil, ErrJournalCorrupt
	}
	if binary.LittleEndian.Uint32(blk[8:12]) != uint32(n) ||
		binary.LittleEndian.Uint32(blk[12:16]) != uint32(stripes) {
		return nil, fmt.Errorf("%w: journal geometry", ErrGeometryMismatch)
	}
	j.failed = int(int32(binary.LittleEndian.Uint32(blk[16:20])))
	return j, nil
}

func (j *journal) format() error {
	if err := j.writeSuperblock(-1); err != nil {
		return err
	}
	j.failed = -1
	for s := 0; s < j.stripes; s++ {
		if err := j.clear(s); err != nil {
			return err
		}
	}
	return nil
}

func (j *journal) writeSuperblock(failed int) error {
	buf := make([]byte, j.dev.BlockSize())
	binary.LittleEndian.PutUint32(buf[0:4], journalMagic)
	binary.LittleEndian.PutUint32(buf[4:8], journalVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(j.n))
	binary.LittleEndian.PutUint32(buf[12:16], uint32(j.stripes))
	binary.LittleEndian.PutUint32(buf[16:20], uint32(int32(failed)))
	binary.LittleEndian.PutUint32(buf[20:24], checksumBytes(buf[:20]))
	return j.dev.WriteBlock(0, buf)
}

// setFailedDisk 把失效盘编号持久化到超级块（-1 表示无失效盘）。
func (j *journal) setFailedDisk(d int) error {
	if err := j.writeSuperblock(d); err != nil {
		return err
	}
	j.failed = d
	return nil
}

func (j *journal) headerBlock(slot int) int {
	return 1 + slot*j.blocksPerSlot
}

// set 写入意图记录：先落负载块，再落头部块（提交点）。
func (j *journal) set(slot int, e *journalEntry) error {
	if len(e.payloads) > j.n {
		return fmt.Errorf("%w: too many payloads", ErrGeometryMismatch)
	}
	base := j.headerBlock(slot)
	for i, p := range e.payloads {
		if err := j.dev.WriteBlock(base+1+i, p.data); err != nil {
			return err
		}
	}
	buf := make([]byte, j.dev.BlockSize())
	binary.LittleEndian.PutUint32(buf[0:4], headerMagic)
	binary.LittleEndian.PutUint32(buf[4:8], 1) // valid
	binary.LittleEndian.PutUint32(buf[8:12], uint32(e.stripe))
	var flags uint32
	if e.recompute {
		flags = flagRecompute
	}
	binary.LittleEndian.PutUint32(buf[12:16], flags)
	binary.LittleEndian.PutUint32(buf[16:20], uint32(len(e.payloads)))
	off := 20
	for _, p := range e.payloads {
		binary.LittleEndian.PutUint32(buf[off:off+4], uint32(p.disk))
		binary.LittleEndian.PutUint32(buf[off+4:off+8], uint32(p.block))
		off += 8
	}
	binary.LittleEndian.PutUint32(buf[off:off+4], checksumBytes(buf[:off]))
	return j.dev.WriteBlock(base, buf)
}

// clear 将槽位标记为无效（写无效头部）。
func (j *journal) clear(slot int) error {
	buf := make([]byte, j.dev.BlockSize())
	binary.LittleEndian.PutUint32(buf[0:4], headerMagic)
	// valid = 0，其余字段为零；校验和仍覆盖，保证可识别。
	binary.LittleEndian.PutUint32(buf[20:24], checksumBytes(buf[:20]))
	return j.dev.WriteBlock(j.headerBlock(slot), buf)
}

// entries 扫描全部槽位，返回有效意图记录（按槽位顺序，确定性）。
func (j *journal) entries() ([]journalEntry, error) {
	var out []journalEntry
	for slot := 0; slot < j.stripes; slot++ {
		base := j.headerBlock(slot)
		buf, err := j.dev.ReadBlock(base)
		if err != nil {
			return nil, err
		}
		if binary.LittleEndian.Uint32(buf[0:4]) != headerMagic {
			continue
		}
		if binary.LittleEndian.Uint32(buf[4:8]) != 1 {
			continue
		}
		count := int(binary.LittleEndian.Uint32(buf[16:20]))
		if count < 0 || count > j.n {
			continue
		}
		off := 20 + count*8
		if binary.LittleEndian.Uint32(buf[off:off+4]) != checksumBytes(buf[:off]) {
			continue // 头部撕裂，视为无效
		}
		e := journalEntry{
			slot:      slot,
			stripe:    int(binary.LittleEndian.Uint32(buf[8:12])),
			recompute: binary.LittleEndian.Uint32(buf[12:16])&flagRecompute != 0,
		}
		for i := 0; i < count; i++ {
			poff := 20 + i*8
			data, err := j.dev.ReadBlock(base + 1 + i)
			if err != nil {
				return nil, err
			}
			e.payloads = append(e.payloads, payload{
				disk:  int(binary.LittleEndian.Uint32(buf[poff : poff+4])),
				block: int(binary.LittleEndian.Uint32(buf[poff+4 : poff+8])),
				data:  data,
			})
		}
		out = append(out, e)
	}
	return out, nil
}
