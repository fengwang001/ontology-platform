// Package change 定义基表变更记录及其二进制编解码。
package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// Op 是变更操作类型。
type Op byte

const (
	OpInsert Op = iota
	OpDelete
	OpUpdate
)

// ErrMalformed 表示编码字节流无法解析。
var ErrMalformed = errors.New("change: malformed encoding")

// Change 是一条基表变更。Group 为 nil 表示分组键缺失（由 view 拒绝并计数）；
// Group 指向空串是合法分组键。Delete 只需 ID，分组与值由 view 的成员索引反查。
type Change struct {
	Version uint64
	Op      Op
	ID      uint64
	Group   *string
	Value   float64
}

// Canonicalize 把 -0 规范化为 +0，使正负零按相等处理。
func Canonicalize(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

// Encode 布局: op(1) | version(8) | id(8) | hasGroup(1) | [glen(2) group] | value(8)。
func (c Change) Encode() []byte {
	var glen int
	if c.Group != nil {
		glen = len(*c.Group)
	}
	buf := make([]byte, 0, 26+glen)
	buf = append(buf, byte(c.Op))
	buf = binary.LittleEndian.AppendUint64(buf, c.Version)
	buf = binary.LittleEndian.AppendUint64(buf, c.ID)
	if c.Group != nil {
		buf = append(buf, 1)
		buf = binary.LittleEndian.AppendUint16(buf, uint16(glen))
		buf = append(buf, *c.Group...)
	} else {
		buf = append(buf, 0)
	}
	return binary.LittleEndian.AppendUint64(buf, math.Float64bits(c.Value))
}

// Decode 是 Encode 的逆过程，任何截断或越界都返回 ErrMalformed。
func Decode(b []byte) (Change, error) {
	var c Change
	if len(b) < 18 {
		return c, ErrMalformed
	}
	c.Op = Op(b[0])
	if c.Op > OpUpdate {
		return c, ErrMalformed
	}
	c.Version = binary.LittleEndian.Uint64(b[1:9])
	c.ID = binary.LittleEndian.Uint64(b[9:17])
	rest := b[17:]
	if rest[0] == 1 {
		if len(rest) < 3 {
			return c, ErrMalformed
		}
		glen := int(binary.LittleEndian.Uint16(rest[1:3]))
		if len(rest) < 3+glen+8 {
			return c, ErrMalformed
		}
		g := string(rest[3 : 3+glen])
		c.Group = &g
		rest = rest[3+glen:]
	} else {
		rest = rest[1:]
	}
	if len(rest) != 8 {
		return c, ErrMalformed
	}
	c.Value = math.Float64frombits(binary.LittleEndian.Uint64(rest))
	return c, nil
}
