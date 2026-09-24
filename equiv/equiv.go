// Package equiv 在有限域上穷举所有赋值，验证两棵谓词树是否等价。
package equiv

import (
	"fmt"
	"sort"
	"strings"

	"ontology/ast"
)

// Assignment 是一组列赋值，键为 "table.name"。
type Assignment map[string]ast.Tri

func (a Assignment) String() string {
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%s", k, []string{"F", "U", "T"}[a[k]])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func unionCols(a, b *ast.Node) []string {
	set := map[string]bool{}
	for _, c := range a.Cols() {
		set[c] = true
	}
	for _, c := range b.Cols() {
		set[c] = true
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// enumerate 对 cols 的全部 3^n 种赋值逐一调用 f，f 返回 false 时停止并
// 返回当前赋值；全部通过则返回 nil。
func enumerate(cols []string, f func(Assignment) bool) Assignment {
	n := len(cols)
	total := 1
	for i := 0; i < n; i++ {
		total *= 3
	}
	env := make(Assignment, n)
	for i := 0; i < total; i++ {
		x := i
		for _, c := range cols {
			env[c] = ast.Tri(x % 3)
			x /= 3
		}
		if !f(env) {
			return env
		}
	}
	return nil
}

// Check 在 3^len(cols) 种赋值下比对两树的求值结果；cols 为 nil 时取两树
// 列的并集。返回第一组不同的赋值，完全等价返回 nil。
func Check(a, b *ast.Node, cols []string) Assignment {
	if cols == nil {
		cols = unionCols(a, b)
	}
	return enumerate(cols, func(env Assignment) bool {
		return a.Eval(env) == b.Eval(env)
	})
}

// CheckTop 按 WHERE 过滤语义比对：只关心求值是否为 True（U 与 F 同效）。
func CheckTop(a, b *ast.Node, cols []string) Assignment {
	if cols == nil {
		cols = unionCols(a, b)
	}
	return enumerate(cols, func(env Assignment) bool {
		return (a.Eval(env) == ast.True) == (b.Eval(env) == ast.True)
	})
}

// Diff 等价于 Check，但以带赋值详情的错误报告差异，便于定位规则错误。
func Diff(a, b *ast.Node, cols []string) error {
	if cols == nil {
		cols = unionCols(a, b)
	}
	var la, lb ast.Tri
	bad := enumerate(cols, func(env Assignment) bool {
		la, lb = a.Eval(env), b.Eval(env)
		return la == lb
	})
	if bad == nil {
		return nil
	}
	names := []string{"FALSE", "NULL", "TRUE"}
	return fmt.Errorf("equiv: differ at %s: left=%s right=%s",
		bad, names[la], names[lb])
}
