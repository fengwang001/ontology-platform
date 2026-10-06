package logkv

import (
	"encoding/binary"
	"hash/crc32"
)

// 提示文件布局（全部小端）：
//
//	magic       "LKH1" 4 字节
//	validBytes  u64   段有效字节数，须等于段文件实际大小
//	entryCount  u32
//	entries     entryCount 条，每条：
//	  seq     u64
//	  offset  u64
//	  length  u32  整条记录字节数
//	  flags   u8   bit0 = 删除标记
//	  keyLen  u32
//	  key     [keyLen]byte
//	crc         u32  对之前全部字节的 CRC32(Castagnoli)
//
// 提示信息不含值。任何字段被篡改都会使自带校验失败而作废。
const hintMagic = "LKH1"

// HintEntry 是提示信息中一个键的最新记录位置。
type HintEntry struct {
	Seq       uint64
	Offset    uint64
	Length    uint32
	Tombstone bool
	Key       []byte
}

// HintData 是一份解码后的提示信息。
type HintData struct {
	ValidBytes uint64
	Entries    []HintEntry
}

// EncodeHint 序列化提示信息并附加自带校验值。
func EncodeHint(h HintData) []byte {
	size := 4 + 8 + 4 + 4
	for _, e := range h.Entries {
		size += 8 + 8 + 4 + 1 + 4 + len(e.Key)
	}
	buf := make([]byte, 0, size)
	buf = append(buf, hintMagic...)
	buf = binary.LittleEndian.AppendUint64(buf, h.ValidBytes)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(h.Entries)))
	for _, e := range h.Entries {
		buf = binary.LittleEndian.AppendUint64(buf, e.Seq)
		buf = binary.LittleEndian.AppendUint64(buf, e.Offset)
		buf = binary.LittleEndian.AppendUint32(buf, e.Length)
		var flags byte
		if e.Tombstone {
			flags = flagTombstone
		}
		buf = append(buf, flags)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(e.Key)))
		buf = append(buf, e.Key...)
	}
	crc := crc32.Checksum(buf, crcTable)
	return binary.LittleEndian.AppendUint32(buf, crc)
}

// DecodeHint 校验并解析提示信息。自带校验失败或结构不完整时
// 返回 ok=false，调用方必须作废提示信息并回退到全段扫描。
func DecodeHint(buf []byte) (HintData, bool) {
	var h HintData
	if len(buf) < 4+8+4+4 {
		return h, false
	}
	if string(buf[:4]) != hintMagic {
		return h, false
	}
	stored := binary.LittleEndian.Uint32(buf[len(buf)-4:])
	if crc32.Checksum(buf[:len(buf)-4], crcTable) != stored {
		return h, false
	}
	body := buf[4 : len(buf)-4]
	h.ValidBytes = binary.LittleEndian.Uint64(body[0:8])
	count := binary.LittleEndian.Uint32(body[8:12])
	rest := body[12:]
	entries := make([]HintEntry, 0, count)
	for i := uint32(0); i < count; i++ {
		if len(rest) < 8+8+4+1+4 {
			return HintData{}, false
		}
		e := HintEntry{
			Seq:       binary.LittleEndian.Uint64(rest[0:8]),
			Offset:    binary.LittleEndian.Uint64(rest[8:16]),
			Length:    binary.LittleEndian.Uint32(rest[16:20]),
			Tombstone: rest[20]&flagTombstone != 0,
		}
		keyLen := binary.LittleEndian.Uint32(rest[21:25])
		rest = rest[25:]
		if keyLen == 0 || len(rest) < int(keyLen) {
			return HintData{}, false
		}
		e.Key = append([]byte(nil), rest[:keyLen]...)
		rest = rest[keyLen:]
		entries = append(entries, e)
	}
	if len(rest) != 0 {
		return HintData{}, false
	}
	h.Entries = entries
	return h, true
}
