// Package dict 实现有序值集合上的字典编码：去重、码字分配与逆映射。
// 用泛型同时服务 int64 与 string 两种列类型。
package dict

import (
	"errors"
	"sort"
)

// ErrTooLarge 字典基数超过调用方给定的上限。此错误属于"可降级"信号：
// 调用方可改用位打包（或原样存储）而不是拒绝写入。
var ErrTooLarge = errors.New("dict: cardinality exceeds limit")

// Dict 是一张不可变（构建后）字典：码字 0..n-1 对应排序后的值。
type Dict[T comparable] struct {
	values []T
	index  map[T]uint64
}

// Build 对 vals 去重并按排序键分配紧凑码字。maxCard>0 时，不同值个数
// 超过上限即返回 ErrTooLarge，且不返回任何可用字典（无半成品状态）。
func Build[T comparable](vals []T, maxCard int, less func(a, b T) bool) (*Dict[T], error) {
	uniq := make(map[T]struct{}, len(vals))
	for _, v := range vals {
		uniq[v] = struct{}{}
	}
	if maxCard > 0 && len(uniq) > maxCard {
		return nil, ErrTooLarge
	}
	values := make([]T, 0, len(uniq))
	for v := range uniq {
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool { return less(values[i], values[j]) })
	index := make(map[T]uint64, len(values))
	for i, v := range values {
		index[v] = uint64(i)
	}
	return &Dict[T]{values: values, index: index}, nil
}

// Len 返回不同值个数。
func (d *Dict[T]) Len() int { return len(d.values) }

// Values 返回字典值的有序副本（码字顺序）。
func (d *Dict[T]) Values() []T {
	out := make([]T, len(d.values))
	copy(out, d.values)
	return out
}

// Code 返回某值的码字；值不存在时 ok=false。
func (d *Dict[T]) Code(v T) (uint64, bool) {
	c, ok := d.index[v]
	return c, ok
}

// Value 是码字的逆映射；码字越界时 ok=false。
func (d *Dict[T]) Value(code uint64) (T, bool) {
	var zero T
	if int(code) >= len(d.values) {
		return zero, false
	}
	return d.values[code], true
}

// Encode 把一组值映射为码字流；任一值不在字典中即失败。
func (d *Dict[T]) Encode(vals []T) ([]uint64, error) {
	codes := make([]uint64, len(vals))
	for i, v := range vals {
		c, ok := d.index[v]
		if !ok {
			return nil, errors.New("dict: value not in dictionary")
		}
		codes[i] = c
	}
	return codes, nil
}

// Decode 把码字流逆映射回值；任一码字越界即失败。
func (d *Dict[T]) Decode(codes []uint64) ([]T, error) {
	out := make([]T, len(codes))
	for i, c := range codes {
		if int(c) >= len(d.values) {
			return nil, errors.New("dict: code out of range")
		}
		out[i] = d.values[c]
	}
	return out, nil
}
