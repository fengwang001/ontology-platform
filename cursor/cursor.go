// Package cursor 提供不透明游标的编码与解码。
//
// 游标锚定复合排序键 (Key, ID) 并携带方向标记。编码后的字节串自带
// CRC32 校验与方向码，任何单比特篡改都会被拒绝并分类。
package cursor

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
)

// 错误分类，均可用 errors.Is 判定。
var (
	ErrTruncated         = errors.New("cursor: 字段不完整")
	ErrChecksum          = errors.New("cursor: 校验失败")
	ErrBadDirection      = errors.New("cursor: 方向标记非法")
	ErrDirectionMismatch = errors.New("cursor: 跨方向复用")
)

// Dir 是翻页方向。
type Dir byte

const (
	None     Dir = iota // 零值：从头开始
	Forward             // 正向（取更大的复合键）
	Backward            // 反向（取更小的复合键）
)

const (
	magic    = 0xC7
	version  = 0x01
	dirFwd   = 0xA5 // 与 dirBwd 互为按位取反，汉明距离 8
	dirBwd   = 0x5A
	headLen  = 12 // magic+version+dir+key(8)+idLen
	crcLen   = 4
	minLen   = headLen + crcLen
	maxIDLen = 255
)

// Cursor 锚定数据集中一个复合键位置。零值表示从头开始。
type Cursor struct {
	Dir Dir
	Key float64
	ID  string
}

// IsZero 报告游标是否为零值（从头开始）。
func (c Cursor) IsZero() bool {
	return c.Dir == None
}

// Encode 把游标编码为不透明字节串。零游标编码为空串。
func Encode(c Cursor) []byte {
	if c.IsZero() {
		return nil
	}
	id := c.ID
	if len(id) > maxIDLen {
		id = id[:maxIDLen]
	}
	buf := make([]byte, headLen+len(id)+crcLen)
	buf[0] = magic
	buf[1] = version
	if c.Dir == Backward {
		buf[2] = dirBwd
	} else {
		buf[2] = dirFwd
	}
	binary.BigEndian.PutUint64(buf[3:11], math.Float64bits(c.Key))
	buf[11] = byte(len(id))
	copy(buf[headLen:], id)
	binary.BigEndian.PutUint32(buf[headLen+len(id):], crc32.ChecksumIEEE(buf[:headLen+len(id)]))
	return buf
}

// Decode 解码游标。空字节串解码为零游标（从头开始，合法）。
// 任何篡改都会被拒绝并按 ErrTruncated / ErrBadDirection / ErrChecksum 分类。
func Decode(b []byte) (Cursor, error) {
	if len(b) == 0 {
		return Cursor{}, nil
	}
	if len(b) < minLen {
		return Cursor{}, ErrTruncated
	}
	var dir Dir
	switch b[2] {
	case dirFwd:
		dir = Forward
	case dirBwd:
		dir = Backward
	default:
		return Cursor{}, ErrBadDirection
	}
	idLen := int(b[11])
	if len(b) != headLen+idLen+crcLen {
		return Cursor{}, ErrTruncated
	}
	if b[0] != magic || b[1] != version {
		return Cursor{}, ErrChecksum
	}
	body := b[:len(b)-crcLen]
	if crc32.ChecksumIEEE(body) != binary.BigEndian.Uint32(b[len(b)-crcLen:]) {
		return Cursor{}, ErrChecksum
	}
	return Cursor{
		Dir: dir,
		Key: math.Float64frombits(binary.BigEndian.Uint64(b[3:11])),
		ID:  string(b[headLen : headLen+idLen]),
	}, nil
}
