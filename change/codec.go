package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// payload 布局（小端）：
//
//	version u64 | op u8 | id u64 | flags u8 | groupLen u16 |
//	group 字节（可选）| valueBits u64
//
// flags bit0 = 分组键存在。固定部分 28 字节。
const (
	fixedLen    = 28
	maxGroupLen = 1 << 16
	flagGroup   = 1
)

// ErrCorrupt 表示字节流无法解码为合法变更。
var ErrCorrupt = errors.New("change: corrupt encoding")

// EncodedLen 返回 c 编码后的字节数。
func (c Change) EncodedLen() int {
	n := fixedLen
	if c.Group != nil {
		n += len(*c.Group)
	}
	return n
}

// Encode 将 c 编码到 buf（长度须 >= EncodedLen），返回写入字节数。
func (c Change) Encode(buf []byte) int {
	binary.LittleEndian.PutUint64(buf[0:8], c.Version)
	buf[8] = byte(c.Op)
	binary.LittleEndian.PutUint64(buf[9:17], c.ID)
	var flags byte
	n := 0
	if c.Group != nil {
		flags = flagGroup
		n = len(*c.Group)
	}
	buf[17] = flags
	binary.LittleEndian.PutUint16(buf[18:20], uint16(n))
	off := 20
	if c.Group != nil {
		copy(buf[off:], *c.Group)
		off += n
	}
	binary.LittleEndian.PutUint64(buf[off:off+8], math.Float64bits(c.Value))
	return off + 8
}

// Decode 从 buf 解码一条变更，返回变更与消耗的字节数。
func Decode(buf []byte) (Change, int, error) {
	var c Change
	if len(buf) < fixedLen {
		return c, 0, ErrCorrupt
	}
	c.Version = binary.LittleEndian.Uint64(buf[0:8])
	c.Op = Op(buf[8])
	c.ID = binary.LittleEndian.Uint64(buf[9:17])
	flags := buf[17]
	n := int(binary.LittleEndian.Uint16(buf[18:20]))
	off := 20
	if flags&flagGroup != 0 {
		if len(buf) < off+n+8 {
			return c, 0, ErrCorrupt
		}
		g := string(buf[off : off+n])
		c.Group = &g
		off += n
	} else if n != 0 {
		return c, 0, ErrCorrupt
	}
	if len(buf) < off+8 {
		return c, 0, ErrCorrupt
	}
	c.Value = math.Float64frombits(binary.LittleEndian.Uint64(buf[off : off+8]))
	return c, off + 8, nil
}
