package lsm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sort"
)

const (
	magicV1   = "LSM1"
	headerLen = 4 + 8 // magic + segment id

	flagTombstone byte = 1

	recFixedLen = 4 + 4 + 1 + 8 + 4 // keyLen, valueLen, flags, seq, crc
)

// record 是段内一条不可变记录。seq 为全局单调写入序号。
type record struct {
	seq       uint64
	key       string
	value     []byte
	tombstone bool
}

// segment 是一个不可变段。id 为冻结/合并时分配的单调段号，
// records 按 key 排序（同 key 至多一条，合并时一键取段号最大者）。
type segment struct {
	id      uint64
	records []record
}

// encodeSegment 将段序列化为自描述字节：
//
//	magic(4) | id(8 BE) | [ keyLen(4) valueLen(4) flags(1) seq(8) key value crc32c(4) ]*
func encodeSegment(seg *segment) []byte { return encodeSegmentImpl(seg) }

// decodeSegment 解析段。strict=true 时尾部不完整记录返回 ErrSegmentTruncated；
// 非法魔数/标志/CRC 返回 ErrSegmentCorrupt（包装具体偏移原因）。
//
// strict=false 时安全丢弃尾部不完整记录（按最后一条完整记录边界截断）。
func decodeSegment(data []byte, strict bool) (*segment, error) {
	return decodeSegmentImpl(data, strict)
}

// RecoverTruncated 丢弃尾部不完整记录，返回截断后的段；非截断类错误原样返回。
// 损坏类错误（魔数/CRC/键序等）原样返回，不做猜测性恢复。
func RecoverTruncated(data []byte) (*segment, []byte, error) {
	seg, boundary, err := decodePrefix(data, false)
	if err != nil {
		return nil, nil, err
	}
	return seg, append([]byte(nil), data[:boundary]...), nil
}

func crcTable() *crc32.Table { return crc32.MakeTable(crc32.Castagnoli) }

func encodeSegmentImpl(seg *segment) []byte {
	recs := make([]record, len(seg.records))
	copy(recs, seg.records)
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].key < recs[j].key })

	size := headerLen
	for _, r := range recs {
		size += recFixedLen + len(r.key) + len(r.value)
	}
	buf := bytes.NewBuffer(make([]byte, 0, size))
	buf.WriteString(magicV1)
	_ = binary.Write(buf, binary.BigEndian, seg.id)

	var prevKey string
	for _, r := range recs {
		if len(recs) > 1 {
			if r.key == prevKey {
				panic("lsm: segment contains duplicate key")
			}
			if r.key < prevKey {
				panic("lsm: segment records not key-sorted")
			}
		}
		prevKey = r.key

		if len(r.key) == 0 {
			panic("lsm: segment contains empty key")
		}
		var flags byte
		if r.tombstone {
			flags |= flagTombstone
		}
		_ = binary.Write(buf, binary.BigEndian, uint32(len(r.key)))
		_ = binary.Write(buf, binary.BigEndian, uint32(len(r.value)))
		buf.WriteByte(flags)
		_ = binary.Write(buf, binary.BigEndian, r.seq)
		buf.WriteString(r.key)
		buf.Write(r.value)

		h := crc32.New(crcTable())
		h.Write(buf.Bytes()[buf.Len()-(recFixedLen-4+len(r.key)+len(r.value)):])
		_ = binary.Write(buf, binary.BigEndian, h.Sum32())
	}
	return buf.Bytes()
}

// decodePrefix 解析 data，返回段与“已成功提交记录之后的安全截断边界”。
// 尾部不完整记录时 boundary 指向最后一条完整记录结束位置；strict 决定返回错误还是截断。
func decodePrefix(data []byte, strict bool) (*segment, int, error) {
	if len(data) < headerLen {
		if strict {
			return nil, 0, fmt.Errorf("%w: missing header (got %d bytes, want %d)",
				ErrSegmentCorrupt, len(data), headerLen)
		}
		return nil, 0, fmt.Errorf("%w: missing header (got %d bytes, want %d)",
			ErrSegmentCorrupt, len(data), headerLen)
	}
	if string(data[:4]) != magicV1 {
		return nil, 0, fmt.Errorf("%w: bad magic %q", ErrSegmentCorrupt, data[:4])
	}
	seg := &segment{id: binary.BigEndian.Uint64(data[4:headerLen])}

	off := headerLen
	var prevKey string
	for off < len(data) {
		recStart := off
		if len(data)-off < recFixedLen {
			// 连固定头都不完整：安全边界是 recStart，整段尾部均可截断。
			if strict {
				return nil, recStart, fmt.Errorf("%w at offset %d", ErrSegmentTruncated, recStart)
			}
			return seg, recStart, nil
		}
		keyLen := int(binary.BigEndian.Uint32(data[off : off+4]))
		valueLen := int(binary.BigEndian.Uint32(data[off+4 : off+8]))
		flags := data[off+8]
		seq := binary.BigEndian.Uint64(data[off+9 : off+17])
		payloadEnd := off + recFixedLen - 4 + keyLen + valueLen
		recEnd := payloadEnd + 4
		if keyLen == 0 {
			return nil, recStart, fmt.Errorf("%w: empty key at offset %d", ErrSegmentCorrupt, recStart)
		}
		if flags&^flagTombstone != 0 {
			return nil, recStart, fmt.Errorf("%w: illegal flags %#x at offset %d",
				ErrSegmentCorrupt, flags, recStart)
		}
		if valueLen > 0 && flags&flagTombstone != 0 {
			return nil, recStart, fmt.Errorf("%w: tombstone with non-empty value at offset %d",
				ErrSegmentCorrupt, recStart)
		}
		if keyLen+valueLen < keyLen { // 溢出防御
			return nil, recStart, fmt.Errorf("%w: length overflow at offset %d", ErrSegmentCorrupt, recStart)
		}
		if recEnd > len(data) {
			// 声明长度超出剩余字节：记录不完整（写了一半），可在 recStart 处安全截断。
			if strict {
				return nil, recStart, fmt.Errorf("%w: record at offset %d declares %d bytes but %d remain",
					ErrSegmentTruncated, recStart, recEnd-recStart, len(data)-recStart)
			}
			return seg, recStart, nil
		}

		wantCRC := binary.BigEndian.Uint32(data[payloadEnd:recEnd])
		gotCRC := crc32.Checksum(data[off:payloadEnd], crcTable())
		if wantCRC != gotCRC {
			return nil, recStart, fmt.Errorf("%w: crc mismatch at offset %d", ErrSegmentCorrupt, recStart)
		}

		key := string(data[off+17 : off+17+keyLen])
		if len(seg.records) > 0 && key <= prevKey {
			return nil, recStart, fmt.Errorf("%w: keys not strictly ascending (%q after %q) at offset %d",
				ErrSegmentCorrupt, prevKey, key, recStart)
		}
		prevKey = key

		r := record{seq: seq, key: key, tombstone: flags&flagTombstone != 0}
		if !r.tombstone {
			r.value = append([]byte(nil), data[off+17+keyLen:payloadEnd]...)
		}
		seg.records = append(seg.records, r)
		off = recEnd
	}
	return seg, off, nil
}

func decodeSegmentImpl(data []byte, strict bool) (*segment, error) {
	seg, _, err := decodePrefix(data, strict)
	if err != nil {
		return nil, err
	}
	return seg, nil
}
