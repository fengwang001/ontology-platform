// Package change 定义基表变更记录及其二进制编解码。
package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// Op 为变更操作类型。
type Op uint8

const (
	Insert Op = 1 + iota
	Delete
	Update
)

func (o Op) valid() bool { return o >= Insert && o <= Update }

// 入口校验错误。Group == nil 表示缺失分组；NaN 值一律拒绝。
var (
	ErrBadOp    = errors.New("change: unknown op")
	ErrNoGroup  = errors.New("change: missing group")
	ErrNaNValue = errors.New("change: NaN value")
	ErrBadVer   = errors.New("change: version must be positive")
	ErrDecode   = errors.New("change: short or corrupt payload")
)

// Change 是一条基表变更。
// Insert: 新记录 (ID,Group,Val)；Delete: 删除记录 (ID,Group,Val)；
// Update: 记录 ID 从 (OldGroup,OldVal) 改为 (Group,Val)。
type Change struct {
	Ver      uint64
	Op       Op
	ID       uint64
	Group    *string
	Val      float64
	OldGroup *string
	OldVal   float64
}

func isNaN(f float64) bool { return f != f }

// Validate 做入口校验：合法分组（空串合法，nil 拒绝）、非 NaN、版本为正。
func (c Change) Validate() error {
	if !c.Op.valid() {
		return ErrBadOp
	}
	if c.Ver == 0 {
		return ErrBadVer
	}
	if c.Group == nil {
		return ErrNoGroup
	}
	if isNaN(c.Val) {
		return ErrNaNValue
	}
	if c.Op == Update {
		if c.OldGroup == nil {
			return ErrNoGroup
		}
		if isNaN(c.OldVal) {
			return ErrNaNValue
		}
	}
	return nil
}

// Encode 布局：ver(8) op(1) id(8) group(1+[4+n]) val(8)
// oldGroup(1+[4+n]) oldVal(8)。字符串为 1 字节存在位 + uint32 长度 + UTF8。
func (c Change) Encode() []byte {
	b := make([]byte, 0, 48)
	b = binary.BigEndian.AppendUint64(b, c.Ver)
	b = append(b, byte(c.Op))
	b = binary.BigEndian.AppendUint64(b, c.ID)
	b = putStr(b, c.Group)
	b = binary.BigEndian.AppendUint64(b, math.Float64bits(c.Val))
	b = putStr(b, c.OldGroup)
	b = binary.BigEndian.AppendUint64(b, math.Float64bits(c.OldVal))
	return b
}

func putStr(b []byte, s *string) []byte {
	if s == nil {
		return append(b, 0)
	}
	b = append(b, 1)
	b = binary.BigEndian.AppendUint32(b, uint32(len(*s)))
	return append(b, *s...)
}

// Decode 解析 Encode 的产物，输入过短返回 ErrDecode。
func Decode(p []byte) (Change, error) {
	var c Change
	if len(p) < 18 {
		return c, ErrDecode
	}
	c.Ver = binary.BigEndian.Uint64(p)
	c.Op = Op(p[8])
	c.ID = binary.BigEndian.Uint64(p[9:])
	g, n, err := getStr(p[17:])
	if err != nil {
		return c, err
	}
	if len(p) < 17+n+8 {
		return c, ErrDecode
	}
	c.Group = g
	c.Val = math.Float64frombits(binary.BigEndian.Uint64(p[17+n:]))
	rest := p[25+n:]
	og, m, err := getStr(rest)
	if err != nil || len(rest[m:]) < 8 {
		return c, ErrDecode
	}
	c.OldGroup, c.OldVal = og, math.Float64frombits(binary.BigEndian.Uint64(rest[m:]))
	return c, nil
}

func getStr(p []byte) (*string, int, error) {
	if len(p) < 1 {
		return nil, 0, ErrDecode
	}
	if p[0] == 0 {
		return nil, 1, nil
	}
	if len(p) < 5 {
		return nil, 0, ErrDecode
	}
	n := int(binary.BigEndian.Uint32(p[1:]))
	if len(p) < 5+n {
		return nil, 0, ErrDecode
	}
	s := string(p[5 : 5+n])
	return &s, 5 + n, nil
}
