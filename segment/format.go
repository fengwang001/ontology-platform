package segment

import (
	"encoding/binary"
	"errors"
)

// 磁盘格式常量。所有多字节整数使用小端序。
const (
	headerSize         = 32
	footerSize         = 8
	indexEntryKeyOff   = 4 // index 条目内 offset 字段之前：klen(4)
	indexEntryTailSize = 8 // offset u64
	crcSize            = 4
	entryPrefixSize    = 8 // klen u32 + vlen u32
	sparseBlockEntries = 16

	magic0      = 0x014745534f544e4f // "ONTOSEG\x01" 小端读出值
	footerMagic = 0x5345474d
	version     = 1
)

var magicBytes = []byte{'O', 'N', 'T', 'O', 'S', 'E', 'G', 1}

var (
	ErrHeaderIncomplete = errors.New("segment: header incomplete or corrupt")
	ErrEntryIncomplete  = errors.New("segment: entry incomplete")
	ErrCRCMismatch      = errors.New("segment: entry CRC mismatch")
	ErrIndexIncomplete  = errors.New("segment: sparse index incomplete or corrupt")
	ErrEmptyKeyOrder    = errors.New("segment: keys must be non-nil and strictly ascending")
)

// Entry 是段内一条键值记录。Tomb 为删除标记；Tomb 时 Value 必须为 nil。
// Key 允许为空字节串，Value 允许为空字节串（与删除标记区分）。
type Entry struct {
	Key   []byte
	Value []byte
	Tomb  bool
}

func putU32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }
func putU64(b []byte, v uint64) { binary.LittleEndian.PutUint64(b, v) }
func u32(b []byte) uint32       { return binary.LittleEndian.Uint32(b) }
func u64(b []byte) uint64       { return binary.LittleEndian.Uint64(b) }

const tombBit uint32 = 1 << 31

// encodedSize 返回一条目的磁盘字节数。
func (e Entry) encodedSize() int {
	return entryPrefixSize + len(e.Key) + len(e.Value) + crcSize
}

// IndexEntry 是稀疏索引中的一个块锚点。
type IndexEntry struct {
	Key    []byte
	Offset int64
}

// Corruption 描述一次段文件损坏的分类结果。
type Corruption struct {
	Err error // ErrHeaderIncomplete / ErrEntryIncomplete / ErrCRCMismatch / ErrIndexIncomplete
}
