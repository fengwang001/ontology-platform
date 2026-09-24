// Package plan 在不修改命名空间的前提下检测冲突并把重命名请求编排成安全步骤序列。
package plan

import (
	"errors"
	"sort"

	"ontology/name"
)

// 四类冲突，全部可用 errors.Is 区分。
var (
	ErrTargetExists = errors.New("plan: target exists and is not moved in this batch")
	ErrDupTarget    = errors.New("plan: two requests rename to the same target")
	ErrDupSource    = errors.New("plan: the same old name appears twice")
	ErrMissing      = errors.New("plan: old name does not exist")
)

// Request 是一条重命名请求 old → new。
type Request struct{ Old, New string }

// Step 是编排后的一个执行动作。
type Step struct{ Old, New string }

// Conflict 携带冲突的具体上下文。
type Conflict struct {
	Kind       error
	Name       string // ErrMissing / ErrTargetExists 的相关名字
	First, Other string
}

func (c *Conflict) Error() string {
	switch {
	case errors.Is(c.Kind, ErrDupTarget):
		return "dup target " + c.Name + " from " + c.First + "," + c.Other
	case errors.Is(c.Kind, ErrDupSource):
		return "dup source " + c.Name + " renames to " + c.First + "," + c.Other
	default:
		return c.Kind.Error() + ": " + c.Name
	}
}
func (c *Conflict) Unwrap() error { return c.Kind }

// Planner 保存一次编排的输入、结果与内部查找计数。
type Planner struct {
	ns      *name.Namespace
	reqs    []Request
	lookups int
}

// New 绑定命名空间与请求（不做任何修改）。
func New(ns *name.Namespace, reqs []Request) *Planner {
	return &Planner{ns: ns, reqs: reqs}
}

// Lookups 返回内部 map 查找次数（只读）。
func (p *Planner) Lookups() int { return p.lookups }

func (p *Planner) has(n string) bool {
	p.lookups++
	return p.ns.ContainsLocked(n)
}

// Detect 只读取命名空间，检出任意一类冲突即返回；命名空间不会被修改。
func (p *Planner) Detect() error {
	p.lookups = 0
	p.ns.Lock()
	defer p.ns.Unlock()

	sourceOf := make(map[string]string) // old -> new（第一次）
	targetOf := make(map[string]string) // new -> old（第一次）
	isSource := make(map[string]bool)
	var missing, external []string

	for _, r := range p.reqs {
		if r.Old == r.New {
			continue // 自环：无操作
		}
		if prev, ok := sourceOf[r.Old]; ok {
			return &Conflict{Kind: ErrDupSource, Name: r.Old, First: prev, Other: r.New}
		}
		sourceOf[r.Old] = r.New
		isSource[r.Old] = true
		if !p.has(r.Old) {
			missing = append(missing, r.Old)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &Conflict{Kind: ErrMissing, Name: missing[0]}
	}
	for _, r := range p.reqs {
		if r.Old == r.New {
			continue
		}
		if prev, ok := targetOf[r.New]; ok {
			return &Conflict{Kind: ErrDupTarget, Name: r.New, First: prev, Other: r.Old}
		}
		targetOf[r.New] = r.Old
		if isSource[r.New] {
			continue // 会在本批被搬走
		}
		if p.has(r.New) {
			external = append(external, r.New)
		}
	}
	if len(external) > 0 {
		sort.Strings(external)
	return &Conflict{Kind: ErrTargetExists, Name: external[0]}
	}
	return nil
}

// Ordered 返回去掉自环后的请求，按源名字典序（供 cycle 包做确定性破环）。
func (p *Planner) Ordered() []Request {
	out := make([]Request, 0, len(p.reqs))
	for _, r := range p.reqs {
		if r.Old != r.New {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Old < out[j].Old })
	return out
}

// Order 对无环请求做拓扑排序：边 new→old，同层按名字字典序。
// cycles 中已登记的源视为将由临时名步骤处理，不参与普通拓扑序。
func Order(reqs []Request, cycles map[string]bool) []Step {
	next := make(map[string]string)  // 源 -> 目标
	pred := make(map[string]string)  // 目标 -> 源
	indeg := make(map[string]int)
	verts := map[string]bool{}
	for _, r := range reqs {
		if cycles[r.Old] || r.Old == r.New {
			continue
		}
		next[r.Old] = r.New
		verts[r.Old], verts[r.New] = true, true
	}
	for s := range next { // 依赖：目标若也是源，则必须先搬走
		if _, ok := next[next[s]]; ok {
			indeg[s]++
		}
	}
	for s, d := range next {
		pred[d] = s
	}
	var ready []string
	for v := range verts {
		if indeg[v] == 0 {
			ready = append(ready, v)
		}
	}
	var steps []Step
	for len(ready) > 0 {
		sort.Strings(ready)
		v := ready[0]
		ready = ready[1:]
		if dst, ok := next[v]; ok {
			steps = append(steps, Step{v, dst})
			if u, ok := pred[v]; ok && indeg[u] > 0 {
				indeg[u]--
				if indeg[u] == 0 {
					ready = append(ready, u)
				}
			}
		}
	}
	return steps
}
