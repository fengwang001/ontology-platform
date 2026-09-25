// Package iface 定义接口契约并校验类型是否满足契约。
// 兼容方向为协变：实现提供的属性类型必须是契约要求类型的子类型。
package iface

import (
	"errors"
	"fmt"
	"sort"

	"ontology/props"
)

var (
	// ErrMissingProperty 表示类型缺少接口要求的属性。
	ErrMissingProperty = errors.New("iface: missing required property")
	// ErrIncompatibleType 表示属性存在但类型不满足协变约束。
	ErrIncompatibleType = errors.New("iface: incompatible property type")
	// ErrDuplicate 表示接口重复注册。
	ErrDuplicate = errors.New("iface: duplicate interface")
)

// Registry 保存已注册接口。missing / incompatible 为非导出计数器，
// 分别统计两类契约失败，供测试断言判定路径可区分。
type Registry struct {
	ifaces       map[string]map[string]string
	missing      int
	incompatible int
}

func New() *Registry { return &Registry{ifaces: make(map[string]map[string]string)} }

// AddInterface 注册接口：name 及其对每个要求属性的类型约束。
func (r *Registry) AddInterface(name string, requiredProps map[string]string) error {
	if name == "" {
		return errors.New("iface: empty interface name")
	}
	if _, ok := r.ifaces[name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicate, name)
	}
	req := make(map[string]string, len(requiredProps))
	for n, typ := range requiredProps {
		if n == "" {
			return fmt.Errorf("iface: empty property name in %q", name)
		}
		req[n] = typ
	}
	r.ifaces[name] = req
	return nil
}

// Has 报告接口是否已注册。
func (r *Registry) Has(name string) bool {
	_, ok := r.ifaces[name]
	return ok
}

// Check 校验 typeName 是否满足 ifaceName 的契约。
// 返回 nil 表示满足；失败时返回以 ErrMissingProperty 或
// ErrIncompatibleType 包装的错误，可用 errors.Is 区分。
func (r *Registry) Check(g props.Graph, typeName, ifaceName string) error {
	req, ok := r.ifaces[ifaceName]
	if !ok {
		return fmt.Errorf("iface: unknown interface %q", ifaceName)
	}
	eff, err := props.Resolve(g, typeName)
	if err != nil {
		return err
	}
	have := make(map[string]string, len(eff))
	for _, p := range eff {
		have[p.Name] = p.Type
	}
	for _, name := range sortedKeys(req) {
		want := req[name]
		got, ok := have[name]
		if !ok {
			r.missing++
			return fmt.Errorf("%w: %q (interface %q requires it, type %q lacks it)",
				ErrMissingProperty, name, ifaceName, typeName)
		}
		if !g.IsSubtype(got, want) {
			r.incompatible++
			return fmt.Errorf("%w: %q: type %q provides %q, interface %q requires a subtype of %q",
				ErrIncompatibleType, name, typeName, got, ifaceName, want)
		}
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
