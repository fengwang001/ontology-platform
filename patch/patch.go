// Package patch 生成并原子地应用 JSON Patch（RFC 6902 子集：add/remove/replace）。
package patch

import (
	"errors"
	"maps"
	"reflect"
	"slices"

	"ontology/ptr"
)

var ErrInvalidOp = errors.New("invalid operation")    // 非法操作：op 非三者之一或 add/replace 缺值
var ErrTooManyOps = errors.New("too many operations") // 补丁条数超过 maxOps

// Op 是一条补丁条目；HasValue 为假表示缺值（remove），add null 时 HasValue 真、Value 为 nil。
type Op struct {
	Op       string
	Path     string
	Value    any
	HasValue bool
}

// Diff 生成 a→b 的补丁，不修改输入；条数超过 maxOps 即拒绝。
func Diff(a, b any, maxOps int) ([]Op, error) {
	var ops []Op
	diff(a, b, "", &ops)
	if len(ops) > maxOps {
		return nil, ErrTooManyOps
	}
	return ops, nil
}

func diff(a, b any, path string, ops *[]Op) {
	if reflect.DeepEqual(a, b) {
		return
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if !aok || !bok { // 类型不同、数组、标量不等 → 整体 replace
		*ops = append(*ops, Op{"replace", path, ptr.Clone(b), true})
		return
	}
	keys := slices.Collect(maps.Keys(am))
	keys = slices.AppendSeq(keys, maps.Keys(bm))
	slices.Sort(keys)
	keys = slices.Compact(keys)
	for _, k := range keys {
		av, inA := am[k]
		bv, inB := bm[k]
		p := path + "/" + ptr.Encode(k)
		switch {
		case !inA:
			*ops = append(*ops, Op{"add", p, ptr.Clone(bv), true})
		case !inB:
			*ops = append(*ops, Op{Op: "remove", Path: p})
		default:
			diff(av, bv, p, ops)
		}
	}
}

// Apply 在 doc 的深拷贝上逐条应用，任一失败整体失败；不修改 doc 与 ops。
func Apply(doc any, ops []Op, maxOps int) (any, error) {
	if len(ops) > maxOps {
		return nil, ErrTooManyOps
	}
	work := ptr.Clone(doc)
	for _, op := range ops {
		valid := op.Op == "add" || op.Op == "remove" || op.Op == "replace"
		if !valid || (op.Op != "remove" && !op.HasValue) {
			return nil, ErrInvalidOp
		}
		segs, err := ptr.Split(op.Path)
		if err != nil {
			return nil, err
		}
		if work, err = applyAt(work, segs, op); err != nil {
			return nil, err
		}
	}
	return work, nil
}

func applyAt(node any, segs []string, op Op) (any, error) {
	if len(segs) == 0 { // 根：add/replace 替换整个文档；remove 根是非法路径
		if op.Op == "remove" {
			return nil, ptr.ErrInvalidPath
		}
		return ptr.Clone(op.Value), nil
	}
	if len(segs) > 1 { // 中间段必须存在
		child, err := ptr.Step(node, segs[0])
		if err != nil {
			return nil, err
		}
		nc, err := applyAt(child, segs[1:], op)
		if err != nil {
			return nil, err
		}
		if m, ok := node.(map[string]any); ok {
			m[segs[0]] = nc
		} else {
			i, _ := ptr.ParseIndex(segs[0])
			node.([]any)[i] = nc
		}
		return node, nil
	}
	last := segs[0]
	switch c := node.(type) {
	case map[string]any:
		if op.Op != "add" { // replace/remove 目标必须存在
			if _, err := ptr.Step(node, last); err != nil {
				return nil, err
			}
		}
		if op.Op == "remove" {
			delete(c, last)
			return c, nil
		}
		c[last] = ptr.Clone(op.Value)
		return c, nil
	case []any:
		if op.Op == "add" && last == "-" {
			return append(c, ptr.Clone(op.Value)), nil
		}
		if op.Op != "add" { // replace/remove 下标必须合法且存在
			if _, err := ptr.Step(c, last); err != nil {
				return nil, err
			}
		}
		i, err := ptr.ParseIndex(last)
		if err != nil {
			return nil, err
		}
		if op.Op == "add" {
			if i > len(c) {
				return nil, ptr.ErrNotFound
			}
			return slices.Insert(c, i, ptr.Clone(op.Value)), nil
		}
		if op.Op == "remove" {
			return slices.Delete(c, i, i+1), nil
		}
		c[i] = ptr.Clone(op.Value)
		return c, nil
	}
	return nil, ptr.ErrNotFound
}
