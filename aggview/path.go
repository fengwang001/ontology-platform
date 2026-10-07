package aggview

import (
	"fmt"

	"ontology/ontology"
)

// PathDecl 声明一条固定长度的链接路径：
//
//	source --hops[0]--> t1 --hops[1]--> ... --hops[len-1]--> target
//
// 每一跳 HopDecl 给出关系名与"该跳终点实例允许的对象类型集合"。
// 类型集合允许在运行时通过 Engine.AddHopType 结构性地新增成员。
type PathDecl struct {
	Name   string
	Source string
	Hops   []HopDecl
	Target string
	Attr   string
}

// HopDecl 描述路径中的一跳。
// Relation 为该跳链接必须具备的关系名。
// Types 为该跳终点实例允许的对象类型集合（至少一个成员）。
type HopDecl struct {
	Relation string
	Types    []string
}

// viewPath 是视图内部持有的可变路径声明（类型集合可新增成员）。
type viewPath struct {
	name   string
	source string
	target string
	attr   string
	hops   []*hop
}

type hop struct {
	relation string
	types    map[string]struct{}
}

// Len 返回路径跳数。
func (p *viewPath) Len() int { return len(p.hops) }

// allowsType 报告第 i 跳的终点类型集合是否包含 typ。
func (p *viewPath) allowsType(i int, typ string) bool {
	_, ok := p.hops[i].types[typ]
	return ok
}

// staticallyCycleFree 报告该声明是否能在声明阶段静态排除环：
// 路径上每个"位置类型序列"（位置 0 为起点类型，位置 i+1 来自第 i 跳
// 的类型集合，末位置为终点类型）两两不相交时，任何实例都不可能在
// 行走过程中重复访问自身类型所在的位置，从而不存在环。
//
// 若不能静态排除，链路结构变更（新增链接）在运行时会做局部环检查，
// 无法排除时拒绝该次变更（ErrCycleDetected）。
func (p *viewPath) staticallyCycleFree() bool {
	seen := map[string]int{}
	add := func(pos int, typ string) bool {
		if prev, ok := seen[typ]; ok && prev != pos {
			return false
		}
		seen[typ] = pos
		return true
	}
	if !add(0, p.source) {
		return false
	}
	for i, h := range p.hops {
		for t := range h.types {
			if !add(i+1, t) {
				return false
			}
		}
	}
	if !add(len(p.hops), p.target) {
		return false
	}
	return true
}

// validateDecl 在声明阶段做类型匹配校验（错误优先级最高的一类）。
// st 为 nil 时跳过"类型是否已注册"检查（仅做声明自身一致性检查）。
func validateDecl(d PathDecl, st *ontology.Store) *ontology.AggregateError {
	if d.Name == "" {
		return ontology.NewTypeMismatchError("view name is empty")
	}
	if d.Source == "" || d.Target == "" || d.Attr == "" {
		return ontology.NewTypeMismatchError("source/target/attr must be non-empty")
	}
	if len(d.Hops) == 0 {
		return ontology.NewTypeMismatchError("path must contain at least one hop")
	}
	for i, h := range d.Hops {
		if h.Relation == "" {
			return ontology.NewTypeMismatchError(fmt.Sprintf("hop %d relation is empty", i))
		}
		if len(h.Types) == 0 {
			return ontology.NewTypeMismatchError(fmt.Sprintf("hop %d has empty type set", i))
		}
	}
	if st != nil {
		if !st.HasType(d.Source) {
			return ontology.NewTypeMismatchError(fmt.Sprintf("source type %q not registered", d.Source))
		}
		if !st.HasType(d.Target) {
			return ontology.NewTypeMismatchError(fmt.Sprintf("target type %q not registered", d.Target))
		}
		for i, h := range d.Hops {
			for _, t := range h.Types {
				if !st.HasType(t) {
					return ontology.NewTypeMismatchError(fmt.Sprintf("hop %d type %q not registered", i, t))
				}
			}
		}
	}
	return nil
}

func newViewPath(d PathDecl) *viewPath {
	hops := make([]*hop, len(d.Hops))
	for i, h := range d.Hops {
		set := make(map[string]struct{}, len(h.Types))
		for _, t := range h.Types {
			set[t] = struct{}{}
		}
		hops[i] = &hop{relation: h.Relation, types: set}
	}
	return &viewPath{name: d.Name, source: d.Source, hops: hops, target: d.Target, attr: d.Attr}
}
