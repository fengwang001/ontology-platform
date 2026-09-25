// Package typetree 维护对象类型的继承 DAG：注册、循环继承检测、
// 多继承菱形冲突检测与属性覆盖方向校验。
package typetree

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/props"
)

var (
	ErrUnknownParent = errors.New("typetree: unknown parent type")
	ErrDuplicate     = errors.New("typetree: duplicate type or parent")
	ErrCycle         = errors.New("typetree: inheritance cycle")
	ErrOverride      = errors.New("typetree: override widens property type")
	// ErrConflict 重导出 props 的菱形冲突哨兵，便于调用方单点引用。
	ErrConflict = props.ErrConflict
)

// Type 是一个已注册的对象类型。
type Type struct {
	Name     string
	Parents  []string
	OwnProps map[string]string
}

// Tree 是继承 DAG。注册顺序即拓扑序，因此图中永远不会成环；
// findCycle 作为防御性校验仍在注册时运行。
type Tree struct {
	types              map[string]*Type
	cycleRejections    int
	conflictRejections int
	overrideRejections int
}

func New() *Tree { return &Tree{types: make(map[string]*Type)} }

// Type 返回已注册类型，ok 为 false 表示不存在。
func (t *Tree) Type(name string) (ty *Type, ok bool) {
	ty, ok = t.types[name]
	return ty, ok
}

// AddType 注册一个类型。依次检测：名称非法或重复、循环继承、
// 父类型不存在、多继承菱形冲突、覆盖方向非法（放宽）。
func (t *Tree) AddType(name string, parents []string, ownProps map[string]string) error {
	if name == "" {
		return errors.New("typetree: empty type name")
	}
	if _, ok := t.types[name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicate, name)
	}
	seen := make(map[string]bool, len(parents))
	for _, p := range parents {
		if p == "" {
			return fmt.Errorf("typetree: empty parent name of %q", name)
		}
		if seen[p] {
			return fmt.Errorf("%w: parent %q of %q", ErrDuplicate, p, name)
		}
		seen[p] = true
	}
	if ring := findCycle(t.types, name, parents); ring != nil {
		t.cycleRejections++
		return fmt.Errorf("%w: %s", ErrCycle, strings.Join(ring, " -> "))
	}
	for _, p := range parents {
		if _, ok := t.types[p]; !ok {
			return fmt.Errorf("%w: %q (parent of %q)", ErrUnknownParent, p, name)
		}
	}
	own := make(map[string]string, len(ownProps))
	for n, typ := range ownProps {
		own[n] = typ
	}
	ty := &Type{Name: name, Parents: append([]string(nil), parents...), OwnProps: own}
	t.types[name] = ty // 先提交，校验失败再回滚
	if err := t.validateJoin(ty); err != nil {
		delete(t.types, name)
		return err
	}
	return nil
}

// validateJoin 校验新类型接入点：祖先间菱形冲突、自身覆盖方向。
func (t *Tree) validateJoin(ty *Type) error {
	parents := append([]string(nil), ty.Parents...)
	sort.Strings(parents)
	anc := []props.Prop{}
	for _, p := range parents {
		rp, err := props.Resolve(t, p)
		if err != nil {
			return err
		}
		anc, err = props.Merge(t, anc, rp)
		if err != nil {
			t.conflictRejections++
			return err
		}
	}
	for _, a := range anc {
		sub, ok := ty.OwnProps[a.Name]
		if ok && !t.IsSubtype(sub, a.Type) {
			t.overrideRejections++
			return fmt.Errorf("%w: %q.%s is %q, ancestor %q declares %q",
				ErrOverride, ty.Name, a.Name, sub, a.Origin, a.Type)
		}
	}
	return nil
}

// IsSubtype 报告 sub 是否为 super 的子类型（自反闭包）。
// 未注册的类型名只与自身相等。
func (t *Tree) IsSubtype(sub, super string) bool {
	if sub == super {
		return true
	}
	seen := map[string]bool{sub: true}
	queue := []string{sub}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n == super {
			return true
		}
		if ty, ok := t.types[n]; ok {
			for _, p := range ty.Parents {
				if !seen[p] {
					seen[p] = true
					queue = append(queue, p)
				}
			}
		}
	}
	return false
}

// OwnProps 实现 props.Graph。
func (t *Tree) OwnProps(name string) (map[string]string, bool) {
	ty, ok := t.types[name]
	if !ok {
		return nil, false
	}
	return ty.OwnProps, true
}

// Parents 实现 props.Graph。
func (t *Tree) Parents(name string) []string {
	if ty, ok := t.types[name]; ok {
		return ty.Parents
	}
	return nil
}

// findCycle 在「现有图 + 待加入边 name->parents」上从 name 出发 DFS，
// 若回到 name 则返回环上的类型序列（首尾相接），否则返回 nil。
func findCycle(types map[string]*Type, name string, parents []string) []string {
	adj := func(n string) []string {
		if n == name {
			return parents
		}
		if ty, ok := types[n]; ok {
			return ty.Parents
		}
		return nil
	}
	var stack []string
	onStack := map[string]int{}
	done := map[string]bool{}
	var ring []string
	var dfs func(n string) bool
	dfs = func(n string) bool {
		if idx, ok := onStack[n]; ok {
			ring = append(append([]string(nil), stack[idx:]...), n)
			return true
		}
		if done[n] {
			return false
		}
		done[n] = true
		onStack[n] = len(stack)
		stack = append(stack, n)
		for _, m := range adj(n) {
			if dfs(m) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		delete(onStack, n)
		return false
	}
	dfs(name)
	return ring
}
