// Package typetree 维护对象类型的继承 DAG。
//
// 注册类型时校验：父类型存在、继承无环、自身属性不重名、
// 属性覆盖必须收窄（子类型属性是父类型同名属性的子类型）、
// 多继承同名属性按菱形三态规则处理（相同合并 / 兼容取窄 / 不兼容报错）。
package typetree

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrDuplicateType  = errors.New("duplicate type name")
	ErrParentNotFound = errors.New("parent type not found")
	ErrTypeNotFound   = errors.New("type not found")
	ErrCycle          = errors.New("cyclic inheritance")
	ErrBadOverride    = errors.New("property override must narrow the parent type")
	ErrPropConflict   = errors.New("inherited property type conflict")
	ErrInvalidName    = errors.New("invalid type name")
)

// Prop 是一个属性声明：名字 + 类型名。
type Prop struct {
	Name string
	Type string
}

// Type 是一个已注册对象类型的快照。
type Type struct {
	Name    string
	Parents []string
	Own     []Prop
}

// Tree 是继承 DAG。
type Tree struct {
	types map[string]Type
}

func New() *Tree {
	return &Tree{types: make(map[string]Type)}
}

// AddType 注册一个对象类型及其父类型与自身属性。
func (t *Tree) AddType(name string, parents []string, ownProps []Prop) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: empty", ErrInvalidName)
	}
	if _, exists := t.types[name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateType, name)
	}
	if cyc := findCycle(name, parents, t); cyc != nil {
		return fmt.Errorf("%w: %s", ErrCycle, strings.Join(cyc, " -> "))
	}
	for _, p := range parents {
		if _, ok := t.types[p]; !ok {
			return fmt.Errorf("%w: %s (required by %s)", ErrParentNotFound, p, name)
		}
	}

	seen := make(map[string]bool, len(ownProps))
	for _, p := range ownProps {
		if seen[p.Name] {
			return fmt.Errorf("%w: type %s declares %s twice", ErrDuplicateType, name, p.Name)
		}
		seen[p.Name] = true
	}

	// 在临时 DAG 上计算有效属性，提前暴露菱形冲突与非法放宽。
	trial := *t
	trial.types = make(map[string]Type, len(t.types)+1)
	for k, v := range t.types {
		trial.types[k] = v
	}
	trial.types[name] = Type{Name: name, Parents: append([]string(nil), parents...), Own: append([]Prop(nil), ownProps...)}
	if _, err := trial.EffectiveProps(name); err != nil {
		return err
	}

	t.types[name] = trial.types[name]
	return nil
}

// Type 返回已注册类型的快照。
func (t *Tree) Type(name string) (Type, bool) {
	v, ok := t.types[name]
	return v, ok
}

// Names 返回全部类型名（已排序）。
func (t *Tree) Names() []string {
	out := make([]string, 0, len(t.types))
	for n := range t.types {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// IsSubtype 报告 sub 是否为 sup 的子类型（含相等）。
// 未注册的类型名仅与自身相等；类型层级存在时按 DAG 可达性判定。
func (t *Tree) IsSubtype(sub, sup string) (bool, error) {
	if sub == sup {
		return true, nil
	}
	if _, ok := t.types[sub]; !ok {
		return false, fmt.Errorf("%w: %s", ErrTypeNotFound, sub)
	}
	return t.ancestorReachable(sub, sup), nil
}

func (t *Tree) ancestorReachable(from, target string) bool {
	cur, ok := t.types[from]
	if !ok {
		return false
	}
	for _, p := range cur.Parents {
		if p == target || t.ancestorReachable(p, target) {
			return true
		}
	}
	return false
}

// findCycle 从待加入类型出发，沿父边寻找回到 name 的路径。
// path 形如 name -> ... -> name；找不到返回 nil。
func findCycle(name string, parents []string, t *Tree) []string {
	var dfs func(cur string, path []string) []string
	dfs = func(cur string, path []string) []string {
		if cur == name {
			return append(path, name)
		}
		node, ok := t.types[cur]
		if !ok {
			return nil
		}
		for _, p := range node.Parents {
			if r := dfs(p, append(path, cur)); r != nil {
				return r
			}
		}
		return nil
	}
	for _, p := range parents {
		if r := dfs(p, []string{name}); r != nil {
			return r
		}
	}
	return nil
}

type candidate struct {
	prop      Prop
	source    string
	inherited bool
}

// EffectiveProps 返回类型 name 的有效属性（自身 + 全部祖先，含覆盖与菱形合并），按名排序。
func (t *Tree) EffectiveProps(name string) ([]Prop, error) {
	return t.effectiveProps(name, make(map[string][]Prop))
}

// effectiveProps 计算（含校验）类型 name 的有效属性，memo 跨菱形共享结果。
func (t *Tree) effectiveProps(name string, memo map[string][]Prop) ([]Prop, error) {
	if cached, ok := memo[name]; ok {
		return cached, nil
	}
	node, ok := t.types[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTypeNotFound, name)
	}

	merged := make(map[string]candidate)
	for _, parentName := range node.Parents {
		pp, err := t.effectiveProps(parentName, memo)
		if err != nil {
			return nil, err
		}
		for _, p := range pp {
			if err := t.mergeInherited(merged, p, parentName); err != nil {
				return nil, err
			}
		}
	}

	// 自身属性：覆盖必须相同或更窄。
	for _, own := range node.Own {
		if old, ok := merged[own.Name]; ok {
			narrower, err := t.isSubtypeKnown(own.Type, old.prop.Type)
			if err != nil {
				return nil, err
			}
			if !narrower {
				return nil, fmt.Errorf("%w: %s.%s type %s is not a subtype of inherited %s (from %s)",
					ErrBadOverride, name, own.Name, own.Type, old.prop.Type, old.source)
			}
		}
		merged[own.Name] = candidate{prop: own, source: name}
	}

	out := make([]Prop, 0, len(merged))
	for _, c := range merged {
		out = append(out, c.prop)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	memo[name] = out
	return out, nil
}

func (t *Tree) mergeInherited(m map[string]candidate, p Prop, source string) error {
	old, ok := m[p.Name]
	if !ok {
		m[p.Name] = candidate{prop: p, source: source, inherited: true}
		return nil
	}
	if old.prop.Type == p.Type {
		// 三态 1：相同类型，合并为一个，来源合并记录。
		old.source = old.source + "," + source
		m[p.Name] = old
		return nil
	}
	narrowLeft, err := t.isSubtypeKnown(old.prop.Type, p.Type)
	if err != nil {
		return err
	}
	narrowRight, err := t.isSubtypeKnown(p.Type, old.prop.Type)
	if err != nil {
		return err
	}
	switch {
	case narrowLeft:
		// 三态 2：旧候选更窄，保留旧候选。
		return nil
	case narrowRight:
		// 三态 2：新候选更窄，取更窄者。
		m[p.Name] = candidate{prop: p, source: source, inherited: true}
		return nil
	default:
		// 三态 3：互不兼容。
		return fmt.Errorf("%w: %s: %s.%s=%s vs %s.%s=%s",
			ErrPropConflict, p.Name, old.source, p.Name, old.prop.Type, source, p.Name, p.Type)
	}
}

// isSubtypeKnown 供属性合并内部使用：两个类型名均应已在 DAG 中注册。
func (t *Tree) isSubtypeKnown(sub, sup string) (bool, error) {
	if sub == sup {
		return true, nil
	}
	if _, ok := t.types[sub]; !ok {
		return false, fmt.Errorf("%w: %s", ErrTypeNotFound, sub)
	}
	return t.ancestorReachable(sub, sup), nil
}
