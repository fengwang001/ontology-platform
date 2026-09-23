// Package cursor 实现不透明分页游标的编码与解码。
//
// 游标记录复合键 (V float64, ID string) 与方向标记，自带 FNV-1a 校验。
// 字节布局：magic(1) | dir(1) | checksum(8, 大端) | v(8, 大端) | id。
package cursor

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"

	"ontology/row"
)

// Direction 标记游标所属的翻页方向。
type Direction byte

const (
	// Forward 表示正向（向更大的复合键）翻页。
	Forward Direction = 1
	// Backward 表示反向（向更小的复合键）翻页。
	Backward Direction = 2
)

const magicByte = 0x6B // 'k'

// 四类可由 errors.Is 区分的错误。
var (
	// ErrIncomplete：字节串长度不足或 magic 不符，字段不完整。
	ErrIncomplete = errors.New("cursor: incomplete or bad magic")
	// ErrDirection：方向标记非法。
	ErrDirection = errors.New("cursor: illegal direction marker")
	// ErrChecksum：校验和不符（游标被篡改）。
	ErrChecksum = errors.New("cursor: checksum mismatch")
	// ErrWrongDirection：游标被跨方向复用。
	ErrWrongDirection = errors.New("cursor: reused with wrong direction")
)

const (
	magicLen   = 1
	dirLen     = 1
	sumLen     = 8
	vLen       = 8
	headerLen  = magicLen + dirLen + sumLen + vLen
	dirOffset  = magicLen
	sumOffset  = magicLen + dirLen
	payloadOff = sumOffset + sumLen
	minLen     = headerLen
)

// Encode 把复合键与方向编码为不透明字节串。
func Encode(k row.Key, dir Direction) []byte {
	buf := make([]byte, headerLen+len(k.ID))
	buf[0] = magicByte
	buf[dirOffset] = byte(dir)
	binary.BigEndian.PutUint64(buf[payloadOff:payloadOff+vLen], math.Float64bits(k.V))
	copy(buf[headerLen:], k.ID)
	sum := fnv.New64a()
	sum.Write(buf[dirOffset : dirOffset+dirLen]) // dir
	sum.Write(buf[payloadOff:])                  // v + id
	binary.BigEndian.PutUint64(buf[sumOffset:payloadOff], sum.Sum64())
	return buf
}

// Decode 解码字节串。want 为调用方期望的方向：
// 空字节串是合法的“从头开始”，返回 ErrEmpty（用 errors.Is 判定）且不应视为故障。
func Decode(raw []byte, want Direction) (row.Key, Direction, error) {
	if len(raw) == 0 {
		return row.Key{}, 0, ErrEmpty
	}
	if len(raw) < minLen || raw[0] != magicByte {
		return row.Key{}, 0, ErrIncomplete
	}
	dir := Direction(raw[dirOffset])
	if dir != Forward && dir != Backward {
		return row.Key{}, dir, ErrDirection
	}
	got := binary.BigEndian.Uint64(raw[sumOffset:payloadOff])
	sum := fnv.New64a()
	sum.Write(raw[dirOffset : dirOffset+dirLen])
	sum.Write(raw[payloadOff:])
	if got != sum.Sum64() {
		return row.Key{}, dir, ErrChecksum
	}
	if dir != want {
		return row.Key{}, dir, ErrWrongDirection
	}
	v := math.Float64frombits(binary.BigEndian.Uint64(raw[payloadOff : payloadOff+vLen]))
	return row.Key{V: v, ID: string(raw[headerLen:])}, dir, nil
}

// ErrEmpty 表示空游标，语义为从头开始，属于合法输入而非故障。
var ErrEmpty = errors.New("cursor: empty (start from beginning)")
