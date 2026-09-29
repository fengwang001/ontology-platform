// Package patch 提供文档（JSON 兼容对象）两个版本之间差异补丁的生成、原子应用与自检能力。
package patch

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
)

// 补丁在生成与应用两侧共同遵守的操作数量上限，防止超大规模输入耗尽资源。
const DefaultMaxOps = 10000

// 可区分的错误类别，调用方可用 errors.Is 精确判断。
var (
	// ErrInvalidPath 路径编码非法（空段、非法转义、数组下标格式错误）。
	ErrInvalidPath = errors.New("patch: invalid path")
	// ErrPathNotFound 路径引用的中间段或目标不存在。
	ErrPathNotFound = errors.New("patch: path not found")
	// ErrInvalidOperation 操作类型非法或作用于不支持的容器类型。
	ErrInvalidOperation = errors.New("patch: invalid operation")
	// ErrPatchTooLarge 补丁操作条数超过允许上限。
	ErrPatchTooLarge = errors.New("patch: patch too large")
	// ErrUnsupportedValue 文档包含无法在补丁中表示的值类型。
	ErrUnsupportedValue = errors.New("patch: unsupported value")
)

// Op 是一条有序补丁操作。
type Op struct {
	Type  string
	Path  string
	Value any
}

// Patch 是一组按序应用的操作集合。
type Patch struct {
	Ops []Op
}

// Len 返回补丁条目数。
func (p Patch) Len() int { return len(p.Ops) }

// Decision 描述生成过程中对某一对取值的判定依据，供自检与日志使用。
type Decision struct {
	Path       string
	Kind       string
	SourceType string
	TargetType string
	Op         Op
}

// GenerateOption 配置生成行为。
type GenerateOption func(*generateConfig)

type generateConfig struct {
	maxOps     int
	onDecision func(Decision)
}

// Generate 计算 source 到 target 的最小有序差异补丁。
func Generate(source, target any, opts ...GenerateOption) (Patch, error) {
	cfg := generateConfig{maxOps: DefaultMaxOps}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxOps < 0 {
		return Patch{}, fmt.Errorf("%w: max ops must be non-negative", ErrPatchTooLarge)
	}
	g := &generator{cfg: cfg}
	if err := g.walk("", source, target); err != nil {
		return Patch{}, err
	}
	return Patch{Ops: g.ops}, nil
}

type generator struct {
	cfg generateConfig
	ops []Op
}

// walk 对同一路径上的两个值生成补丁，encoded 为当前路径的编码形式。
func (g *generator) walk(encoded string, source, target any) error {
	srcObj, srcIsObj := source.(map[string]any)
	dstObj, dstIsObj := target.(map[string]any)

	switch {
	case reflect.DeepEqual(source, target):
		// 两值相等：不产生任何操作，这是补丁最小化的基础。
		g.log(encoded, "equal", source, target, Op{})
		return nil
	case srcIsObj && dstIsObj:
		g.log(encoded, "object", source, target, Op{})
		return g.walkObject(encoded, srcObj, dstObj)
	default:
		// 类型不同、两侧或任一侧为数组、标量不等：整体替换，不深入数组内部。
		return g.emit(encoded, "replace", OpReplace(encoded, target), source, target)
	}
}

func (g *generator) walkObject(encoded string, source, target map[string]any) error {
	keySet := make(map[string]struct{}, len(source)+len(target))
	for key := range source {
		keySet[key] = struct{}{}
	}
	for key := range target {
		keySet[key] = struct{}{}
	}
	keys := make([]string, 0, len(keySet))
	for key := range keySet {
		keys = append(keys, key)
	}
	// 键的并集按字节序逐键处理，保证补丁条目确定且有序、可复现。
	sort.Strings(keys)

	for _, key := range keys {
		if key == "" {
			return fmt.Errorf("%w: empty object key cannot be encoded", ErrInvalidPath)
		}
		childPath := encoded + "/" + escapeKey(key)
		srcVal, srcOk := source[key]
		dstVal, dstOk := target[key]
		switch {
		case srcOk && !dstOk:
			if err := g.emit(childPath, "remove", OpRemove(childPath), srcVal, nil); err != nil {
				return err
			}
		case !srcOk && dstOk:
			if err := g.emit(childPath, "add", OpAdd(childPath, dstVal), nil, dstVal); err != nil {
				return err
			}
		default:
			if err := g.walk(childPath, srcVal, dstVal); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *generator) emit(path, kind string, op Op, source, target any) error {
	if len(g.ops) >= g.cfg.maxOps {
		return fmt.Errorf("%w: limit %d reached", ErrPatchTooLarge, g.cfg.maxOps)
	}
	g.ops = append(g.ops, op)
	g.log(path, kind, source, target, op)
	return nil
}

func (g *generator) log(path, kind string, source, target any, op Op) {
	if g.cfg.onDecision == nil {
		return
	}
	g.cfg.onDecision(Decision{
		Path:       path,
		Kind:       kind,
		SourceType: typeName(source),
		TargetType: typeName(target),
		Op:         op,
	})
}

// Op 构造辅助，便于应用侧与测试以命名方式识别操作。
func OpAdd(path string, value any) Op { return Op{Type: "add", Path: path, Value: value} }
func OpRemove(path string) Op         { return Op{Type: "remove", Path: path} }
func OpReplace(path string, value any) Op {
	return Op{Type: "replace", Path: path, Value: value}
}

func escapeKey(key string) string {
	escaped, err := EncodePath([]string{key})
	if err != nil {
		// 空键无法在路径中表示：生成时直接拒绝而不是产出不可应用的补丁。
		return ""
	}
	return escaped[1:]
}

func typeName(v any) string {
	if v == nil {
		return "null"
	}
	return reflect.TypeOf(v).String()
}

// WithMaxOps 设置补丁允许的最大操作条数。
func WithMaxOps(n int) GenerateOption {
	return func(c *generateConfig) { c.maxOps = n }
}

// WithDecisionLogger 在生成过程中回调每一步的输入、判定依据与产生的补丁条目。
func WithDecisionLogger(fn func(Decision)) GenerateOption {
	return func(c *generateConfig) { c.onDecision = fn }
}
