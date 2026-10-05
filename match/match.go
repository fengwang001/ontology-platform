// Package match 提供两字段三态（值/掩码）匹配的命中、重叠与包含判定。
package match

import "errors"

// ErrInvalid 表示值在掩码之外有置位，属参数非法。
var ErrInvalid = errors.New("match: value has bits set outside mask")

// Field 是一个 32 位的（值，掩码）对；值在掩码外不得有置位。
type Field struct {
	Value uint32
	Mask  uint32
}

// valid 报告值是否完全落在掩码之内。
func (f Field) valid() bool {
	return f.Value&^f.Mask == 0
}

// Match 由 2 个字段组成的三态匹配。
type Match struct {
	F [2]Field
}

// Packet 是报文的两个 32 位字段。
type Packet [2]uint32

// New 构造一个 Match；任一字段的值在掩码外有置位时返回 ErrInvalid。
func New(v0, m0, v1, m1 uint32) (Match, error) {
	m := Match{F: [2]Field{{Value: v0, Mask: m0}, {Value: v1, Mask: m1}}}
	if !m.Valid() {
		return Match{}, ErrInvalid
	}
	return m, nil
}

// Must 同 New，参数非法时 panic；便于测试与常量构造。
func Must(v0, m0, v1, m1 uint32) Match {
	m, err := New(v0, m0, v1, m1)
	if err != nil {
		panic(err)
	}
	return m
}

// Valid 报告两个字段的值是否都落在各自掩码之内。
func (m Match) Valid() bool {
	return m.F[0].valid() && m.F[1].valid()
}

// Hit 报告报文是否命中：每个字段上 报文&掩码 == 值。
func (m Match) Hit(p Packet) bool {
	for i := 0; i < 2; i++ {
		if p[i]&m.F[i].Mask != m.F[i].Value {
			return false
		}
	}
	return true
}

// Overlaps 报告两个匹配是否重叠，即存在同时命中二者的报文：
// 每个字段上 (v1^v2)&m1&m2 == 0。相同匹配也算重叠。
func (m Match) Overlaps(o Match) bool {
	for i := 0; i < 2; i++ {
		a, b := m.F[i], o.F[i]
		if (a.Value^b.Value)&a.Mask&b.Mask != 0 {
			return false
		}
	}
	return true
}

// Equal 报告两个匹配是否逐字段值与掩码全等。
func (m Match) Equal(o Match) bool {
	return m.F == o.F
}

// Contains 报告 o 是否被 m 包含（o 的命中集是 m 命中集的子集）：
// 每个字段上 m 的掩码是 o 掩码的子集，且 o 的值&m 的掩码 == m 的值。
func (m Match) Contains(o Match) bool {
	for i := 0; i < 2; i++ {
		a, b := m.F[i], o.F[i]
		if a.Mask&^b.Mask != 0 {
			return false
		}
		if b.Value&a.Mask != a.Value {
			return false
		}
	}
	return true
}
