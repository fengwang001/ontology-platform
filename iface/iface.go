// Package iface 实现接口契约：接口声明若干要求属性及其类型约束，
// 类型以协变方式（实现属性是要求类型的子类型）满足契约。
package iface

import (
	"errors"
	"fmt"

	"ontology/typetree"
)

var (
	ErrEmptyName        = errors.New("iface: empty interface name")
	ErrIfaceExists      = errors.New("iface: interface already exists")
	ErrEmptyPropName    = errors.New("iface: empty required property name")
	ErrEmptyPropType    = errors.New("iface: empty required property type")
	ErrDuplicateProp    = errors.New("iface: duplicate required property")
	ErrIfaceNotFound    = errors.New("iface: interface not found")
	ErrMissingProp      = errors.New("iface: missing required property")
	ErrPropTypeMismatch = errors.New("iface: property type not compatible")
)

// Registry 持有接口声明并基于类型树做契约检查。
type Registry struct {
	tree   *typetree.Tree
	ifaces map[string][]typetree.Prop
}

func NewRegistry(tree *typetree.Tree) *Registry {
	return &Registry{tree: tree, ifaces: make(map[string][]typetree.Prop)}
}

// AddInterface 注册接口。requiredProps 按声明顺序检查，须无重名、非空。
func (r *Registry) AddInterface(name string, requiredProps []typetree.Prop) error {
	if name == "" {
		return ErrEmptyName
	}
	if _, exists := r.ifaces[name]; exists {
		return fmt.Errorf("%w: %s", ErrIfaceExists, name)
	}
	seen := make(map[string]struct{}, len(requiredProps))
	copied := make([]typetree.Prop, len(requiredProps))
	for i, p := range requiredProps {
		if p.Name == "" {
			return fmt.Errorf("%w: in interface %s", ErrEmptyPropName, name)
		}
		if p.Type == "" {
			return fmt.Errorf("%w: %s.%s", ErrEmptyPropType, name, p.Name)
		}
		if _, dup := seen[p.Name]; dup {
			return fmt.Errorf("%w: %s.%s", ErrDuplicateProp, name, p.Name)
		}
		seen[p.Name] = struct{}{}
		copied[i] = p
	}
	r.ifaces[name] = copied
	return nil
}

// Check 检查类型 typeName 是否满足接口 ifaceName。
// 返回 nil 表示满足；错误可经 errors.Is 区分为 ErrMissingProp
// （错误信息含具体缺失属性名）或 ErrPropTypeMismatch。
func (r *Registry) Check(typeName, ifaceName string) error {
	required, ok := r.ifaces[ifaceName]
	if !ok {
		return fmt.Errorf("%w: %s", ErrIfaceNotFound, ifaceName)
	}
	effective, err := r.tree.EffectiveProps(typeName)
	if err != nil {
		return err
	}
	byName := make(map[string]string, len(effective))
	for _, p := range effective {
		byName[p.Name] = p.Type
	}
	for _, req := range required {
		actual, present := byName[req.Name]
		if !present {
			return fmt.Errorf("%w: %s lacks %s",
				ErrMissingProp, typeName, req.Name)
		}
		// 协变：实现提供的 actual 必须是要求类型 req.Type 的子类型。
		if actual != req.Type && !r.tree.IsSubtype(actual, req.Type) {
			return fmt.Errorf("%w: %s.%s is %s, interface %s requires %s",
				ErrPropTypeMismatch, typeName, req.Name, actual,
				ifaceName, req.Type)
		}
	}
	return nil
}
