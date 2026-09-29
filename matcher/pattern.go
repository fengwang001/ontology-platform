// Package matcher 在 graph.Graph 上进行子图模式匹配。
//
// 模式（Pattern）由带类型约束的节点变量（NodeVar）与带类型约束的
// 有向边模式（EdgePat）组成。匹配器使用带回溯的约束搜索：
// 变量按最小候选优先的顺序逐个绑定，绑定后立即做局部一致性剪枝；
// 同一变量始终绑定同一对象；最终嵌入通过模式自同构规范化去重，
// 同构匹配（同一组对象映射）只返回一次，且结果与边插入顺序无关。
package matcher

import (
	"fmt"
)

// maxPatternVars 限制模式变量数量。同构规范化需要在自同构候选类内
// 枚举排列，该上限保证枚举成本有界（典型本体模式远小于此值）。
const maxPatternVars = 12

// NodeVar 是模式中的节点变量。
type NodeVar struct {
	// Name 是变量名，同一模式内唯一；同一变量始终绑定同一对象。
	Name string
	// Type 要求对象的类型；空串表示不限类型。
	Type string
	// Constraints 是该变量绑定对象必须满足的属性约束（合取）。
	Constraints []Constraint
}

// EdgePat 是模式中的有向边：Source --Type--> Target。
type EdgePat struct {
	Type   string
	Source string
	Target string
}

// Pattern 是待匹配的子图模式。
type Pattern struct {
	Nodes []NodeVar
	Edges []EdgePat
}

// Validate 在搜索前做静态校验。非法模式返回带有可区分原因的错误，
// 此类查询不会返回任何（部分）结果。
func (p *Pattern) Validate() error {
	if p == nil || len(p.Nodes) == 0 {
		return ErrEmptyPattern
	}
	if len(p.Nodes) > maxPatternVars {
		return fmt.Errorf("%w: pattern has %d variables, limit is %d",
			ErrPatternDefinition, len(p.Nodes), maxPatternVars)
	}
	defined := make(map[string]bool, len(p.Nodes))
	incident := make(map[string]bool, len(p.Nodes))
	for _, node := range p.Nodes {
		if node.Name == "" {
			return fmt.Errorf("%w: node variable name must not be empty", ErrPatternDefinition)
		}
		if defined[node.Name] {
			return fmt.Errorf("%w: duplicate node variable %q", ErrPatternDefinition, node.Name)
		}
		defined[node.Name] = true
		for _, constraint := range node.Constraints {
			if err := constraint.validate(); err != nil {
				return err
			}
		}
	}
	for i, edge := range p.Edges {
		if edge.Type == "" {
			return fmt.Errorf("%w: edge #%d has empty type", ErrPatternDefinition, i)
		}
		if !defined[edge.Source] {
			return fmt.Errorf("%w: edge #%d source %q is not a defined variable", ErrPatternDefinition, i, edge.Source)
		}
		if !defined[edge.Target] {
			return fmt.Errorf("%w: edge #%d target %q is not a defined variable", ErrPatternDefinition, i, edge.Target)
		}
		incident[edge.Source] = true
		incident[edge.Target] = true
	}
	for _, node := range p.Nodes {
		if !incident[node.Name] && node.Type == "" && len(node.Constraints) == 0 {
			return fmt.Errorf("%w: variable %q has no edge, type or constraint", ErrUnconstrainedVariable, node.Name)
		}
	}
	return nil
}
