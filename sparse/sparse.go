// Package sparse 实现段的稀疏偏移索引：每隔 N 条记录存一个
// 「序号 -> 段内字节偏移」锚点，支持 floor 查找。索引完全可由段重建。
package sparse

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const (
	magic       = "ONIDX"
	version     = byte(1)
	idxHeader   = 25 // magic5 + ver1 + first8 + step4 + count4 + crc4
	anchorSize  = 12 // seq8 + offset4
)

// Anchor 是一个稀疏锚点：序号 seq 的记录位于段数据区 offset 字节处。
type Anchor struct {
	Seq    uint64
	Offset uint32
}

// Index 是一段的稀疏索引。锚点按 Seq 严格升序。
type Index struct {
	FirstSeq uint64
	Step     uint32
	Anchors  []Anchor
}

var (
	// ErrIndexCorrupt：索引文件头 CRC 或锚点 CRC 不符 / 结构非法。
	ErrIndexCorrupt = errors.New("sparse: index corrupt")
	// ErrEmptyIndex：空索引无法 floor 查找（对应空段，无需定位）。
	ErrEmptyIndex = errors.New("sparse: empty index")
)

// Encode 把索引序列化为确定性字节：头 + 锚点 + 每条锚点的 CRC32。
func (x Index) Encode() ([]byte, error) {
	n := len(x.Anchors)
	buf := make([]byte, idxHeader+n*(anchorSize+crcSize))
	copy(buf[:5], magic)
	buf[5] = version
	binary.BigEndian.PutUint64(buf[6:14], x.FirstSeq)
	binary.BigEndian.PutUint32(buf[14:18], x.Step)
	binary.BigEndian.PutUint32(buf[18:22], uint32(n))
	binary.BigEndian.PutUint32(buf[22:26], crc32.ChecksumIEEE(buf[:22]))
	pos := idxHeader
	for _, a := range x.Anchors {
		binary.BigEndian.PutUint64(buf[pos:pos+8], a.Seq)
		binary.BigEndian.PutUint32(buf[pos+8:pos+12], a.Offset)
		binary.BigEndian.PutUint32(buf[pos+12:pos+16],
			crc32.ChecksumIEEE(buf[pos:pos+12]))
		pos += anchorSize + crcSize
	}
	return buf, nil
}

// Decode 解析 Encode 的产物，校验头与每条锚点的 CRC。
func Decode(raw []byte) (Index, error) {
	if len(raw) < idxHeader {
		return Index{}, ErrIndexCorrupt
	}
	if string(raw[:5]) != magic || raw[5] != version {
		return Index{}, ErrIndexCorrupt
	}
	if binary.BigEndian.Uint32(raw[22:26]) != crc32.ChecksumIEEE(raw[:22]) {
		return Index{}, ErrIndexCorrupt
	}
	n := int(binary.BigEndian.Uint32(raw[18:22]))
	if len(raw) != idxHeader+n*(anchorSize+crcSize) {
		return Index{}, ErrIndexCorrupt
	}
	x := Index{
		FirstSeq: binary.BigEndian.Uint64(raw[6:14]),
		Step:     binary.BigEndian.Uint32(raw[14:18]),
		Anchors:  make([]Anchor, 0, n),
	}
	pos := idxHeader
	for i := 0; i < n; i++ {
		block := raw[pos : pos+anchorSize]
		got := binary.BigEndian.Uint32(raw[pos+anchorSize : pos+anchorSize+crcSize])
		if got != crc32.ChecksumIEEE(block) {
			return Index{}, ErrIndexCorrupt
		}
		x.Anchors = append(x.Anchors, Anchor{
			Seq:    binary.BigEndian.Uint64(block[:8]),
			Offset: binary.BigEndian.Uint32(block[8:12]),
		})
		pos += anchorSize + crcSize
	}
	return x, nil
}
