// Package change 定义基表变更记录及其二进制编解码。
package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// Op 是变更操作类型。
type Op uint8

const (
	Insert Op = 1
	Delete Op = 2
	Update Op = 3
)

// Change 是一条基表变更。Group 空串合法；HasGroup=false 表示分组键缺失。
// Update 用 NewGroup 非空（HasNewGroup）表示把记录移入新组。
type Change struct {
	Op          Op
	ID          uint64
	Group       string
	HasGroup    bool
	NewGroup    string
	HasNewGroup bool
	Value       float64
	Ver         uint64
}

// ErrMalformed 表示载荷无法解码为一条 Change。
var ErrMalformed = errors.New("change: malformed payload")

func putStr(b []byte, pos int, s string, present bool) int {
	if !present {
		b[pos] = 0
		return pos + 1
	}
	b[pos] = 1
	pos++
	binary.BigEndian.PutUint16(b[pos:], uint16(len(s)))
	pos += 2
	copy(b[pos:], s)
	return pos + len(s)
}

func getStr(b []byte, pos int) (string, bool, int, error) {
	if pos >= len(b) {
		return "", false, pos, ErrMalformed
	}
	switch b[pos] {
	case 0:
		return "", false, pos + 1, nil
	case 1:
		pos++
		if pos+2 > len(b) {
			return "", false, pos, ErrMalformed
		}
		n := int(binary.BigEndian.Uint16(b[pos:]))
		pos += 2
		if pos+n > len(b) {
			return "", false, pos, ErrMalformed
		}
		return string(b[pos : pos+n]), true, pos + n, nil
	default:
		return "", false, pos, ErrMalformed
	}
}

// Encode 将变更序列化为自描述字节载荷。
func (c Change) Encode() []byte {
	b := make([]byte, 1+8+8+8+len(c.Group)+len(c.NewGroup)+6)
	pos := 0
	b[0] = byte(c.Op)
	pos++
	binary.BigEndian.PutUint64(b[pos:], c.ID)
	pos += 8
	binary.BigEndian.PutUint64(b[pos:], math.Float64bits(c.Value))
	pos += 8
	binary.BigEndian.PutUint64(b[pos:], c.Ver)
	pos += 8
	pos = putStr(b, pos, c.Group, c.HasGroup)
	pos = putStr(b, pos, c.NewGroup, c.HasNewGroup)
	return b[:pos]
}

// Decode 从载荷解析变更，失败返回 ErrMalformed。
func Decode(p []byte) (Change, error) {
	var c Change
	if len(p) < 1+8+8+8+2 {
		return c, ErrMalformed
	}
	pos := 0
	c.Op = Op(p[0])
	pos++
	c.ID = binary.BigEndian.Uint64(p[pos:])
	pos += 8
	c.Value = math.Float64frombits(binary.BigEndian.Uint64(p[pos:]))
	pos += 8
	c.Ver = binary.BigEndian.Uint64(p[pos:])
	pos += 8
	var err error
	if c.Group, c.HasGroup, pos, err = getStr(p, pos); err != nil {
		return c, err
	}
	if c.NewGroup, c.HasNewGroup, pos, err = getStr(p, pos); err != nil {
		return c, err
	}
	if pos != len(p) {
		return c, ErrMalformed
	}
	return c, nil
}
