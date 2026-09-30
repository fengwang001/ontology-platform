package patch

import "fmt"

// Apply 把补丁原子地应用到 doc 上，返回新文档。
// 先整体校验再逐条执行；任一失败则整体失败，doc 与 p 均不变。
func Apply(doc any, p Patch) (any, error) {
	if len(p) > MaxOps {
		return nil, fmt.Errorf("%w: %d ops exceeds %d", ErrPatchTooLarge, len(p), MaxOps)
	}
	// 预校验：操作类型合法、路径可解析，失败不留痕。
	segList := make([][]string, len(p))
	for i, op := range p {
		switch op.Op {
		case OpAdd, OpRemove, OpReplace:
		default:
			return nil, fmt.Errorf("%w: op %d has unknown kind %q", ErrInvalidOp, i, op.Op)
		}
		segs, err := parsePath(op.Path)
		if err != nil {
			return nil, fmt.Errorf("op %d: %w", i, err)
		}
		segList[i] = segs
	}
	// 在深拷贝上执行，任何一步失败都不影响调用方文档。
	root := deepCopy(doc)
	var err error
	for i, op := range p {
		root, err = applyAt(root, segList[i], op)
		if err != nil {
			return nil, fmt.Errorf("op %d (%s %s): %w", i, op.Op, op.Path, err)
		}
	}
	return root, nil
}

// applyAt 在 node 上按 segs 定位并执行操作，返回更新后的节点。
// 递归下降时中间段必须存在；数组增删会返回新切片并由父层回填。
func applyAt(node any, segs []string, op Op) (any, error) {
	if len(segs) == 0 {
		// 根路径：仅允许整体替换或新增。
		switch op.Op {
		case OpAdd, OpReplace:
			return deepCopy(op.Value), nil
		default:
			return nil, fmt.Errorf("%w: cannot remove root", ErrInvalidOp)
		}
	}
	if len(segs) == 1 {
		return applyLeaf(node, segs[0], op)
	}
	seg := segs[0]
	switch n := node.(type) {
	case map[string]any:
		child, ok := n[seg]
		if !ok {
			return nil, fmt.Errorf("%w: object key %q", ErrPathNotFound, seg)
		}
		newChild, err := applyAt(child, segs[1:], op)
		if err != nil {
			return nil, err
		}
		n[seg] = newChild
		return n, nil
	case []any:
		idx, err := parseIndex(seg)
		if err != nil {
			return nil, err
		}
		if idx >= len(n) {
			return nil, fmt.Errorf("%w: array index %d out of bounds (len %d)", ErrPathNotFound, idx, len(n))
		}
		newChild, err := applyAt(n[idx], segs[1:], op)
		if err != nil {
			return nil, err
		}
		n[idx] = newChild
		return n, nil
	default:
		return nil, fmt.Errorf("%w: cannot descend into %T at %q", ErrTypeMismatch, node, seg)
	}
}

// applyLeaf 在父容器 node 上对末段 seg 执行操作，返回更新后的容器。
func applyLeaf(node any, seg string, op Op) (any, error) {
	switch n := node.(type) {
	case map[string]any:
		_, exists := n[seg]
		switch op.Op {
		case OpAdd:
			if exists {
				return nil, fmt.Errorf("%w: add target key %q already exists", ErrInvalidOp, seg)
			}
			n[seg] = deepCopy(op.Value)
		case OpReplace:
			if !exists {
				return nil, fmt.Errorf("%w: replace target key %q", ErrPathNotFound, seg)
			}
			n[seg] = deepCopy(op.Value)
		case OpRemove:
			if !exists {
				return nil, fmt.Errorf("%w: remove target key %q", ErrPathNotFound, seg)
			}
			delete(n, seg)
		}
		return n, nil
	case []any:
		idx, err := parseIndex(seg)
		if err != nil {
			return nil, err
		}
		switch op.Op {
		case OpAdd:
			if idx > len(n) {
				return nil, fmt.Errorf("%w: add index %d out of bounds (len %d)", ErrPathNotFound, idx, len(n))
			}
			out := make([]any, 0, len(n)+1)
			out = append(out, n[:idx]...)
			out = append(out, deepCopy(op.Value))
			out = append(out, n[idx:]...)
			return out, nil
		case OpReplace:
			if idx >= len(n) {
				return nil, fmt.Errorf("%w: replace index %d out of bounds (len %d)", ErrPathNotFound, idx, len(n))
			}
			n[idx] = deepCopy(op.Value)
			return n, nil
		case OpRemove:
			if idx >= len(n) {
				return nil, fmt.Errorf("%w: remove index %d out of bounds (len %d)", ErrPathNotFound, idx, len(n))
			}
			out := make([]any, 0, len(n)-1)
			out = append(out, n[:idx]...)
			out = append(out, n[idx+1:]...)
			return out, nil
		}
		return n, nil
	default:
		return nil, fmt.Errorf("%w: parent of %q is %T, not a container", ErrTypeMismatch, seg, node)
	}
}
