// Package change 定义基表变更记录及其二进制编解码。
package change

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Op 是变更操作类型。
type Op byte

const (
	Insert Op = iota + 1
	Delete
	Update
)

func (o Op) String() string {
	switch o {
	case Insert:
		return "insert"
	case Delete:
		return "delete"
	case Update:
		return "update"
	}
	return "unknown"
}

// Change 是一条基表变更。Version 由上游单调分配。
// HasGroup 区分「分组键为空串（合法）」与「缺失（拒绝）」。
type Change struct {
	Version  uint64
	Op       Op
	HasGroup bool
	Group    string
	ID       string
	Value    float64
}

// Equal 逐字段比较两条变更（用于重复投递的幂等判定）。
func (c Change) Equal(o Change) bool {
	return c.Version == o.Version && c.Op == o.Op && c.HasGroup == o.HasGroup &&
		c.Group == o.Group && c.ID == o.ID &&
		math.Float64bits(c.Value) == math.Float64bits(o.Value)
}

// ErrShort 表示记录体不完整。
var ErrShort = errors.New("change: record body too short")

// 记录体布局：version(8) op(1) flags(1) groupLen(2) group idLen(2) id value(8)。
const fixedLen = 8 + 1 + 1 + 2 + 2 + 8

const flagHasGroup = 1

// Encode 把变更编码为记录体（不含长度前缀与 CRC，由 journal 负责）。
func (c Change) Encode() []byte {
	buf := make([]byte, fixedLen+len(c.Group)+len(c.ID))
	binary.LittleEndian.PutUint64(buf[0:8], c.Version)
	buf[8] = byte(c.Op)
	if c.HasGroup {
		buf[9] = flagHasGroup
	}
	binary.LittleEndian.PutUint16(buf[10:12], uint16(len(c.Group)))
	off := 12 + copy(buf[12:], c.Group)
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(len(c.ID)))
	off += 2 + copy(buf[off+2:], c.ID)
	binary.LittleEndian.PutUint64(buf[off:off+8], math.Float64bits(c.Value))
	return buf
}

// Decode 从记录体解码变更。
func Decode(buf []byte) (Change, error) {
	var c Change
	if len(buf) < 12 {
		return c, ErrShort
	}
	c.Version = binary.LittleEndian.Uint64(buf[0:8])
	c.Op = Op(buf[8])
	if c.Op < Insert || c.Op > Update {
		return c, fmt.Errorf("change: unknown op %d", buf[8])
	}
	c.HasGroup = buf[9]&flagHasGroup != 0
	glen := int(binary.LittleEndian.Uint16(buf[10:12]))
	if len(buf) < 12+glen+2 {
		return c, ErrShort
	}
	c.Group = string(buf[12 : 12+glen])
	off := 12 + glen
	ilen := int(binary.LittleEndian.Uint16(buf[off : off+2]))
	if len(buf) < off+2+ilen+8 {
		return c, ErrShort
	}
	c.ID = string(buf[off+2 : off+2+ilen])
	off += 2 + ilen
	c.Value = math.Float64frombits(binary.LittleEndian.Uint64(buf[off : off+8]))
	return c, nil
}
