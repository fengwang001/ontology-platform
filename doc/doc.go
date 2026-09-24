// Package doc 定义记录集合（键 -> 字段映射）及其规范化编码。
package doc

import (
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"sort"
)

// Record 是一条记录：字段名 -> 值。字段不存在与值为空串可区分。
type Record map[string]any

// Set 是记录集合：键 -> 记录。空串键、空串字段名均合法。
type Set map[string]Record

// TypeName 返回值的可读类型名，用于冲突报告。
func TypeName(v any) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", v)
}

// Equal 判断两条记录是否完全相同（字段集合与值逐一相等）。
func Equal(a, b Record) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !reflect.DeepEqual(av, bv) {
			return false
		}
	}
	return true
}

// ValueEqual 判断两个值是否相等；双方都不存在（has 均为 false）也算相等。
func ValueEqual(av, bv any, hasA, hasB bool) bool {
	if hasA != hasB {
		return false
	}
	if !hasA {
		return true
	}
	return reflect.DeepEqual(av, bv)
}

// Keys 返回集合的键，按字典序排列。
func Keys(s Set) []string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// FieldKeys 返回记录的字段名，按字典序排列。
func FieldKeys(r Record) []string {
	names := make([]string, 0, len(r))
	for k := range r {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func appendStr(b []byte, s string) []byte {
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(s)))
	b = append(b, n[:]...)
	return append(b, s...)
}

// AppendValue 以类型标签 + 长度前缀编码一个值。
func AppendValue(b []byte, v any) []byte {
	switch t := v.(type) {
	case nil:
		return append(b, 'n')
	case string:
		return appendStr(append(b, 's'), t)
	case bool:
		if t {
			return append(b, 't')
		}
		return append(b, 'f')
	case int:
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(int64(t)))
		return append(append(b, 'i'), n[:]...)
	case int64:
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(t))
		return append(append(b, 'I'), n[:]...)
	case float64:
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], math.Float64bits(t))
		return append(append(b, 'F'), n[:]...)
	default:
		return appendStr(append(b, '?'), fmt.Sprintf("%v", v))
	}
}

// Encode 输出集合的规范化编码：键字典序、键内字段名字典序，
// 与 map 构造顺序、迭代顺序完全无关。
func Encode(s Set) []byte {
	var b []byte
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(s)))
	b = append(b, n[:]...)
	for _, k := range Keys(s) {
		b = appendStr(b, k)
		r := s[k]
		binary.LittleEndian.PutUint32(n[:], uint32(len(r)))
		b = append(b, n[:]...)
		for _, f := range FieldKeys(r) {
			b = appendStr(b, f)
			b = AppendValue(b, r[f])
		}
	}
	return b
}
