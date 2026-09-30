// Package patch 提供文档补丁的生成、应用与自检。
//
// 文档为 JSON 风格的数据：map[string]any、[]any、string、float64、
// bool、nil。补丁由一组有序操作构成，应用后可把源文档还原为目标文档。
package patch

import (
	"errors"
	"fmt"
	"sort"
)

// 错误类别：互不相同的哨兵错误，调用方可用 errors.Is 区分。
var (
	// ErrInvalidPath 路径编码非法（转义错误、数组下标非法等）。
	ErrInvalidPath = errors.New("patch: invalid path")
	// ErrPathNotFound 路径的中间段或目标不存在。
	ErrPathNotFound = errors.New("patch: path not found")
	// ErrInvalidOp 操作类型非法或操作字段缺失。
	ErrInvalidOp = errors.New("patch: invalid operation")
	// ErrPatchTooLarge 补丁超出数量、深度或长度限制。
	ErrPatchTooLarge = errors.New("patch: patch exceeds limit")
	// ErrTypeMismatch 路径中间段类型与文档结构不符。
	ErrTypeMismatch = errors.New("patch: type mismatch")
)

// 补丁规模限制。
const (
	MaxOps       = 10000 // 单份补丁最大操作数
	MaxPathDepth = 64    // 单条路径最大段数
	MaxPathLen   = 4096  // 单条路径编码后的最大字节数
)

// OpKind 操作类型。
type OpKind string

const (
	OpAdd     OpKind = "add"     // 新增键或向数组插入元素
	OpRemove  OpKind = "remove"  // 删除键或数组元素
	OpReplace OpKind = "replace" // 整体替换现有值
)

// Op 单条补丁操作。Path 为编码后的路径，Value 仅 add/replace 使用。
type Op struct {
	Op    OpKind
	Path  string
	Value any
}

// Patch 有序操作序列，应用时逐条执行，任一失败则整份失败。
type Patch []Op

// Generate 由源文档 oldDoc 与目标文档 newDoc 生成最小有序补丁。
// 输入文档不会被修改，补丁中的值为深拷贝。
func Generate(oldDoc, newDoc any) (Patch, error) {
	var p Patch
	diffInto(&p, "", oldDoc, newDoc)
	if len(p) > MaxOps {
		return nil, fmt.Errorf("%w: %d ops exceeds %d", ErrPatchTooLarge, len(p), MaxOps)
	}
	return p, nil
}

// diffInto 从 path 指向的子树递归生成操作，追加到 p。
// 两值相等不产生操作；两侧都是对象则对键的并集按字节序逐键处理；
// 其余情况（类型不同、数组、标量不等）整体替换。
func diffInto(p *Patch, path string, oldV, newV any) {
	if deepEqual(oldV, newV) {
		return
	}
	oldObj, oldOK := oldV.(map[string]any)
	newObj, newOK := newV.(map[string]any)
	if oldOK && newOK {
		keys := unionKeys(oldObj, newObj)
		for _, k := range keys {
			child := path + "/" + escapeSegment(k)
			oldVal, inOld := oldObj[k]
			newVal, inNew := newObj[k]
			switch {
			case !inNew:
				*p = append(*p, Op{Op: OpRemove, Path: child})
			case !inOld:
				*p = append(*p, Op{Op: OpAdd, Path: child, Value: deepCopy(newVal)})
			default:
				diffInto(p, child, oldVal, newVal)
			}
		}
		return
	}
	*p = append(*p, Op{Op: OpReplace, Path: path, Value: deepCopy(newV)})
}

// unionKeys 返回两映射键的并集，按字节序排序。
func unionKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := seen[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// Verify 自检：生成补丁并应用，校验往返相等且深度一致。
func Verify(oldDoc, newDoc any) error {
	p, err := Generate(oldDoc, newDoc)
	if err != nil {
		return fmt.Errorf("verify: generate: %w", err)
	}
	got, err := Apply(oldDoc, p)
	if err != nil {
		return fmt.Errorf("verify: apply: %w", err)
	}
	if !deepEqual(got, newDoc) {
		return fmt.Errorf("verify: round-trip mismatch")
	}
	if depth(got) != depth(newDoc) {
		return fmt.Errorf("verify: depth mismatch: got %d, want %d", depth(got), depth(newDoc))
	}
	return nil
}
