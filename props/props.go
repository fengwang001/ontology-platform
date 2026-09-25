// Package props 在继承 DAG 上解析类型的有效属性集。
//
// 有效属性 = 自身属性 + 所有祖先继承的属性；子类型对同名属性的覆盖必须
// 收窄（见 typetree 包的注册校验）；多继承同名属性按菱形三态合并
// （相同合并 / 一方更窄取窄 / 不兼容报错）。结果按属性名排序，
// 不依赖任何插入顺序，保证确定性。
package props

import (
	"fmt"

	"ontology/typetree"
)

// Resolver 在给定的继承 DAG 上解析有效属性。
type Resolver struct {
	tree *typetree.Tree
}

func NewResolver(t *typetree.Tree) *Resolver {
	return &Resolver{tree: t}
}

// Resolve 返回类型 name 的有效属性集，按属性名排序。
// 属性覆盖方向（收窄）与菱形三态冲突在 typetree.AddType 注册时已校验；
// 对未注册类型名返回包装了 typetree.ErrTypeNotFound 的错误。
func (r *Resolver) Resolve(name string) ([]typetree.Prop, error) {
	if r == nil || r.tree == nil {
		return nil, fmt.Errorf("props: resolver is not initialized")
	}
	props, err := r.tree.EffectiveProps(name)
	if err != nil {
		return nil, err
	}
	// 返回副本，调用方修改不影响 DAG 内部状态。
	out := make([]typetree.Prop, len(props))
	copy(out, props)
	return out, nil
}
