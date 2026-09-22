// Package dict 提供 int64 值的字典编码：去重、码字分配与逆映射。
//
// 码字按首次出现的顺序从 0 递增分配。字典可配置最大基数，
// 超过上限时 Add 返回 ErrTooManyValues，调用方应回退到其他编码。
package dict

import "errors"

// ErrTooManyValues 表示字典基数超过配置上限。
var ErrTooManyValues = errors.New("dict: cardinality exceeds limit")

// ErrBadCode 表示解码时遇到字典中不存在的码字。
var ErrBadCode = errors.New("dict: code out of range")

// Dict 保存值到码字的双向映射。零值不可用，请用 New 构造。
type Dict struct {
	index map[int64]uint32
	vals  []int64
	max   int
}

// New 创建空字典。maxCard 为最大基数，<=0 表示不限制。
func New(maxCard int) *Dict {
	return &Dict{index: make(map[int64]uint32), max: maxCard}
}

// Add 返回 v 的码字，不存在时分配新码字。
// 新码字会使基数超限时不做任何修改，返回 ErrTooManyValues。
func (d *Dict) Add(v int64) (uint32, error) {
	if c, ok := d.index[v]; ok {
		return c, nil
	}
	if d.max > 0 && len(d.vals) >= d.max {
		return 0, ErrTooManyValues
	}
	c := uint32(len(d.vals))
	d.index[v] = c
	d.vals = append(d.vals, v)
	return c, nil
}

// Contains 报告 v 是否已在字典中。
func (d *Dict) Contains(v int64) bool {
	_, ok := d.index[v]
	return ok
}

// Cardinality 返回当前基数（不同值的个数）。
func (d *Dict) Cardinality() int {
	return len(d.vals)
}

// Value 返回码字 c 对应的值，c 越界时返回 ErrBadCode。
func (d *Dict) Value(c uint32) (int64, error) {
	if int(c) >= len(d.vals) {
		return 0, ErrBadCode
	}
	return d.vals[c], nil
}

// Values 返回字典值的副本，下标即码字。
func (d *Dict) Values() []int64 {
	out := make([]int64, len(d.vals))
	copy(out, d.vals)
	return out
}

// Freeze 把字典序列化为字节流（值个数 + 定长 8 字节小端值序列）。
func (d *Dict) Freeze() []byte {
	out := make([]byte, 4+8*len(d.vals))
	put32(out, uint32(len(d.vals)))
	for i, v := range d.vals {
		put64(out[4+i*8:], uint64(v))
	}
	return out
}

// Thaw 从字节流还原字典。数据不足或长度不自洽时返回错误。
func Thaw(data []byte, maxCard int) (*Dict, int, error) {
	if len(data) < 4 {
		return nil, 0, errors.New("dict: truncated header")
	}
	n := int(get32(data))
	if maxCard > 0 && n > maxCard {
		return nil, 0, ErrTooManyValues
	}
	need := 4 + 8*n
	if len(data) < need {
		return nil, 0, errors.New("dict: truncated values")
	}
	d := New(maxCard)
	for i := 0; i < n; i++ {
		v := int64(get64(data[4+i*8:]))
		if _, err := d.Add(v); err != nil {
			return nil, 0, err
		}
	}
	return d, need, nil
}

func put32(dst []byte, v uint32) {
	for i := 0; i < 4; i++ {
		dst[i] = byte(v >> (8 * i))
	}
}

func get32(src []byte) uint32 {
	return uint32(src[0]) | uint32(src[1])<<8 | uint32(src[2])<<16 | uint32(src[3])<<24
}

func put64(dst []byte, v uint64) {
	for i := 0; i < 8; i++ {
		dst[i] = byte(v >> (8 * i))
	}
}

func get64(src []byte) uint64 {
	var v uint64
	for i := 7; i >= 0; i-- {
		v = v<<8 | uint64(src[i])
	}
	return v
}
