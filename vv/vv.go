// Package vv 提供版本向量类型与偏序比较。
package vv

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"sort"
)

var (
	ErrUnknownReplica = errors.New("vv: unknown replica id")
	ErrOverflow       = errors.New("vv: counter overflow")
	ErrCounterRollback = errors.New("vv: counter rollback detected")
	ErrHeader         = errors.New("vv: incomplete header")
	ErrEntry          = errors.New("vv: incomplete component")
	ErrCRC            = errors.New("vv: crc mismatch")
)

// Relation 是两个版本向量之间的四种偏序关系之一。
type Relation int

const (
	Equal Relation = iota
	Less
	Greater
	Concurrent
)

func (r Relation) String() string {
	switch r {
	case Equal:
		return "equal"
	case Less:
		return "less"
	case Greater:
		return "greater"
	default:
		return "concurrent"
	}
}

// Vector 是 副本ID → 计数器；缺失分量语义为 0。
type Vector map[string]uint64

// Copy 返回深拷贝。
func (v Vector) Copy() Vector {
	c := make(Vector, len(v))
	for k, x := range v {
		c[k] = x
	}
	return c
}

// Compare 返回 a 相对 b 的偏序关系。
func Compare(a, b Vector) Relation {
	r, _ := CompareCounted(a, b)
	return r
}

// CompareCounted 同时返回分量读取次数；次数等于两向量 key 并集大小，
// 因而不超过 2*并集大小，且不遍历任何历史副本。
func CompareCounted(a, b Vector) (Relation, int) {
	keys := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	lt, gt := false, false
	for k := range keys {
		x, y := a[k], b[k] // 缺失分量读取为 0
		switch {
		case x < y:
			lt = true
		case x > y:
			gt = true
		}
	}
	switch {
	case lt && gt:
		return Concurrent, len(keys)
	case lt:
		return Less, len(keys)
	case gt:
		return Greater, len(keys)
	default:
		return Equal, len(keys)
	}
}

// Join 返回逐分量最大值（水位合并）。
func Join(a, b Vector) Vector {
	m := a.Copy()
	for k, x := range b {
		if x > m[k] {
			m[k] = x
		}
	}
	return m
}

// Inc 返回 v 在副本 r 上 +1 后的新向量；上溢时返回 ErrOverflow。
func Inc(v Vector, r string) (Vector, error) {
	if v[r] == math.MaxUint64 {
		return nil, ErrOverflow
	}
	c := v.Copy()
	c[r]++
	return c, nil
}

// CheckKnown 要求向量中每个 ID 都已登记（known 为空表示不校验）。
func (v Vector) CheckKnown(known map[string]struct{}) error {
	if len(known) == 0 {
		return nil
	}
	for k := range v {
		if _, ok := known[k]; !ok {
			return ErrUnknownReplica
		}
	}
	return nil
}

// 编码布局：magic(2)=="VV" | count uint16 | [klen uint16 | key | val uint64]... | crc32

// Encode 将向量编码为确定性字节序列（key 字典序）。
func (v Vector) Encode() []byte {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf := make([]byte, 0, 4+len(keys)*12+4)
	buf = append(buf, 'V', 'V')
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(keys)))
	for _, k := range keys {
		buf = binary.BigEndian.AppendUint16(buf, uint16(len(k)))
		buf = append(buf, k...)
		buf = binary.BigEndian.AppendUint64(buf, v[k])
	}
	sum := crc32.ChecksumIEEE(buf)
	return binary.BigEndian.AppendUint32(buf, sum)
}

// Decode 解析 Encode 的输出，并按截断位置分类错误。
func Decode(data []byte) (Vector, error) {
	if len(data) < 4 || data[0] != 'V' || data[1] != 'V' {
		return nil, ErrHeader
	}
	n := int(binary.BigEndian.Uint16(data[2:4]))
	p := 4
	v := make(Vector, n)
	for i := 0; i < n; i++ {
		if len(data)-p < 2 {
			return nil, ErrEntry
		}
		kl := int(binary.BigEndian.Uint16(data[p : p+2]))
		p += 2
		if len(data)-p < kl+8 {
			return nil, ErrEntry
		}
		k := string(data[p : p+kl])
		p += kl
		v[k] = binary.BigEndian.Uint64(data[p : p+8])
		p += 8
	}
	if len(data)-p < 4 {
		return nil, ErrCRC
	}
	want := binary.BigEndian.Uint32(data[p : p+4])
	if crc32.ChecksumIEEE(data[:p]) != want {
		return nil, ErrCRC
	}
	return v, nil
}
