package typetree

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Prop struct{ Name, Type string }

type propDef struct {
	prop   Prop
	origin string
}

type typeNode struct {
	parents []string
	eff     map[string]propDef
}

type Tree struct{ types map[string]*typeNode }

var (
	ErrEmptyName       = errors.New("typetree: empty type name")
	ErrTypeExists      = errors.New("typetree: type already exists")
	ErrParentNotFound  = errors.New("typetree: parent type not found")
	ErrCycle           = errors.New("typetree: cyclic inheritance")
	ErrTypeNotFound    = errors.New("typetree: type not found")
	ErrEmptyPropName   = errors.New("typetree: empty property name")
	ErrDuplicateProp   = errors.New("typetree: duplicate own property")
	ErrPropConflict    = errors.New("typetree: conflicting inherited properties")
	ErrInvalidOverride = errors.New("typetree: invalid property override")
)

func New() *Tree { return &Tree{types: map[string]*typeNode{}} }

// AddType 注册类型；父类型必须已存在。注册时完成环检测、菱形三态合并与
// 自身属性的协变覆盖检查，失败则不留下任何状态。
func (t *Tree) AddType(name string, parents []string, ownProps []Prop) error {
	if name == "" {
		return ErrEmptyName
	}
	if _, ok := t.types[name]; ok {
		return fmt.Errorf("%w: %s", ErrTypeExists, name)
	}
	pSet := map[string]struct{}{}
	for _, p := range parents {
		if p == name {
			return fmt.Errorf("%w: %s", ErrCycle, name+" -> "+name)
		}
		if !t.Has(p) {
			return fmt.Errorf("%w: parent %s of %s", ErrParentNotFound, p, name)
		}
		pSet[p] = struct{}{}
	}
	ps := sortedKeys(pSet)
	for _, p := range ps { // 新边 name->p 若让 p 可达 name 即成环
		if path := t.findPath(p, name); path != nil {
			seq := append(append([]string{name}, path...), name)
			return fmt.Errorf("%w: %s", ErrCycle, strings.Join(seq, " -> "))
		}
	}
	seen := map[string]struct{}{}
	for _, pr := range ownProps {
		if pr.Name == "" {
			return fmt.Errorf("%w: in type %s", ErrEmptyPropName, name)
		}
		if _, dup := seen[pr.Name]; dup {
			return fmt.Errorf("%w: %s.%s", ErrDuplicateProp, name, pr.Name)
		}
		seen[pr.Name] = struct{}{}
	}
	eff, err := t.mergeParents(ps)
	if err != nil {
		return err
	}
	for _, pr := range ownProps {
		if inh, ok := eff[pr.Name]; ok &&
			pr.Type != inh.prop.Type && !t.IsSubtype(pr.Type, inh.prop.Type) {
			return fmt.Errorf("%w: %s.%s:%s over %s.%s:%s",
				ErrInvalidOverride, name, pr.Name, pr.Type,
				inh.origin, pr.Name, inh.prop.Type)
		}
		eff[pr.Name] = propDef{prop: pr, origin: name}
	}
	t.types[name] = &typeNode{parents: ps, eff: eff}
	return nil
}

// mergeParents 按来源名排序后三态合并，结果与注册、列举顺序无关。
func (t *Tree) mergeParents(ps []string) (map[string]propDef, error) {
	gathered := map[string][]propDef{}
	for _, p := range ps {
		for _, d := range t.types[p].eff {
			gathered[d.prop.Name] = append(gathered[d.prop.Name], d)
		}
	}
	out := make(map[string]propDef, len(gathered))
	for pname, defs := range gathered {
		sort.Slice(defs, func(i, j int) bool { return defs[i].origin < defs[j].origin })
		cur := defs[0]
		for _, nxt := range defs[1:] {
			m, err := mergeTwo(pname, cur, nxt, t)
			if err != nil {
				return nil, err
			}
			cur = m
		}
		out[pname] = cur
	}
	return out, nil
}

func mergeTwo(pn string, a, b propDef, t *Tree) (propDef, error) {
	switch {
	case a.prop.Type == b.prop.Type:
		return a, nil // 相同 → 合并
	case t.IsSubtype(b.prop.Type, a.prop.Type):
		return b, nil // b 更窄 → 取 b
	case t.IsSubtype(a.prop.Type, b.prop.Type):
		return a, nil // a 更窄 → 取 a
	default:
		return propDef{}, fmt.Errorf("%w: %s.%s:%s vs %s.%s:%s",
			ErrPropConflict, a.origin, pn, a.prop.Type, b.origin, pn, b.prop.Type)
	}
}

// findPath 返回 from 沿父边到 to 的序列（含两端），不可达为 nil。
func (t *Tree) findPath(from, to string) []string {
	prev := map[string]string{from: ""}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == to {
			var path []string
			for c := to; c != ""; c = prev[c] {
				path = append([]string{c}, path...)
			}
			return path
		}
		node := t.types[cur]
		if node == nil {
			continue
		}
		for _, p := range node.parents {
			if _, seen := prev[p]; !seen {
				prev[p] = cur
				stack = append(stack, p)
			}
		}
	}
	return nil
}

// IsSubtype 报告 sub 是否为 super 的相同类型或沿父边可达的子类型。
func (t *Tree) IsSubtype(sub, super string) bool {
	return t.findPath(sub, super) != nil
}

func (t *Tree) Has(name string) bool { _, ok := t.types[name]; return ok }

// EffectiveProps 返回类型的有效属性，按属性名排序。
func (t *Tree) EffectiveProps(name string) ([]Prop, error) {
	node, ok := t.types[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTypeNotFound, name)
	}
	out := make([]Prop, 0, len(node.eff))
	for _, d := range node.eff {
		out = append(out, d.prop)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
