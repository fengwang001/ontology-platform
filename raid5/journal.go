package raid5

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
)

// 意图日志记录格式（小端序）：
//
//   头（所有记录）：
//     [0:8]   magic   = 0x52414944354A4E4C ("RAID5JNL")
//     [8:12]  length  = 记录体（含 crc 与头之后）字节数
//     [12:13] kind    = 1 意图 / 2 提交
//     [13:17] stripe  条带号
//   意图体（kind=1）：
//     [17]    degraded    0/1
//     [18]    bitmap[N]   本笔写入覆盖的数据槽位图（正常态 RMW 用）
//     若 degraded=1，随后为整体重放数据：N 个块按盘号 0..N-1 排列，
//       包含该条带的全部新数据块与新校验块；失效盘位置为全零占位。
//   提交体（kind=2）：无附加内容。
//   尾：
//     crc32(IEEE)，覆盖头（含 magic/length/kind/stripe）与体。
//
// 每次写入前先 append 意图记录并 fsync；写盘完成后 append 提交记录并 fsync。
// 恢复时只重放“有意图、无提交”的最后一条记录；重放后日志整体截断清空。

const (
	journalMagic   uint64 = 0x52414944354A4E4C
	journalHeader         = 17
	journalCRCSize        = 4
	kindIntent            = 1
	kindCommit            = 2
)

// Journal 是只追加的意图日志文件。
type Journal struct {
	f journalFile
}

// OpenJournal 打开（必要时创建）日志文件。
func OpenJournal(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	return &Journal{f: f}, nil
}

// journalFile 抽象日志存储（生产用 *os.File，测试用内存实现，
// 保证“断电”后日志字节仍然保留）。
type journalFile interface {
	io.Reader
	io.Writer
	io.Seeker
	Truncate(size int64) error
	Sync() error
	Close() error
}

// NewJournal 用给定存储创建日志（供测试注入内存实现）。
func NewJournal(f journalFile) *Journal { return &Journal{f: f} }

func recordBodyLen(n int, degraded bool, bitmapLen int) int {
	body := 1 + bitmapLen // degraded 标志 + 位图
	if degraded {
		body += n * BlockSize
	}
	return body
}

// AppendIntent 追加一条意图记录并落盘。
// blocks 按盘号 0..N-1 排列（仅降级态需要，正常态传 nil）。
func (j *Journal) AppendIntent(stripe int, bitmap []bool, degraded bool, blocks [][]byte) error {
	n := len(bitmap) + 1
	bodyLen := recordBodyLen(n, degraded, len(bitmap))
	rec := make([]byte, journalHeader+bodyLen+journalCRCSize)
	binary.LittleEndian.PutUint64(rec[0:8], journalMagic)
	binary.LittleEndian.PutUint32(rec[8:12], uint32(bodyLen+journalCRCSize))
	rec[12] = kindIntent
	binary.LittleEndian.PutUint32(rec[13:17], uint32(stripe))
	if degraded {
		rec[17] = 1
	}
	for k, on := range bitmap {
		if on {
			rec[18+k] = 1
		}
	}
	if degraded {
		off := journalHeader + 1 + len(bitmap)
		for d := 0; d < n; d++ {
			copy(rec[off+d*BlockSize:off+(d+1)*BlockSize], blocks[d])
		}
	}
	sum := crc32.ChecksumIEEE(rec[:journalHeader+bodyLen])
	binary.LittleEndian.PutUint32(rec[journalHeader+bodyLen:], sum)
	return j.append(rec)
}

// AppendCommit 追加一条提交记录并落盘。
func (j *Journal) AppendCommit(stripe int) error {
	rec := make([]byte, journalHeader+journalCRCSize)
	binary.LittleEndian.PutUint64(rec[0:8], journalMagic)
	binary.LittleEndian.PutUint32(rec[8:12], journalCRCSize)
	rec[12] = kindCommit
	binary.LittleEndian.PutUint32(rec[13:17], uint32(stripe))
	sum := crc32.ChecksumIEEE(rec[:journalHeader])
	binary.LittleEndian.PutUint32(rec[journalHeader:], sum)
	return j.append(rec)
}

func (j *Journal) append(rec []byte) error {
	if _, err := j.f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if _, err := j.f.Write(rec); err != nil {
		return err
	}
	return j.f.Sync()
}

// Reset 清空日志（恢复重放完成后调用，保证日志长度确定）。
func (j *Journal) Reset() error {
	if err := j.f.Truncate(0); err != nil {
		return err
	}
	return j.f.Sync()
}

func (j *Journal) Close() error { return j.f.Close() }

// pendingIntent 是恢复时解析出的未完成意图。
type pendingIntent struct {
	stripe   int
	degraded bool
	bitmap   []bool
	// blocks 按盘号排列，仅 degraded 时有效。
	blocks [][]byte
}

// Parse 扫描日志，返回最后一条“有意图但无提交”的记录。
// 损坏或写了一半（含断电导致的部分块）的尾部记录按不存在处理；
// 已提交的意图不返回。N 为盘数，用于确定位图宽度。
func (j *Journal) Parse(n int) (*pendingIntent, error) {
	if _, err := j.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(j.f)
	if err != nil {
		return nil, err
	}
	var pending *pendingIntent
	pos := 0
	for pos < len(data) {
		if len(data)-pos < journalHeader {
			break // 残缺尾记录
		}
		if binary.LittleEndian.Uint64(data[pos:pos+8]) != journalMagic {
			break
		}
		bodyLen := int(binary.LittleEndian.Uint32(data[pos+8 : pos+12]))
		total := journalHeader + bodyLen
		if bodyLen < journalCRCSize || total > len(data)-pos {
			break // 长度非法或记录被断电截断
		}
		kind := data[pos+12]
		stripe := int(binary.LittleEndian.Uint32(data[pos+13 : pos+17]))
		storedCRC := binary.LittleEndian.Uint32(data[pos+total-journalCRCSize : pos+total])
		if crc32.ChecksumIEEE(data[pos:pos+total-journalCRCSize]) != storedCRC {
			break // 撕裂/损坏记录：它之后不可能再有有效记录
		}
		switch kind {
		case kindIntent:
			pi := &pendingIntent{stripe: stripe}
			pi.degraded = data[pos+17] == 1
			bitmapBytes := data[pos+18 : pos+18+(n-1)]
			pi.bitmap = make([]bool, n-1)
			for k := 0; k < n-1; k++ {
				pi.bitmap[k] = bitmapBytes[k] == 1
			}
			if pi.degraded {
				off := pos + journalHeader + 1 + (n - 1)
				pi.blocks = make([][]byte, n)
				for d := 0; d < n; d++ {
					pi.blocks[d] = make([]byte, BlockSize)
					copy(pi.blocks[d], data[off+d*BlockSize:off+(d+1)*BlockSize])
				}
			}
			pending = pi
		case kindCommit:
			pending = nil
		default:
			break
		}
		pos += total
	}
	return pending, nil
}
