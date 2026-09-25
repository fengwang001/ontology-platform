// Package props 解析类型的有效属性集：自身属性 + 祖先继承属性，
// 含覆盖（自身优先）与多继承菱形三态合并。结果按属性名排序，
// 与注册顺序无关。
package props

import (
	"errors"
	"fmt"
	"sort"
)

// ErrConflict 表示多继承下同一属性在两个祖先处类型不兼容。
var ErrConflict = errors.New("props: inherited property conflict")

// mergeCalls 非导出计数器：合并调用次数。测试用它配合逐字节比对
// 证明「同一类型集任意注册顺序，Resolve 结果与合并路径一致」。
var mergeCalls int

// Graph 是继承 DAG 的只读视图，避免 props 与 typetree 循环依赖。
type Graph interface {
	OwnProps(name string) (map[string]string, bool)
	Parents(name string) []string
	IsSubtype(sub, super string) bool
}

// Prop 是一个有效属性：名字、类型、声明来源（Origin 为声明它的类型名）。
type Prop struct {
	Name   string
	Type   string
	Origin string
}

// Resolve 返回 name 的有效属性集，按属性名排序。
// 祖先按字典序拓扑合并，自身属性最后覆盖，保证确定性。
func Resolve(g Graph, name string) ([]Prop, error) {
	return resolve(g, name, make(map[string][]Prop))
}

func resolve(g Graph, name string, memo map[string][]Prop) ([]Prop, error) {
	if r, ok := memo[name]; ok {
		return r, nil
	}
	own, ok := g.OwnProps(name)
	if !ok {
		return nil, fmt.Errorf("props: unknown type %q", name)
	}
	parents := append([]string(nil), g.Parents(name)...)
	sort.Strings(parents)
	merged := []Prop{}
	for _, p := range parents {
		rp, err := resolve(g, p, memo)
		if err != nil {
			return nil, err
		}
		merged, err = Merge(g, merged, rp)
		if err != nil {
			return nil, err
		}
	}
	out := make(map[string]Prop, len(merged)+len(own))
	for _, p := range merged {
		out[p.Name] = p
	}
	for n, typ := range own { // 自身属性最后写入，覆盖继承结果
		out[n] = Prop{Name: n, Type: typ, Origin: name}
	}
	res := sorted(out)
	memo[name] = res
	return res, nil
}

// Merge 按菱形三态规则合并两个有效属性集：
// 类型相同或一方更窄 → 取更窄者；互不兼容 → ErrConflict 并指出双方来源。
func Merge(g Graph, a, b []Prop) ([]Prop, error) {
	mergeCalls++
	out := make(map[string]Prop, len(a)+len(b))
	for _, p := range a {
		out[p.Name] = p
	}
	for _, p := range b {
		cur, ok := out[p.Name]
		if !ok {
			out[p.Name] = p
			continue
		}
		switch {
		case g.IsSubtype(p.Type, cur.Type):
			out[p.Name] = p // p 更窄（含相等），取 p
		case g.IsSubtype(cur.Type, p.Type):
			// cur 更窄，保持
		default:
			return nil, fmt.Errorf("%w: %q: %q declares %q, %q declares %q",
				ErrConflict, p.Name, cur.Origin, cur.Type, p.Origin, p.Type)
		}
	}
	return sorted(out), nil
}

func sorted(m map[string]Prop) []Prop {
	res := make([]Prop, 0, len(m))
	for _, p := range m {
		res = append(res, p)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Name < res[j].Name })
	return res
}
