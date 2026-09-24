// Package change 定义变更记录及其自描述二进制编解码。
package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// 操作类型。
const (
	OpInsert byte = 1
	OpDelete byte = 2
	OpUpdate byte = 3
)

// ErrMalformed 表示记录体不是合法的 change 编码。
var ErrMalformed = errors.New("change: malformed record")

// Change 是一条基表变更。Group==nil 表示分组缺失（非法）；空串合法。
// 更新操作以 OldGroup/OldVal 携带被替换前的位置与值。
type Change struct {
	Ver      int64
	Op       byte
	Group    *string
	Val      float64
	ID       string
	OldGroup *string
	OldVal   float64
}

const (
	flagHasGroup    = 1 << 0
	flagHasOldGroup = 1 << 1
)

func putStr(dst []byte, s string) []byte {
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(s)))
	dst = append(dst, lb[:]...)
	return append(dst, s...)
}

// Encode 返回自描述编码（不含长度前缀与 CRC，那是 journal 的职责）。
func (c Change) Encode() []byte {
	b := make([]byte, 0, 64)
	var hdr [18]byte
	hdr[0] = c.Op
	binary.BigEndian.PutUint64(hdr[1:9], uint64(c.Ver))
	var flags byte
	if c.Group != nil {
		flags |= flagHasGroup
	}
	if c.Op == OpUpdate {
		flags |= flagHasOldGroup
	}
	hdr[9] = flags
	binary.BigEndian.PutUint64(hdr[10:18], math.Float64bits(c.Val))
	b = append(b, hdr[:]...)
	if c.Group != nil {
		b = putStr(b, *c.Group)
	}
	b = putStr(b, c.ID)
	if c.Op == OpUpdate {
		if c.OldGroup != nil {
			b = putStr(b, *c.OldGroup)
		} else {
			b = putStr(b, "")
		}
		var ob [8]byte
		binary.BigEndian.PutUint64(ob[:], math.Float64bits(c.OldVal))
		b = append(b, ob[:]...)
	}
	return b
}

func readStr(b []byte, pos int) (string, int, error) {
	if pos+4 > len(b) {
		return "", 0, ErrMalformed
	}
	n := int(binary.BigEndian.Uint32(b[pos : pos+4]))
	pos += 4
	if n < 0 || pos+n > len(b) {
		return "", 0, ErrMalformed
	}
	return string(b[pos : pos+n]), pos + n, nil
}

// Decode 解析 Encode 产生的记录体。
func Decode(b []byte) (Change, error) {
	var c Change
	if len(b) < 18 {
		return c, ErrMalformed
	}
	c.Op = b[0]
	if c.Op != OpInsert && c.Op != OpDelete && c.Op != OpUpdate {
		return c, ErrMalformed
	}
	c.Ver = int64(binary.BigEndian.Uint64(b[1:9]))
	flags := b[9]
	c.Val = math.Float64frombits(binary.BigEndian.Uint64(b[10:18]))
	pos := 18
	if flags&flagHasGroup != 0 {
		g, np, err := readStr(b, pos)
		if err != nil {
			return c, err
		}
		c.Group, pos = &g, np
	}
	id, np, err := readStr(b, pos)
	if err != nil {
		return c, err
	}
	c.ID, pos = id, np
	if c.Op == OpUpdate {
		if flags&flagHasOldGroup == 0 {
			return c, ErrMalformed
		}
		og, np2, err := readStr(b, pos)
		if err != nil {
			return c, err
		}
		c.OldGroup, pos = &og, np2
		if pos+8 > len(b) {
			return c, ErrMalformed
		}
		c.OldVal = math.Float64frombits(binary.BigEndian.Uint64(b[pos : pos+8]))
		pos += 8
	}
	if pos != len(b) {
		return c, ErrMalformed
	}
	return c, nil
}
