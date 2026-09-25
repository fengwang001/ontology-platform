// Package props 负责解析对象类型的有效属性集
// （自身属性 + 祖先继承属性，含协变覆盖与菱形三态合并）。
package props

import (
	"fmt"

	"ontology/typetree"
)

// Resolver 基于一个类型树解析有效属性。
type Resolver struct {
	tree *typetree.Tree
}

func NewResolver(tree *typetree.Tree) *Resolver {
	return &Resolver{tree: tree}
}

// Resolve 返回类型的有效属性，按属性名排序，结果与注册顺序无关。
func (r *Resolver) Resolve(typeName string) ([]typetree.Prop, error) {
	if r == nil || r.tree == nil {
		return nil, fmt.Errorf("props: resolver without a typetree")
	}
	props, err := r.tree.EffectiveProps(typeName)
	if err != nil {
		return nil, err
	}
	out := make([]typetree.Prop, len(props))
	copy(out, props)
	return out, nil
}

// Resolve 是包级便捷函数。
func Resolve(tree *typetree.Tree, typeName string) ([]typetree.Prop, error) {
	return NewResolver(tree).Resolve(typeName)
}
