// Package plan 在不修改命名空间的前提下检测冲突并计算安全执行顺序。
package plan

import (
	"container/heap"
	"errors"
	"sort"

	"ontology/name"
)

// Req 是一条重命名请求。
type Req struct{ Old, New string }

var (
	// ErrTargetExists：新名已存在且不在本批被搬走。
	ErrTargetExists = errors.New("plan: target already exists")
	// ErrDuplicateTarget：两个请求指向同一新名。
	ErrDuplicateTarget = errors.New("plan: duplicate target")
	// ErrDuplicateSource：同一旧名出现两次。
	ErrDuplicateSource = errors.New("plan: duplicate source")
	// ErrMissingSource：旧名不存在。
	ErrMissingSource = errors.New("plan: source missing")
)

// Conflict 描述一次冲突，Unwrap 到四类哨兵错误之一。
type Conflict struct {
	Kind     error
	Name     string // 冲突名字（旧名/新名）
	OtherOld string // 重复目标：另一个旧名
	OtherNew string // 重复旧名：另一个新名
}

func (c *Conflict) Error() string {
	switch {
	case errors.Is(c.Kind, ErrTargetExists):
		return "target exists and is not moved: " + c.Name
	case errors.Is(c.Kind, ErrDuplicateTarget):
		return "duplicate target " + c.Name + " <- " + c.OtherOld
	case errors.Is(c.Kind, ErrDuplicateSource):
		return "duplicate source " + c.Name + " -> " + c.OtherNew
	default:
		return "missing source: " + c.Name
	}
}
func (c *Conflict) Unwrap() error { return c.Kind }

// counters 记录检测+排序期间的哈希查找次数（非导出，测试同包可读）。
type counters struct{ lookups int }

// 编译期保证错误分类可用。
var _ = []error{ErrTargetExists, ErrDuplicateTarget, ErrDuplicateSource, ErrMissingSource}

func (c *counters) in(ns *name.Namespace, n string) bool {
	c.lookups++
	return ns.HasLocked(n)
}

// detect 做四类冲突检测，返回第一个冲突（若有）。全程只读命名空间。
// reqs 中的自环在排序前已被剔除（见 Build），故此处无自环。
func detect(ns *name.Namespace, reqs []Req, c *counters) *Conflict {
	source := make(map[string]string, len(reqs)) // old -> new
	target := make(map[string]string, len(reqs)) // new -> old
	for _, r := range reqs {
		if !c.in(ns, r.Old) {
			return &Conflict{Kind: ErrMissingSource, Name: r.Old}
		}
		if other, ok := source[r.Old]; ok {
			c.lookups++
			return &Conflict{Kind: ErrDuplicateSource, Name: r.Old, OtherNew: other}
		}
		source[r.Old] = r.New
		if other, ok := target[r.New]; ok {
			c.lookups++
			return &Conflict{Kind: ErrDuplicateTarget, Name: r.New, OtherOld: other}
		}
		target[r.New] = r.Old
	}
	for _, r := range reqs {
		if !c.in(ns, r.New) {
			continue // 新名原本不存在，安全
		}
		if _, moved := source[r.New]; moved {
			c.lookups++
			continue // 新名会在本批被搬走
		}
		return &Conflict{Kind: ErrTargetExists, Name: r.New}
	}
	return nil
}

// Plan 是检测通过后的编排结果。
type Plan struct {
	Reqs      []Req   // 全部非自环请求
	Order     []Req   // 安全拓扑序（环上的边不在其中，见 Cycles）
	Cycles    [][]Req // 每个有向环，按边方向 x0->x1,...,xk->x0
	SelfLoops []Req   // 自环（无操作，不执行不记录）
	lookups   int     // 检测+排序的哈希查找总次数
}

// Lookups 返回检测与排序期间的哈希查找次数（复杂度测试用）。
func (p *Plan) Lookups() int { return p.lookups }

// edgeHeap 按 old 字典序取最小，保证同层确定性。
type edgeHeap []Req

func (h edgeHeap) Len() int           { return len(h) }
func (h edgeHeap) Less(i, j int) bool { return h[i].Old < h[j].Old }
func (h edgeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *edgeHeap) Push(x any)        { *h = append(*h, x.(Req)) }
func (h *edgeHeap) Pop() any {
	old := *h
	r := old[len(old)-1]
	*h = old[:len(old)-1]
	return r
}

// Build 先做冲突检测（任何修改之前），再计算拓扑序与环。
func Build(ns *name.Namespace, reqs []Req) (*Plan, error) {
	p := &Plan{}
	c := &counters{}

	var live []Req
	for _, r := range reqs {
		if r.Old == r.New {
			p.SelfLoops = append(p.SelfLoops, r)
			continue
		}
		live = append(live, r)
	}
	p.Reqs = append(p.Reqs, live...)

	if cf := detect(ns, live, c); cf != nil {
		p.lookups = c.lookups
		return nil, cf
	}

	src := make(map[string]string, len(live))
	byTarget := make(map[string]string, len(live))
	remaining := make(map[string]Req, len(live))
	for _, r := range live {
		src[r.Old] = r.New
		byTarget[r.New] = r.Old
		remaining[r.Old] = r
	}

	dep := make(map[string]int, len(live))
	h := &edgeHeap{}
	for _, r := range live {
		if _, ok := src[r.New]; ok {
			dep[r.Old] = 1
		} else {
			heap.Push(h, r)
		}
	}

	for h.Len() > 0 {
		r := heap.Pop(h).(Req)
		p.Order = append(p.Order, r)
		delete(remaining, r.Old)
		pred, ok := byTarget[r.Old]
		if !ok {
			continue
		}
		dep[pred]--
		if dep[pred] == 0 {
			heap.Push(h, Req{Old: pred, New: src[pred]})
		}
	}

	for len(remaining) > 0 {
		starts := make([]string, 0, len(remaining))
		for o := range remaining {
			starts = append(starts, o)
		}
		sort.Strings(starts)
		x0 := starts[0]
		var cyc []Req
		cur := x0
		for {
			r := remaining[cur]
			cyc = append(cyc, r)
			delete(remaining, cur)
			cur = r.New
			if cur == x0 {
				break
			}
		}
		p.Cycles = append(p.Cycles, cyc)
	}

	p.lookups = c.lookups
	return p, nil
}
