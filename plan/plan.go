// Package plan 在任何修改之前完成冲突检测，并算出确定性的安全执行顺序（拓扑序）。
package plan

import (
	"errors"
	"sort"

	"ontology/name"
)

var (
	// ErrDupSource 同一旧名出现两次。
	ErrDupSource = errors.New("plan: duplicate source name")
	// ErrDupTarget 两个请求指向同一新名。
	ErrDupTarget = errors.New("plan: duplicate target name")
	// ErrMissingSource 旧名不存在于命名空间。
	ErrMissingSource = errors.New("plan: source name missing")
	// ErrTargetExists 目标已存在且本批不会把它搬走。
	ErrTargetExists = errors.New("plan: target already exists")
)

// Req 是一条重命名请求。
type Req struct {
	Old string
	New string
}

// Conflict 携带可判定的冲突类型与相关名字。
type Conflict struct {
	Kind error
	A, B string
}

func (c *Conflict) Error() string { return "plan conflict: " + c.Kind.Error() }
func (c *Conflict) Unwrap() error { return c.Kind }

// Planner 保存请求与 map 访问计数。
type Planner struct {
	reqs   []Req
	lookup int // 非导出：线性复杂度计数器
}

// New 构造 Planner。
func New(reqs []Req) *Planner { return &Planner{reqs: reqs} }

// Lookups 返回到目前为止的 map 查找次数（测试用于线性复杂度断言）。
func (p *Planner) Lookups() int { return p.lookup }

func (p *Planner) get(m map[string]struct{}, k string) bool {
	p.lookup++
	_, ok := m[k]
	return ok
}

func (p *Planner) getReq(m map[string]Req, k string) (Req, bool) {
	p.lookup++
	r, ok := m[k]
	return r, ok
}

// Check 仅做冲突检测，绝不修改命名空间。
func (p *Planner) Check(ns *name.Namespace) error {
	ns.RLock()
	defer ns.RUnlock()

	byOld := make(map[string]Req, len(p.reqs))
	byNew := make(map[string]Req, len(p.reqs))
	moving := make(map[string]struct{}, len(p.reqs))
	existing := ns.Snapshot()
	exSet := make(map[string]struct{}, len(existing))
	for _, n := range existing {
		exSet[n] = struct{}{}
	}
	for _, r := range p.reqs {
		if r.Old != r.New {
			moving[r.Old] = struct{}{}
		}
	}
	blocked := make(map[string]struct{}, len(exSet))
	for _, n := range existing {
		p.lookup++
		if _, mv := moving[n]; !mv {
			blocked[n] = struct{}{}
		}
	}
	for _, r := range p.reqs {
		if prev, ok := p.getReq(byOld, r.Old); ok {
			return &Conflict{Kind: ErrDupSource, A: prev.New, B: r.New}
		}
		byOld[r.Old] = r
		if r.Old == r.New {
			continue // 自环：无操作，不占用目标
		}
		if prev, ok := p.getReq(byNew, r.New); ok {
			return &Conflict{Kind: ErrDupTarget, A: prev.Old, B: r.Old}
		}
		byNew[r.New] = r
	}
	// missing 与 targetExists 按旧名字典序检查，保证错误确定性。
	olds := make([]string, 0, len(p.reqs))
	for _, r := range p.reqs {
		olds = append(olds, r.Old)
	}
	sort.Strings(olds)
	for _, o := range olds {
		if !p.get(exSet, o) {
			return &Conflict{Kind: ErrMissingSource, A: o}
		}
	r := byOld[o]
		if r.Old == r.New {
			continue
		}
		if p.get(blocked, r.New) {
			return &Conflict{Kind: ErrTargetExists, A: r.New}
		}
	}
	return nil
}

// Order 返回安全执行顺序（拓扑序，同层按旧名字典序）与请求中的环（边集合）。
// 自环不产生步骤。
func (p *Planner) Order() (seq []Req, cycles [][]Req) {
	indeg := make(map[string]int)
	adj := make(map[string][]string)
	nodes := make(map[string]Req)
	for _, r := range p.reqs {
		if r.Old == r.New {
			continue
		}
		nodes[r.Old] = r
		if _, ok := indeg[r.Old]; !ok {
			indeg[r.Old] = 0
		}
		if _, ok := indeg[r.New]; !ok {
			indeg[r.New] = 0
		}
	}
	for _, r := range nodes {
		// 依赖：r 的新名若也是某请求的旧名，那条请求必须先执行。边 new->old。
		adj[r.New] = append(adj[r.New], r.Old)
		indeg[r.Old]++
	}
	ready := []string{}
	for n, d := range indeg {
		if d == 0 {
			ready = append(ready, n)
		}
	}
	done := make(map[string]bool)
	for len(ready) > 0 {
		sort.Strings(ready)
		n := ready[0]
		ready = ready[1:]
		if r, ok := nodes[n]; ok {
			seq = append(seq, r)
		}
		done[n] = true
		next := append([]string{}, adj[n]...)
		sort.Strings(next)
		for _, m := range next {
			indeg[m]--
			if indeg[m] == 0 {
				ready = append(ready, m)
			}
		}
	}
	// 未完成节点即处于环中；沿 nodes 边（old->new）枚举互不相交的环。
	rem := make(map[string]bool)
	for n := range nodes {
		if !done[n] {
			rem[n] = true
		}
	}
	for len(rem) > 0 {
		starts := []string{}
		for n := range rem {
			starts = append(starts, n)
		}
		sort.Strings(starts)
		s := starts[0]
		var cyc []Req
		cur := s
		for rem[cur] {
			rem[cur] = false
			delete(rem, cur)
			r := nodes[cur]
			cyc = append(cyc, r)
			cur = r.New
		}
		cycles = append(cycles, cyc)
	}
	return seq, cycles
}
