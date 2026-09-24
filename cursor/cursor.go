// Package cursor 实现不透明分页游标的编码与解码。
//
// 编码布局（大端）：
//
//	ver(1) | dir(1) | score(8) | idLen(2) | id(idLen) | crc32(4)
//
// 空字节串是合法的"无游标"输入，表示从数据集起点开始。
package cursor

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"

	"ontology/row"
)

var (
	// ErrCursorMalformed：字段缺失、长度不符或版本不支持。
	ErrCursorMalformed = errors.New("cursor: malformed")
	// ErrBadDirection：方向标记字节不是已定义的方向。
	ErrBadDirection = errors.New("cursor: invalid direction marker")
	// ErrChecksum：校验和不匹配，游标内容被篡改。
	ErrChecksum = errors.New("cursor: checksum mismatch")
	// ErrWrongDirection：游标在相反方向的翻页中被使用。
	ErrWrongDirection = errors.New("cursor: direction does not match scan")
)

const (
	version byte = 1

	dirForward  byte = 0x0F
	dirBackward byte = 0xF0

	headerLen = 2 // ver + dir
	scoreLen  = 8
	idLenLen  = 2
	crcLen    = 4
	minLen    = headerLen + scoreLen + idLenLen + crcLen
)

// Direction 是翻页方向。
type Direction int

const (
	Forward Direction = iota
	Backward
)

// Cursor 是解码后的游标：复合键位置 + 所属方向。
type Cursor struct {
	Key row.Key
	Dir Direction
}

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// Encode 把复合键与方向编码成不透明字节串。
func Encode(k row.Key, dir Direction) []byte {
	id := []byte(k.ID)
	buf := make([]byte, minLen+len(id))
	buf[0] = version
	buf[1] = dirByte(dir)
	binary.BigEndian.PutUint64(buf[headerLen:headerLen+scoreLen], floatBits(k.Score))
	binary.BigEndian.PutUint16(buf[headerLen+scoreLen:headerLen+scoreLen+idLenLen], uint16(len(id)))
	copy(buf[headerLen+scoreLen+idLenLen:], id)
	sum := crc32.Checksum(buf[:len(buf)-crcLen], crcTable)
	binary.BigEndian.PutUint32(buf[len(buf)-crcLen:], sum)
	return buf
}

// Decode 解码非空游标，并用 want 做跨方向校验。
// nil/空切片表示"从起点开始"，返回 (零值, true, nil)。
func Decode(data []byte, want Direction) (Cursor, bool, error) {
	if len(data) == 0 {
		return Cursor{}, true, nil
	}
	if len(data) < minLen || data[0] != version {
		return Cursor{}, false, ErrCursorMalformed
	}
	idEnd := len(data) - crcLen
	idStart := headerLen + scoreLen + idLenLen
	idLen := int(binary.BigEndian.Uint16(data[headerLen+scoreLen : idStart]))
	if idLen != idEnd-idStart {
		return Cursor{}, false, ErrCursorMalformed
	}
	dir, err := fromDirByte(data[1])
	if err != nil {
		return Cursor{}, false, err
	}
	stored := binary.BigEndian.Uint32(data[idEnd:])
	if crc32.Checksum(data[:idEnd], crcTable) != stored {
		return Cursor{}, false, ErrChecksum
	}
	if dir != want {
		return Cursor{}, false, ErrWrongDirection
	}
	score := bitsFloat(binary.BigEndian.Uint64(data[headerLen : headerLen+scoreLen]))
	return Cursor{Key: row.Row{Score: score, ID: string(data[idStart:idEnd])}, Dir: dir}, false, nil
}

func dirByte(d Direction) byte {
	if d == Backward {
		return dirBackward
	}
	return dirForward
}

func fromDirByte(b byte) (Direction, error) {
	switch b {
	case dirForward:
		return Forward, nil
	case dirBackward:
		return Backward, nil
	default:
		return Forward, ErrBadDirection
	}
}

func floatBits(f float64) uint64 { return math.Float64bits(f) }

func bitsFloat(b uint64) float64 { return math.Float64frombits(b) }
