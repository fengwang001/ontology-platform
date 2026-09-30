package lsm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// 记录帧布局（全部小端）：
//
//	[0:4]   CRC32(payload)，payload 为 crc 字段之后的全部字节
//	[4]     类型：0=普通记录，1=墓碑
//	[5:13]  段内序号 Seq
//	[13:17] 键长度 klen
//	[17:21] 值长度 vlen
//	[21:]   键 || 值
const recordHeaderSize = 21

var (
	// errIncomplete 表示缓冲区中的记录不完整（安全截断点）。
	errIncomplete = errors.New("lsm: incomplete record")
	// errCorrupt 表示记录内容非法（类型未知或 CRC 校验失败）。
	errCorrupt = errors.New("lsm: corrupt record")
)

// Record 是一条键值记录。Tombstone 为 true 表示删除标记。
type Record struct {
	Key       []byte
	Value     []byte
	Seq       uint64
	Tombstone bool
}

// encodeRecord 将记录编码为带 CRC 校验的二进制帧。
func encodeRecord(rec Record) []byte {
	buf := make([]byte, recordHeaderSize+len(rec.Key)+len(rec.Value))
	if rec.Tombstone {
		buf[4] = 1
	}
	binary.LittleEndian.PutUint64(buf[5:13], rec.Seq)
	binary.LittleEndian.PutUint32(buf[13:17], uint32(len(rec.Key)))
	binary.LittleEndian.PutUint32(buf[17:21], uint32(len(rec.Value)))
	copy(buf[recordHeaderSize:], rec.Key)
	copy(buf[recordHeaderSize+len(rec.Key):], rec.Value)
	binary.LittleEndian.PutUint32(buf[0:4], crc32.ChecksumIEEE(buf[4:]))
	return buf
}

// decodeRecord 从 buf 中解码一条记录，返回记录与消耗的字节数。
// 数据不完整时返回 errIncomplete，内容非法时返回 errCorrupt。
func decodeRecord(buf []byte) (Record, int, error) {
	if len(buf) < recordHeaderSize {
		return Record{}, 0, errIncomplete
	}
	crc := binary.LittleEndian.Uint32(buf[0:4])
	typ := buf[4]
	seq := binary.LittleEndian.Uint64(buf[5:13])
	klen := binary.LittleEndian.Uint32(buf[13:17])
	vlen := binary.LittleEndian.Uint32(buf[17:21])
	total := recordHeaderSize + int(klen) + int(vlen)
	if int(klen) < 0 || int(vlen) < 0 || total < recordHeaderSize {
		return Record{}, 0, fmt.Errorf("%w: bad length", errCorrupt)
	}
	if len(buf) < total {
		return Record{}, 0, errIncomplete
	}
	if crc32.ChecksumIEEE(buf[4:total]) != crc {
		return Record{}, 0, fmt.Errorf("%w: crc mismatch", errCorrupt)
	}
	if typ > 1 {
		return Record{}, 0, fmt.Errorf("%w: unknown type %d", errCorrupt, typ)
	}
	if klen == 0 {
		return Record{}, 0, fmt.Errorf("%w: empty key", errCorrupt)
	}
	rec := Record{
		Key:       append([]byte(nil), buf[recordHeaderSize:recordHeaderSize+klen]...),
		Value:     append([]byte(nil), buf[recordHeaderSize+klen:total]...),
		Seq:       seq,
		Tombstone: typ == 1,
	}
	return rec, total, nil
}
