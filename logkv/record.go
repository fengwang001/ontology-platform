package logkv

import (
	"encoding/binary"
	"hash/crc32"
)

// 记录布局（全部小端）：
//
//	crc     u32  对 [4, 8+length) 区间的 CRC32(Castagnoli)
//	length  u32  本字段之后剩余的字节数，即记录总长 = 8 + length
//	seq     u64  全局唯一且严格递增的写序号
//	flags   u8   bit0 = 删除标记
//	keyLen  u32  键长，必须 > 0
//	valLen  u32  值长，删除标记记录必须为 0
//	key     [keyLen]byte
//	value   [valLen]byte
const (
	recordHeaderSize = 25 // crc+length+seq+flags+keyLen+valLen
	recordMinLength  = recordHeaderSize - 8 + 1
	flagTombstone    = 1
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// Record 是一条已解码的日志记录。
type Record struct {
	Seq       uint64
	Key       []byte
	Value     []byte
	Tombstone bool
}

// RecordSize 返回一条记录编码后的总字节数。
func RecordSize(keyLen, valLen int) int {
	return recordHeaderSize + keyLen + valLen
}

// EncodeRecord 把一条记录编码为可直接追加的字节串。
func EncodeRecord(seq uint64, key, value []byte, tombstone bool) []byte {
	buf := make([]byte, RecordSize(len(key), len(value)))
	length := uint32(len(buf) - 8)
	binary.LittleEndian.PutUint32(buf[4:8], length)
	binary.LittleEndian.PutUint64(buf[8:16], seq)
	var flags byte
	if tombstone {
		flags = flagTombstone
	}
	buf[16] = flags
	binary.LittleEndian.PutUint32(buf[17:21], uint32(len(key)))
	binary.LittleEndian.PutUint32(buf[21:25], uint32(len(value)))
	copy(buf[recordHeaderSize:], key)
	copy(buf[recordHeaderSize+len(key):], value)
	binary.LittleEndian.PutUint32(buf[0:4], crc32.Checksum(buf[4:], crcTable))
	return buf
}

// recordHeader 是解析后的定长头。
type recordHeader struct {
	length uint32 // 头 8 字节之后的字节数
	seq    uint64
	flags  byte
	keyLen uint32
	valLen uint32
}

func parseRecordHeader(b []byte) recordHeader {
	return recordHeader{
		length: binary.LittleEndian.Uint32(b[4:8]),
		seq:    binary.LittleEndian.Uint64(b[8:16]),
		flags:  b[16],
		keyLen: binary.LittleEndian.Uint32(b[17:21]),
		valLen: binary.LittleEndian.Uint32(b[21:25]),
	}
}

// total 返回整条记录的字节数。
func (h recordHeader) total() int64 {
	return 8 + int64(h.length)
}

// consistent 报告头部字段是否自洽（长度与键值长度吻合、键非空、
// 删除标记不携带值、无未知标志位）。
func (h recordHeader) consistent() bool {
	if h.flags&^flagTombstone != 0 {
		return false
	}
	if h.keyLen == 0 {
		return false
	}
	if h.flags&flagTombstone != 0 && h.valLen != 0 {
		return false
	}
	want := uint32(recordHeaderSize-8) + h.keyLen + h.valLen
	return h.length == want
}

// decodeRecord 校验并解码一段完整的记录字节。调用方保证
// len(buf) == 8+length 且头部自洽。
func decodeRecord(buf []byte) (Record, bool) {
	if len(buf) < recordHeaderSize {
		return Record{}, false
	}
	stored := binary.LittleEndian.Uint32(buf[0:4])
	if crc32.Checksum(buf[4:], crcTable) != stored {
		return Record{}, false
	}
	h := parseRecordHeader(buf)
	rec := Record{
		Seq:       h.seq,
		Tombstone: h.flags&flagTombstone != 0,
		Key:       buf[recordHeaderSize : recordHeaderSize+h.keyLen],
	}
	if h.valLen > 0 {
		rec.Value = buf[recordHeaderSize+h.keyLen : recordHeaderSize+h.keyLen+h.valLen]
	}
	return rec, true
}
