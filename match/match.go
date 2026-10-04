package match

import "errors"

// ErrInvalid 表示值在掩码外存在置位。
var ErrInvalid = errors.New("match: value has bits outside mask")

// Field 是一个 32 位三态匹配字段。
type Field struct {
	Value uint32
	Mask  uint32
}

// Match 由两个字段组成。
type Match struct {
	F0 Field
	F1 Field
}

// Pkt 是由两个 32 位字段组成的报文。
type Pkt struct {
	F0 uint32
	F1 uint32
}

// New 构造并校验一个 Match。
func New(f0, f1 Field) (Match, error) {
	m := Match{F0: f0, F1: f1}
	if !m.valid() {
		return Match{}, ErrInvalid
	}
	return m, nil
}

func (m Match) valid() bool {
	return m.F0.Value&^m.F0.Mask == 0 && m.F1.Value&^m.F1.Mask == 0
}

// Valid 判断 Match 是否合法（值在掩码外无置位）。
func (m Match) Valid() bool { return m.valid() }

func fieldHit(f Field, v uint32) bool { return v&f.Mask == f.Value }

func fieldEqual(a, b Field) bool { return a.Value == b.Value && a.Mask == b.Mask }

// 两字段掩码交集中值不一致即无重叠。
func fieldOverlap(a, b Field) bool { return (a.Value^b.Value)&a.Mask&b.Mask == 0 }

// a 包含 b：a 的掩码位是 b 掩码位的子集，且 b 的值在 a 掩码上等于 a 的值。
func fieldContains(a, b Field) bool {
	return a.Mask&b.Mask == a.Mask && b.Value&a.Mask == a.Value
}

// Hit 判断报文是否命中本匹配。
func (m Match) Hit(p Pkt) bool { return fieldHit(m.F0, p.F0) && fieldHit(m.F1, p.F1) }

// Equal 判断两个匹配是否逐字段相同。
func (m Match) Equal(o Match) bool { return fieldEqual(m.F0, o.F0) && fieldEqual(m.F1, o.F1) }

// Overlap 判断两个匹配是否存在共同命中报文。
func (m Match) Overlap(o Match) bool {
	return fieldOverlap(m.F0, o.F0) && fieldOverlap(m.F1, o.F1)
}

// Contains 判断 m 是否包含 o（命中 o 的报文必命中 m）。
func (m Match) Contains(o Match) bool {
	return fieldContains(m.F0, o.F0) && fieldContains(m.F1, o.F1)
}
