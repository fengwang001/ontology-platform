// Package api 组装 typetree/props/iface，对外暴露唯一入口。
package api

import (
	"ontology/iface"
	"ontology/props"
	"ontology/typetree"
)

// API 是类型系统的唯一入口。
type API struct {
	tree *typetree.Tree
	res  *props.Resolver
	reg  *iface.Registry
}

func New() *API {
	tree := typetree.New()
	return &API{
		tree: tree,
		res:  props.NewResolver(tree),
		reg:  iface.NewRegistry(tree),
	}
}

// AddType 注册对象类型。parents 可为 nil；ownProps 可为 nil。
func (a *API) AddType(name string, parents []string, ownProps []typetree.Prop) error {
	if name == "" {
		return typetree.ErrEmptyName
	}
	if ownProps == nil {
		ownProps = []typetree.Prop{}
	}
	return a.tree.AddType(name, dedupParents(parents), ownProps)
}

// AddInterface 注册接口契约。
func (a *API) AddInterface(name string, requiredProps []typetree.Prop) error {
	if name == "" {
		return iface.ErrEmptyName
	}
	if requiredProps == nil {
		requiredProps = []typetree.Prop{}
	}
	return a.reg.AddInterface(name, requiredProps)
}

// Resolve 返回类型有效属性，按属性名排序。
func (a *API) Resolve(typeName string) ([]typetree.Prop, error) {
	if typeName == "" {
		return nil, typetree.ErrEmptyName
	}
	return a.res.Resolve(typeName)
}

// Check 检查类型是否满足接口；nil 表示满足。
func (a *API) Check(typeName, ifaceName string) error {
	if typeName == "" {
		return typetree.ErrEmptyName
	}
	if ifaceName == "" {
		return iface.ErrEmptyName
	}
	return a.reg.Check(typeName, ifaceName)
}

// IsSubtype 报告 sub 是否为 super 的相同类型或子类型。
func (a *API) IsSubtype(sub, super string) bool {
	if sub == "" || super == "" {
		return false
	}
	return a.tree.IsSubtype(sub, super)
}

// 透传哨兵，方便调用方只依赖 api 包做 errors.Is。
var (
	ErrMissingProp      = iface.ErrMissingProp
	ErrPropTypeMismatch = iface.ErrPropTypeMismatch
	ErrCycle            = typetree.ErrCycle
	ErrPropConflict     = typetree.ErrPropConflict
	ErrInvalidOverride  = typetree.ErrInvalidOverride
	ErrTypeNotFound     = typetree.ErrTypeNotFound
)

func dedupParents(parents []string) []string {
	if len(parents) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(parents))
	out := make([]string, 0, len(parents))
	for _, p := range parents {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
