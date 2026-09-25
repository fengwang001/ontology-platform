// Package api 组装 typetree、props、iface 三个包，对外暴露唯一入口。
//
// 所有类型层级与接口声明共享同一底层 DAG：属性覆盖收窄、菱形三态合并、
// 接口协变检查使用的是同一套子类型关系。
package api

import (
	"fmt"

	"ontology/iface"
	"ontology/props"
	"ontology/typetree"
)

// API 是本体类型系统的唯一对外入口。
type API struct {
	tree   *typetree.Tree
	props  *props.Resolver
	ifaces *iface.Registry
}

func New() *API {
	t := typetree.New()
	return &API{
		tree:   t,
		props:  props.NewResolver(t),
		ifaces: iface.NewRegistry(t),
	}
}

// AddType 添加对象类型，参数做非空与基础校验后委托 typetree。
func (a *API) AddType(name string, parents []string, ownProps []typetree.Prop) error {
	if a == nil {
		return fmt.Errorf("api: nil receiver")
	}
	if name == "" {
		return fmt.Errorf("%w: empty type name", typetree.ErrInvalidName)
	}
	for _, p := range ownProps {
		if p.Name == "" {
			return fmt.Errorf("%w: type %s has a property with empty name", typetree.ErrInvalidName, name)
		}
		if p.Type == "" {
			return fmt.Errorf("%w: property %s.%s has empty type", typetree.ErrInvalidName, name, p.Name)
		}
	}
	return a.tree.AddType(name, parents, ownProps)
}

// AddInterface 添加接口声明。
func (a *API) AddInterface(name string, requiredProps []typetree.Prop) error {
	if a == nil {
		return fmt.Errorf("api: nil receiver")
	}
	if name == "" {
		return fmt.Errorf("%w: empty interface name", iface.ErrInvalidName)
	}
	for _, p := range requiredProps {
		if p.Name == "" {
			return fmt.Errorf("%w: interface %s has a property with empty name", iface.ErrInvalidName, name)
		}
		if p.Type == "" {
			return fmt.Errorf("%w: property %s.%s has empty type", iface.ErrInvalidName, name, p.Name)
		}
	}
	return a.ifaces.AddInterface(name, requiredProps)
}

// Resolve 返回类型的有效属性（按名排序）。
func (a *API) Resolve(typeName string) ([]typetree.Prop, error) {
	if a == nil {
		return nil, fmt.Errorf("api: nil receiver")
	}
	if typeName == "" {
		return nil, fmt.Errorf("%w: empty type name", typetree.ErrTypeNotFound)
	}
	if _, ok := a.tree.Type(typeName); !ok {
		return nil, fmt.Errorf("%w: %s", typetree.ErrTypeNotFound, typeName)
	}
	return a.props.Resolve(typeName)
}

// Check 判定类型是否满足接口（协变；缺属性/类型错配可 errors.Is 区分）。
func (a *API) Check(typeName, ifaceName string) error {
	if a == nil {
		return fmt.Errorf("api: nil receiver")
	}
	if _, ok := a.tree.Type(typeName); !ok {
		return fmt.Errorf("%w: %s", typetree.ErrTypeNotFound, typeName)
	}
	if _, ok := a.ifaces.Interface(ifaceName); !ok {
		return fmt.Errorf("%w: %s", iface.ErrInterfaceNotFound, ifaceName)
	}
	return a.ifaces.Check(typeName, ifaceName)
}

// IsSubtype 报告 sub 是否为 sup 的子类型（含相等）；名称须已注册。
func (a *API) IsSubtype(sub, sup string) (bool, error) {
	if a == nil {
		return false, fmt.Errorf("api: nil receiver")
	}
	if _, ok := a.tree.Type(sub); !ok {
		return false, fmt.Errorf("%w: %s", typetree.ErrTypeNotFound, sub)
	}
	if _, ok := a.tree.Type(sup); !ok {
		return false, fmt.Errorf("%w: %s", typetree.ErrTypeNotFound, sup)
	}
	return a.tree.IsSubtype(sub, sup)
}
