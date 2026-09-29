// Package dict 提供有序字典编码：去重、码字分配与逆映射。
// 类型参数 K 必须是 comparable；字典本身不处理“空值”概念，
// 是否存在由调用方（空值位图）决定，因此空串与不存在天然可区分。
package dict

import "errors"

var (
	ErrDictLimit = errors.New("dict: cardinality exceeds limit")
	ErrBadCode   = errors.New("dict: code out of range")
)

// Map 是一组值到紧凑码字的双向映射。码字按首次出现顺序分配。
type Map[K comparable] struct {
	values []K
	index  map[K]uint64
}

// Build 去重构建字典。maxCard>0 时，若不同值个数超限返回 ErrDictLimit。
func Build[K comparable](vals []K, maxCard int) (*Map[K], []uint64, error) {
	m := &Map[K]{index: make(map[K]uint64)}
	codes := make([]uint64, len(vals))
	for i, v := range vals {
		code, ok := m.index[v]
		if !ok {
			if maxCard > 0 && len(m.values) >= maxCard {
				return nil, nil, ErrDictLimit
			}
			code = uint64(len(m.values))
			m.index[v] = code
			m.values = append(m.values, v)
		}
		codes[i] = code
	}
	return m, codes, nil
}

// Len 返回字典基数。
func (m *Map[K]) Len() int { return len(m.values) }

// Code 返回某值的码字；第二返回值表示该值是否在字典中（与值为零值无关）。
func (m *Map[K]) Code(v K) (uint64, bool) {
	c, ok := m.index[v]
	return c, ok
}

// Lookup 是 Code 的逆映射：code 越界时返回 ErrBadCode。
func (m *Map[K]) Lookup(code uint64) (K, error) {
	var zero K
	if code >= uint64(len(m.values)) {
		return zero, ErrBadCode
	}
	return m.values[code], nil
}

// Values 返回字典中按码字顺序排列的全部不同值。
func (m *Map[K]) Values() []K {
	out := make([]K, len(m.values))
	copy(out, m.values)
	return out
}

// Encode 返回 vals 的码字序列（字典必须由相同集合构建）。
func (m *Map[K]) Encode(vals []K) ([]uint64, error) {
	out := make([]uint64, len(vals))
	for i, v := range vals {
		c, ok := m.index[v]
		if !ok {
			return nil, ErrBadCode
		}
		out[i] = c
	}
	return out, nil
}

// Decode 把码字序列还原为原值序列。
func (m *Map[K]) Decode(codes []uint64) ([]K, error) {
	out := make([]K, len(codes))
	for i, c := range codes {
		if c >= uint64(len(m.values)) {
			return nil, ErrBadCode
		}
		out[i] = m.values[c]
	}
	return out, nil
}
