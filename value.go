package ontology

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ValueKind 枚举属性值的类型。
type ValueKind int

const (
	KindInt ValueKind = iota
	KindString
	KindStringSet
)

func (k ValueKind) String() string {
	switch k {
	case KindInt:
		return "int"
	case KindString:
		return "string"
	case KindStringSet:
		return "string-set"
	}
	return fmt.Sprintf("ValueKind(%d)", int(k))
}

// Value 是一个带类型的属性值。集合类型的成员始终有序且去重，
// 因此 Value 可以直接按值比较。
type Value struct {
	Kind ValueKind
	Int  int64
	Str  string
	Set  []string
}

func IntValue(v int64) Value  { return Value{Kind: KindInt, Int: v} }
func StrValue(s string) Value { return Value{Kind: KindString, Str: s} }

// SetValue 构造一个规范化（排序、去重）的字符串集合值。
func SetValue(members ...string) Value {
	var sorted []string
	seen := make(map[string]struct{}, len(members))
	for _, m := range members {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		sorted = append(sorted, m)
	}
	sort.Strings(sorted)
	return Value{Kind: KindStringSet, Set: sorted}
}

// ZeroValue 返回某类型的零值，作为实例属性的初始值。
func ZeroValue(k ValueKind) Value {
	return Value{Kind: k}
}

func (v Value) Equal(o Value) bool {
	if v.Kind != o.Kind {
		return false
	}
	switch v.Kind {
	case KindInt:
		return v.Int == o.Int
	case KindString:
		return v.Str == o.Str
	case KindStringSet:
		return slices.Equal(v.Set, o.Set)
	}
	return false
}

func (v Value) Clone() Value {
	if v.Kind == KindStringSet {
		v.Set = append([]string(nil), v.Set...)
	}
	return v
}

func (v Value) String() string {
	switch v.Kind {
	case KindInt:
		return strconv.FormatInt(v.Int, 10)
	case KindString:
		return strconv.Quote(v.Str)
	case KindStringSet:
		return "{" + strings.Join(v.Set, ",") + "}"
	}
	return fmt.Sprintf("<invalid %d>", int(v.Kind))
}
