// Package change 定义基表变更记录及其自描述二进制编解码。
package change

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
)

// Op 是变更操作类型。
type Op uint8

const (
	Insert Op = 1 + iota
	Delete
	Update
)

// Change 是一条变更。Group 为空串合法；HasGroup=false 表示分组缺失。
type Change struct {
	Op       Op
	Version  uint64
	ID       string
	Group    string
	HasGroup bool
	Value    float64
	// OldGroup/OldValue 仅 Update 使用。
	OldGroup string
	OldValue float64
}

var (
	// ErrShort 表示截断：数据不足以解码。
	ErrShort = errors.New("change: short payload")
	// ErrMalformed 表示载荷内容非法（未知操作、越界长度）。
	ErrMalformed = errors.New("change: malformed payload")
)

const (
	flagGroup    = 1 << 0
	flagOldGroup = 1 << 1
)

func putStr(b *bytes.Buffer, s string) {
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(s)))
	b.Write(lb[:])
	b.WriteString(s)
}

func getStr(b []byte, pos int) (string, int, error) {
	if pos+4 > len(b) {
		return "", 0, ErrShort
	}
	n := int(binary.BigEndian.Uint32(b[pos:]))
	pos += 4
	if n < 0 || pos+n > len(b) {
		return "", 0, ErrMalformed
	}
	return string(b[pos : pos+n]), pos + n, nil
}

// Encode 返回自描述载荷：op|flags|version|id|group?|value|oldgroup?|oldvalue。
func Encode(c Change) []byte {
	var b bytes.Buffer
	b.Grow(40 + len(c.ID) + len(c.Group) + len(c.OldGroup))
	var flags byte
	if c.HasGroup {
		flags |= flagGroup
	}
	if c.Op == Update {
		flags |= flagOldGroup
	}
	b.WriteByte(byte(c.Op))
	b.WriteByte(flags)
	var vb [8]byte
	binary.BigEndian.PutUint64(vb[:], c.Version)
	b.Write(vb[:])
	putStr(&b, c.ID)
	if c.HasGroup {
		putStr(&b, c.Group)
	}
	binary.BigEndian.PutUint64(vb[:], math.Float64bits(c.Value))
	b.Write(vb[:])
	if c.Op == Update {
		putStr(&b, c.OldGroup)
		binary.BigEndian.PutUint64(vb[:], math.Float64bits(c.OldValue))
		b.Write(vb[:])
	}
	return b.Bytes()
}

// Decode 解析 Encode 的产物；截断返回 ErrShort，内容非法返回 ErrMalformed。
func Decode(p []byte) (Change, error) {
	var c Change
	if len(p) < 2+8+4 {
		return c, ErrShort
	}
	c.Op = Op(p[0])
	if c.Op < Insert || c.Op > Update {
		return c, ErrMalformed
	}
	flags := p[1]
	pos := 2
	c.Version = binary.BigEndian.Uint64(p[pos:])
	pos += 8
	var err error
	if c.ID, pos, err = getStr(p, pos); err != nil {
		return c, err
	}
	if flags&flagGroup != 0 {
		c.HasGroup = true
		if c.Group, pos, err = getStr(p, pos); err != nil {
			return c, err
		}
	}
	if pos+8 > len(p) {
		return c, ErrShort
	}
	c.Value = math.Float64frombits(binary.BigEndian.Uint64(p[pos:]))
	pos += 8
	if c.Op == Update {
		if flags&flagOldGroup == 0 {
			return c, ErrMalformed
		}
		if c.OldGroup, pos, err = getStr(p, pos); err != nil {
			return c, err
		}
		if pos+8 > len(p) {
			return c, ErrShort
		}
		c.OldValue = math.Float64frombits(binary.BigEndian.Uint64(p[pos:]))
		pos += 8
	}
	if pos != len(p) {
		return c, ErrMalformed
	}
	return c, nil
}
