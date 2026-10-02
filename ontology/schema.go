package ontology

import "sort"

type pathNode struct {
	name string
	rep  Rep
	leaf bool
}

type leafInfo struct {
	name   string
	nodes  []pathNode
	maxRep int
	maxDef int
}

func schemaError(path string) error {
	return &FieldError{Kind: ErrSchema, Path: path}
}

// validateSchema 按深度优先、分组先校验再进入子字段的次序校验模式。
func validateSchema(fields []Field) ([]leafInfo, error) {
	var leaves []leafInfo
	var walk func(fs []Field, nodes []pathNode, reps, defs int) error
	walk = func(fs []Field, nodes []pathNode, reps, defs int) error {
		seen := make(map[string]bool, len(fs))
		for i := range fs {
			f := &fs[i]
			path := joinNodes(append(append([]pathNode(nil), nodes...), pathNode{name: f.Name}))
			if f.Name == "" {
				return schemaError(path)
			}
			if seen[f.Name] {
				return schemaError(path)
			}
			seen[f.Name] = true

			if f.Rep < Required || f.Rep > Repeated {
				return schemaError(path)
			}
			if len(nodes)+1 > 6 {
				return schemaError(path)
			}

			ns := append(append([]pathNode(nil), nodes...), pathNode{name: f.Name, rep: f.Rep, leaf: len(f.Children) == 0})
			childReps := reps
			childDefs := defs
			if f.Rep == Repeated {
				childReps++
			}
			if f.Rep != Required {
				childDefs++
			}

			if len(f.Children) == 0 {
				if len(leaves) >= 16 {
					return schemaError(path)
				}
				leaves = append(leaves, leafInfo{
					name:   path,
					nodes:  ns,
					maxRep: childReps,
					maxDef: childDefs,
				})
				continue
			}
			if err := walk(f.Children, ns, childReps, childDefs); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(fields, nil, 0, 0); err != nil {
		return nil, err
	}
	if len(leaves) == 0 {
		return nil, schemaError("")
	}
	return leaves, nil
}

func joinNodes(nodes []pathNode) string {
	var b []byte
	for i, n := range nodes {
		if i > 0 {
			b = append(b, '.')
		}
		b = append(b, n.name...)
	}
	return string(b)
}

// buildIndexTree 构建带叶子下标范围的索引树，leafLo/leafHi 是 leaves 中的半开区间。
func buildIndexTree(fields []Field) []schemaNode {
	idx := 0
	var build func(fs []Field, parent string, reps, defs int) []schemaNode
	build = func(fs []Field, parent string, reps, defs int) []schemaNode {
		nodes := make([]schemaNode, 0, len(fs))
		for i := range fs {
			f := &fs[i]
			path := joinPath(parent, f.Name)
			childReps, childDefs := reps, defs
			if f.Rep == Repeated {
				childReps++
			}
			if f.Rep != Required {
				childDefs++
			}
			n := schemaNode{field: f, path: path, leafLo: idx, repLevel: childReps, defLevel: childDefs}
			if len(f.Children) == 0 {
				idx++
			} else {
				n.children = build(f.Children, path, childReps, childDefs)
			}
			n.leafHi = idx
			nodes = append(nodes, n)
		}
		return nodes
	}
	return build(fields, "", 0, 0)
}

// collectLeafPaths 返回每个叶子（DFS 次序）自根到叶的节点指针路径。
func collectLeafPaths(tree []schemaNode) [][]*schemaNode {
	var out [][]*schemaNode
	var walk func(nodes []schemaNode, acc []*schemaNode)
	walk = func(nodes []schemaNode, acc []*schemaNode) {
		for i := range nodes {
			n := &nodes[i]
			next := append(append([]*schemaNode(nil), acc...), n)
			if len(n.field.Children) == 0 {
				out = append(out, next)
			} else {
				walk(n.children, next)
			}
		}
	}
	walk(tree, nil)
	return out
}

func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}

// sortedKeys 返回按字节序排序的键。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func typeError(path string) error {
	return &FieldError{Kind: ErrType, Path: path}
}

func missingError(path string) error {
	return &FieldError{Kind: ErrMissingRequired, Path: path}
}

func unknownError(path string) error {
	return &FieldError{Kind: ErrUnknownField, Path: path}
}
