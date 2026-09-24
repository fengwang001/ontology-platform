// Package patch 按生成规则产生补丁，并按应用规则原子地应用补丁。
package patch

import (
	"errors"
	"reflect"
	"sort"

	"ontology/ptr"
)

// Op 是一条补丁条目；HasValue 为假表示缺值（add/replace 缺值即非法操作）。
type Op struct {
	Op, Path string
	Value    any
	HasValue bool
}

// 可判定哨兵错误：非法操作 / 补丁超限。
var (
	ErrInvalidOp  = errors.New("patch: invalid operation")
	ErrTooManyOps = errors.New("patch: too many ops")
)

// Diff 生成从 a 到 b 的补丁；超过 maxOps 条即拒绝。不修改入参。
func Diff(a, b any, maxOps int) ([]Op, error) {
	ops := []Op{}
	diff(a, b, "", &ops)
	if len(ops) > maxOps { return nil, ErrTooManyOps }
	return ops, nil
}

func diff(a, b any, path string, ops *[]Op) {
	if reflect.DeepEqual(a, b) { return }
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if !aok || !bok { // 异型、数组、标量不等：整体 replace
		*ops = append(*ops, Op{Op: "replace", Path: path, Value: b, HasValue: true})
		return
	}
	keys, seen := []string{}, map[string]bool{}
	for k := range am {
		keys, seen[k] = append(keys, k), true
	}
	for k := range bm {
		if !seen[k] { keys = append(keys, k) }
	}
	sort.Strings(keys)
	for _, k := range keys {
		av, ahas := am[k]
		bv, bhas := bm[k]
		p := path + "/" + ptr.Encode(k)
		switch {
		case !ahas:
			*ops = append(*ops, Op{Op: "add", Path: p, Value: bv, HasValue: true})
		case !bhas:
			*ops = append(*ops, Op{Op: "remove", Path: p})
		default:
			diff(av, bv, p, ops)
		}
	}
}

// Apply 原子地应用补丁：任一条失败则整体失败，入参文档不被修改。
func Apply(doc any, ops []Op, maxOps int) (any, error) {
	if len(ops) > maxOps { return nil, ErrTooManyOps }
	work := deepCopy(doc)
	var err error
	for _, op := range ops {
		if work, err = applyOne(work, op); err != nil { return nil, err }
	}
	return work, nil
}

func applyOne(doc any, op Op) (any, error) {
	if (op.Op != "add" && op.Op != "remove" && op.Op != "replace") || (op.Op != "remove" && !op.HasValue) {
		return nil, ErrInvalidOp
	}
	segs, err := ptr.Split(op.Path)
	if err != nil { return nil, err }
	if len(segs) == 0 { // 根：add/replace 替换整个文档；remove 根非法
		if op.Op == "remove" { return nil, ptr.ErrInvalidPath }
		return op.Value, nil
	}
	parent, last, set, err := ptr.Locate(doc, segs)
	if err != nil { return nil, err }
	switch p := parent.(type) {
	case map[string]any:
		_, has := p[last]
		if op.Op != "add" && !has { return nil, ptr.ErrNotFound }
		if op.Op == "remove" { delete(p, last) } else { p[last] = op.Value }
		return doc, nil
	case []any:
		np, err := applyToSlice(p, last, op)
		if err != nil { return nil, err }
		if set == nil { return np, nil }
		set(np)
		return doc, nil
	}
	return nil, ptr.ErrNotFound
}

func applyToSlice(p []any, last string, op Op) ([]any, error) {
	if op.Op == "add" && last == "-" { return append(p, op.Value), nil }
	idx, err := ptr.ParseIndex(last)
	if err != nil { return nil, err }
	if op.Op == "add" {
		if idx > len(p) { return nil, ptr.ErrNotFound }
		np := make([]any, 0, len(p)+1)
		return append(append(append(np, p[:idx]...), op.Value), p[idx:]...), nil
	}
	if idx >= len(p) { return nil, ptr.ErrNotFound }
	if op.Op == "remove" { return append(p[:idx], p[idx+1:]...), nil }
	p[idx] = op.Value
	return p, nil
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t { m[k] = deepCopy(x) }
		return m
	case []any:
		s := make([]any, len(t))
		for i, x := range t { s[i] = deepCopy(x) }
		return s
	}
	return v
}
