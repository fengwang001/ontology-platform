package patch

import (
	"context"
	"fmt"
	"reflect"
)

// Apply 按顺序把补丁原子应用到 doc，返回应用后的新文档。
//
// 任何一条操作失败都会整体拒绝：返回对应类别的错误，且调用方传入的 doc
// 与补丁都保持不变（内部先在深拷贝上执行，成功后才返回结果）。
func Apply(doc any, p Patch) (any, error) {
	if len(p.Ops) > DefaultMaxOps {
		return nil, fmt.Errorf("%w: %d ops exceed limit %d",
			ErrPatchTooLarge, len(p.Ops), DefaultMaxOps)
	}

	working, err := deepCopy(doc)
	if err != nil {
		return nil, err
	}
	for i, op := range p.Ops {
		if err := applyOp(&working, op); err != nil {
			return nil, fmt.Errorf("op %d (%s %q): %w", i, op.Type, op.Path, err)
		}
	}
	return working, nil
}

func applyOp(root *any, op Op) error {
	segments, err := DecodePath(op.Path)
	if err != nil {
		return err
	}

	if len(segments) == 0 {
		// 根路径只允许整体替换；根上新增/删除没有意义。
		switch op.Type {
		case "replace":
			copied, err := deepCopy(op.Value)
			if err != nil {
				return err
			}
			*root = copied
			return nil
		case "add":
			return fmt.Errorf("%w: cannot add at root", ErrInvalidOperation)
		case "remove":
			return fmt.Errorf("%w: cannot remove root", ErrInvalidOperation)
		default:
			return fmt.Errorf("%w: unknown op type %q", ErrInvalidOperation, op.Type)
		}
	}

	parentTokens, token := parentAndToken(segments)
	parent, err := locate(root, parentTokens)
	if err != nil {
		return err
	}

	if op.Type == "add" || op.Type == "replace" {
		copied, err := deepCopy(op.Value)
		if err != nil {
			return err
		}
		op.Value = copied
	}

	switch container := parent.(type) {
	case map[string]any:
		return applyMapOp(container, token, op)
	case []any:
		return applySliceOp(container, token, op, root, parentTokens)
	default:
		// 父级是标量：无法继续定位目标路径，归类为路径不存在。
		return fmt.Errorf("%w: parent of %q is a scalar and cannot contain %q",
			ErrPathNotFound, op.Path, token)
	}
}

func applyMapOp(container map[string]any, token string, op Op) error {
	_, exists := container[token]
	switch op.Type {
	case "add":
		container[token] = op.Value
		return nil
	case "replace":
		if !exists {
			return fmt.Errorf("%w: key %q", ErrPathNotFound, token)
		}
		container[token] = op.Value
		return nil
	case "remove":
		if !exists {
			return fmt.Errorf("%w: key %q", ErrPathNotFound, token)
		}
		delete(container, token)
		return nil
	default:
		return fmt.Errorf("%w: unknown op type %q", ErrInvalidOperation, op.Type)
	}
}

func applySliceOp(container []any, token string, op Op,
	root *any, parentTokens []string) error {
	idx, err := parseArrayIndex(token)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	switch op.Type {
	case "remove":
		if idx >= len(container) {
			return fmt.Errorf("%w: array index %d", ErrPathNotFound, idx)
		}
		writeBack(root, parentTokens, append(container[:idx:idx], container[idx+1:]...))
		return nil
	case "replace":
		if idx >= len(container) {
			return fmt.Errorf("%w: array index %d", ErrPathNotFound, idx)
		}
		container[idx] = op.Value
		return nil
	case "add":
		if idx > len(container) {
			return fmt.Errorf("%w: array index %d", ErrPathNotFound, idx)
		}
		grown := make([]any, 0, len(container)+1)
		grown = append(grown, container[:idx]...)
		grown = append(grown, op.Value)
		grown = append(grown, container[idx:]...)
		writeBack(root, parentTokens, grown)
		return nil
	default:
		return fmt.Errorf("%w: unknown op type %q", ErrInvalidOperation, op.Type)
	}
}

// locate 沿中间段下钻；任何一段缺失或下钻对象不是容器都视为路径不存在/非法。
func locate(root *any, tokens []string) (any, error) {
	current := *root
	for _, token := range tokens {
		switch node := current.(type) {
		case map[string]any:
			value, ok := node[token]
			if !ok {
				return nil, fmt.Errorf("%w: segment %q", ErrPathNotFound, token)
			}
			current = value
		case []any:
			idx, err := parseArrayIndex(token)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPath, err)
			}
			if idx >= len(node) {
				return nil, fmt.Errorf("%w: segment %d", ErrPathNotFound, idx)
			}
			current = node[idx]
		default:
			return nil, fmt.Errorf("%w: segment %q traverses a scalar",
				ErrPathNotFound, token)
		}
	}
	return current, nil
}

// writeBack 把经过 add/remove 得到的新切片写回其父容器（切片是值语义，
// append 可能更换底层数组）。
func writeBack(root *any, parentTokens []string, value []any) {
	if len(parentTokens) == 0 {
		*root = value
		return
	}
	upperTokens, token := parentAndToken(parentTokens)
	parent, err := locate(root, upperTokens)
	if err != nil {
		// writeBack 的路径已在 locate 阶段验证过，理论上不可达。
		panic(err)
	}
	switch node := parent.(type) {
	case map[string]any:
		node[token] = value
	case []any:
		idx, err := parseArrayIndex(token)
		if err != nil {
			panic(err)
		}
		node[idx] = value
	}
}

// deepCopy 对 JSON 兼容文档做深拷贝；同时校验所有值类型都是可表示的。
func deepCopy(v any) (any, error) {
	switch value := v.(type) {
	case nil:
		return nil, nil
	case string:
		return value, nil
	case bool:
		return value, nil
	case float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return value, nil
	case map[string]any:
		copied := make(map[string]any, len(value))
		for key, item := range value {
			child, err := deepCopy(item)
			if err != nil {
				return nil, err
			}
			copied[key] = child
		}
		return copied, nil
	case []any:
		copied := make([]any, len(value))
		for i, item := range value {
			child, err := deepCopy(item)
			if err != nil {
				return nil, err
			}
			copied[i] = child
		}
		return copied, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedValue, reflect.TypeOf(v))
	}
}

// SelfCheck 生成 source->target 的补丁并应用，校验往返深度相等且输入不被修改。
// 它无包级可变状态，可被多个执行体并发调用。
func SelfCheck(ctx context.Context, source, target any, opts ...GenerateOption) (Patch, error) {
	if err := ctx.Err(); err != nil {
		return Patch{}, err
	}

	srcBefore, err := deepCopy(source)
	if err != nil {
		return Patch{}, err
	}
	p, err := Generate(source, target, opts...)
	if err != nil {
		return Patch{}, err
	}
	got, err := Apply(source, p)
	if err != nil {
		return Patch{}, err
	}
	if !reflect.DeepEqual(got, target) {
		return p, fmt.Errorf("patch: self-check round-trip mismatch")
	}
	if !reflect.DeepEqual(source, srcBefore) {
		return p, fmt.Errorf("patch: self-check detected source mutation")
	}
	return p, nil
}
