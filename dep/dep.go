// Package dep 定义列声明、依赖图校验（环检测、未声明依赖）与传递依赖闭包。
package dep

import "errors"

var (
	// ErrCycle 依赖图含环。
	ErrCycle = errors.New("dep: cyclic dependency")
	// ErrUnknownDep 派生列依赖了未声明的列。
	ErrUnknownDep = errors.New("dep: dependency on undeclared column")
)

// Derived 派生列声明：Deps 为依赖列名（有序），Fn 按 Deps 顺序取值计算。
type Derived struct {
	Deps []string
	Fn   func([]int) int
}

// Spec 列声明全集：Base 为基列及其初值，Derived 为派生列。
type Spec struct {
	Base    map[string]int
	Derived map[string]Derived
}

// Validate 校验依赖图：依赖的列必须已声明，且图无环。
func Validate(s Spec) error {
	for _, d := range s.Derived {
		for _, dep := range d.Deps {
			if _, ok := s.Base[dep]; ok {
				continue
			}
			if _, ok := s.Derived[dep]; ok {
				continue
			}
			return ErrUnknownDep
		}
	}
	// DFS 三色环检测，只走派生列之间的边。
	const (
		white = iota // 未访问
		gray         // 在栈上
		black        // 已完成
	)
	state := map[string]int{}
	var visit func(n string) error
	visit = func(n string) error {
		switch state[n] {
		case gray:
			return ErrCycle
		case black:
			return nil
		}
		state[n] = gray
		for _, dep := range s.Derived[n].Deps {
			if _, isDerived := s.Derived[dep]; isDerived {
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		state[n] = black
		return nil
	}
	for name := range s.Derived {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

// Dependents 返回反向邻接表：x -> 直接依赖 x 的派生列集合。
func Dependents(s Spec) map[string][]string {
	rev := map[string][]string{}
	for name, d := range s.Derived {
		for _, dep := range d.Deps {
			rev[dep] = append(rev[dep], name)
		}
	}
	return rev
}

// Closure 返回传递依赖 base 的全部派生列（不含 base 自身）。
func Closure(rev map[string][]string, base string) map[string]bool {
	out := map[string]bool{}
	stack := append([]string(nil), rev[base]...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out[n] {
			continue
		}
		out[n] = true
		stack = append(stack, rev[n]...)
	}
	return out
}
