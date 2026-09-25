// Package iface 实现接口声明与契约检查。
//
// 契约方向为协变（见 DESIGN.md 推导点 3）：接口要求 p: T，实现提供 p: U，
// 当且仅当 U 是 T 的子类型（U ≼ T）时满足。两类失败用不同哨兵错误，
// 可通过 errors.Is 区分：缺属性 ErrMissingProp（带具体属性名）、
// 类型不兼容 ErrPropTypeMismatch。
package iface

import (
	"errors"
	"fmt"
	"sort"

	"ontology/props"
	"ontology/typetree"
)

var (
	ErrDuplicateInterface = errors.New("duplicate interface name")
	ErrInterfaceNotFound  = errors.New("interface not found")
	ErrInvalidName        = errors.New("invalid interface name")
	ErrDuplicateProp      = errors.New("duplicate required property")
	ErrMissingProp        = errors.New("missing required property")
	ErrPropTypeMismatch   = errors.New("property type does not satisfy the interface")
)

// Interface 是一个接口声明：对若干属性的类型约束。
type Interface struct {
	Name     string
	Required []typetree.Prop
}

// Registry 保存接口声明，并在给定 DAG 上做契约检查。
type Registry struct {
	tree     *typetree.Tree
	resolver *props.Resolver
	ifaces   map[string]Interface
}

func NewRegistry(t *typetree.Tree) *Registry {
	return &Registry{tree: t, resolver: props.NewResolver(t), ifaces: make(map[string]Interface)}
}

// AddInterface 注册接口；校验空名、重名、要求属性唯一。
func (r *Registry) AddInterface(name string, required []typetree.Prop) error {
	if name == "" {
		return fmt.Errorf("%w: empty", ErrInvalidName)
	}
	if _, exists := r.ifaces[name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateInterface, name)
	}
	seen := make(map[string]bool, len(required))
	cp := make([]typetree.Prop, len(required))
	copy(cp, required)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Name < cp[j].Name })
	for _, p := range cp {
		if seen[p.Name] {
			return fmt.Errorf("%w: interface %s declares %s twice", ErrDuplicateProp, name, p.Name)
		}
		seen[p.Name] = true
	}
	r.ifaces[name] = Interface{Name: name, Required: cp}
	return nil
}

// Interface 返回已注册接口的快照。
func (r *Registry) Interface(name string) (Interface, bool) {
	v, ok := r.ifaces[name]
	return v, ok
}

// Check 判定类型 typeName 是否满足接口 ifaceName。
//
// 返回 nil 表示满足；否则错误包装 ErrMissingProp（含具体缺失属性名）
// 或 ErrPropTypeMismatch（含实现/要求双方类型），可用 errors.Is 区分。
// 同一属性既缺失又错配时按缺失报告；多个属性有问题时报第一个（按属性名排序）。
func (r *Registry) Check(typeName, ifaceName string) error {
	it, ok := r.ifaces[ifaceName]
	if !ok {
		return fmt.Errorf("%w: %s", ErrInterfaceNotFound, ifaceName)
	}
	effective, err := r.resolver.Resolve(typeName)
	if err != nil {
		return err
	}
	byName := make(map[string]string, len(effective))
	for _, p := range effective {
		byName[p.Name] = p.Type
	}

	var firstMissing, firstMismatch string
	var mismatchGot, mismatchWant string
	for _, req := range it.Required {
		got, present := byName[req.Name]
		if !present {
			if firstMissing == "" {
				firstMissing = req.Name
			}
			continue
		}
		ok, err := r.tree.IsSubtype(got, req.Type)
		if err != nil {
			return err
		}
		if !ok && firstMismatch == "" {
			// 协变：实现类型 U(got) 必须是要求类型 T(req.Type) 的子类型。
			firstMismatch, mismatchGot, mismatchWant = req.Name, got, req.Type
		}
	}
	if firstMissing != "" {
		return fmt.Errorf("%w: type %s does not provide %s required by %s",
			ErrMissingProp, typeName, firstMissing, ifaceName)
	}
	if firstMismatch != "" {
		return fmt.Errorf("%w: %s.%s is %s, interface %s requires %s (covariant: provided must be subtype)",
			ErrPropTypeMismatch, typeName, firstMismatch, mismatchGot, ifaceName, mismatchWant)
	}
	return nil
}
