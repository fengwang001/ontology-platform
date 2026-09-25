// Package api 组装 typetree、props、iface 三个包，
// 对外暴露唯一入口 Ontology，并做参数校验。
package api

import (
	"errors"
	"fmt"

	"ontology/iface"
	"ontology/props"
	"ontology/typetree"
)

// 重导出哨兵错误，调用方只需引用 api 即可 errors.Is 判定。
var (
	ErrCycle            = typetree.ErrCycle
	ErrConflict         = typetree.ErrConflict
	ErrOverride         = typetree.ErrOverride
	ErrDuplicate        = typetree.ErrDuplicate
	ErrMissingProperty  = iface.ErrMissingProperty
	ErrIncompatibleType = iface.ErrIncompatibleType
	// ErrUnknownType 表示引用了未注册的类型。
	ErrUnknownType = errors.New("api: unknown type")
	// ErrUnknownInterface 表示引用了未注册的接口。
	ErrUnknownInterface = errors.New("api: unknown interface")
)

// Ontology 是类型层级与接口契约的统一入口。
type Ontology struct {
	tree   *typetree.Tree
	ifaces *iface.Registry
}

func New() *Ontology {
	return &Ontology{tree: typetree.New(), ifaces: iface.New()}
}

// AddType 注册对象类型：name 不可空，parents 必须已注册，
// ownProps 为自身声明的属性（属性名 -> 类型名）。
func (o *Ontology) AddType(name string, parents []string, ownProps map[string]string) error {
	if name == "" {
		return errors.New("api: empty type name")
	}
	return o.tree.AddType(name, parents, ownProps)
}

// AddInterface 注册接口：name 不可空，requiredProps 为契约（属性名 -> 类型名）。
func (o *Ontology) AddInterface(name string, requiredProps map[string]string) error {
	if name == "" {
		return errors.New("api: empty interface name")
	}
	return o.ifaces.AddInterface(name, requiredProps)
}

// Resolve 返回类型的有效属性集（自身 + 继承 + 覆盖），按属性名排序。
func (o *Ontology) Resolve(typeName string) ([]props.Prop, error) {
	if typeName == "" {
		return nil, errors.New("api: empty type name")
	}
	if _, ok := o.tree.Type(typeName); !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, typeName)
	}
	return props.Resolve(o.tree, typeName)
}

// Check 校验类型是否满足接口契约，失败原因可 errors.Is 区分。
func (o *Ontology) Check(typeName, ifaceName string) error {
	if typeName == "" || ifaceName == "" {
		return errors.New("api: empty type or interface name")
	}
	if _, ok := o.tree.Type(typeName); !ok {
		return fmt.Errorf("%w: %q", ErrUnknownType, typeName)
	}
	if !o.ifaces.Has(ifaceName) {
		return fmt.Errorf("%w: %q", ErrUnknownInterface, ifaceName)
	}
	return o.ifaces.Check(o.tree, typeName, ifaceName)
}

// IsSubtype 报告 sub 是否为 super 的子类型（自反闭包）。
func (o *Ontology) IsSubtype(sub, super string) bool {
	if sub == "" || super == "" {
		return false
	}
	return o.tree.IsSubtype(sub, super)
}
