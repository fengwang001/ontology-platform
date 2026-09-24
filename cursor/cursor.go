// Package cursor 提供不透明游标的编码与解码。
//
// 字节格式：方向(1B) | Key 大端 uint64(8B) | ID 长度(1B) | ID | CRC32(4B)。
// 空字节串是合法游标，表示从头（正向）或从尾（反向）开始。
package cursor

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"

	"ontology/row"
)

// Direction 是游标的翻页方向标记。
type Direction byte

const (
	Forward  Direction = 1
	Backward Direction = 2
)

// 四类可用 errors.Is 区分的错误。
var (
	ErrChecksum  = errors.New("cursor: checksum mismatch")
	ErrTruncated = errors.New("cursor: incomplete fields")
	ErrDirection = errors.New("cursor: invalid direction marker")
	ErrCrossDir  = errors.New("cursor: paging direction mismatch")
)

const (
	headerLen = 1 + 8 + 1 // 方向 + Key + ID 长度
	crcLen    = 4
	minLen    = headerLen + crcLen
)

// Cursor 是解码后的游标：上一页边界行的复合键加方向标记。
// 零值且 empty 为 true 时表示起点游标。
type Cursor struct {
	Dir   Direction
	Key   float64
	ID    string
	empty bool
}

// After 返回指向 r 之后（正向）或之前（反向）位置的游标。
func After(r row.Row, dir Direction) Cursor {
	return Cursor{Dir: dir, Key: r.Key, ID: r.ID}
}

// Empty 报告该游标是否为起点游标（由空字节串解码而来）。
func (c Cursor) Empty() bool { return c.empty }

// RequireDir 校验游标方向；方向不符时报 ErrCrossDir。起点游标任意方向可用。
func (c Cursor) RequireDir(want Direction) error {
	if !c.empty && c.Dir != want {
		return ErrCrossDir
	}
	return nil
}

// Encode 把游标编码为不透明字节串。
func Encode(c Cursor) []byte {
	if c.empty {
		return nil
	}
	buf := make([]byte, headerLen+len(c.ID)+crcLen)
	buf[0] = byte(c.Dir)
	binary.BigEndian.PutUint64(buf[1:9], math.Float64bits(c.Key))
	buf[9] = byte(len(c.ID))
	copy(buf[headerLen:], c.ID)
	binary.BigEndian.PutUint32(buf[len(buf)-crcLen:], crc32.ChecksumIEEE(buf[:len(buf)-crcLen]))
	return buf
}

// Decode 解码游标。空输入返回起点游标；其余输入依次校验方向标记、
// 长度自洽性与 CRC，失败分别返回 ErrDirection / ErrTruncated / ErrChecksum。
func Decode(b []byte) (Cursor, error) {
	if len(b) == 0 {
		return Cursor{empty: true}, nil
	}
	if len(b) < minLen {
		return Cursor{}, ErrTruncated
	}
	dir := Direction(b[0])
	if dir != Forward && dir != Backward {
		return Cursor{}, ErrDirection
	}
	idLen := int(b[9])
	if len(b) != headerLen+idLen+crcLen {
		return Cursor{}, ErrTruncated
	}
	want := binary.BigEndian.Uint32(b[len(b)-crcLen:])
	if crc32.ChecksumIEEE(b[:len(b)-crcLen]) != want {
		return Cursor{}, ErrChecksum
	}
	return Cursor{
		Dir: dir,
		Key: math.Float64frombits(binary.BigEndian.Uint64(b[1:9])),
		ID:  string(b[headerLen : headerLen+idLen]),
	}, nil
}
