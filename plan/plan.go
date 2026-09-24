// Package plan 检测批量重命名冲突并编排安全的执行顺序。
//
// 排序规则：请求 r 依赖于请求 s 当且仅当 r.New == s.Old（s 必须先
// 把名字腾出来）。无环部分用 Kahn 拓扑序，同层按 old 字典序出队；
// 剩余部分必为互不相交的简单环，交给 cycle.Break 逐环破解。
package plan

import (
	"container/heap"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"ontology/cycle"
	"ontology/name"
)

// 四类冲突的哨兵错误，可用 errors.Is 区分。
var (
	ErrTargetExists    = errors.New("plan: target exists and is not moved away")
	ErrDuplicateTarget = errors.New("plan: two requests share one new name")
	ErrDuplicateSource = errors.New("plan: old name requested twice")
	ErrSourceMissing   = errors.New("plan: old name does not exist")
)

// Plan 是编排结果：Steps 为按序执行的步骤，Temps 为引入的临时名数量。
type Plan struct {
	Steps []name.Rename
	Temps int
}

var lookups atomic.Int64 // 非导出计数器：命名空间查询 + map 查询次数

// Lookups 返回累计查找次数（测试用）。
func Lookups() int64 { return lookups.Load() }

// ResetLookups 清零查找计数器（测试用）。
func ResetLookups() { lookups.Store(0) }

func note() { lookups.Add(1) }

// Build 检测冲突并编排执行顺序。对命名空间只读：任何冲突都在
// 修改之前被发现并拒绝整批。自环 a->a 是合法的无操作，不产生步骤。
func Build(ns *name.Namespace, reqs []name.Rename) (*Plan, error) {
	byOld := make(map[string]name.Rename, len(reqs))
	byNew := make(map[string]string, len(reqs))
	for _, r := range reqs {
		note()
		if prev, dup := byOld[r.Old]; dup {
			return nil, fmt.Errorf("%w: %q -> %q and %q", ErrDuplicateSource, r.Old, prev.New, r.New)
		}
		byOld[r.Old] = r
		note()
		if prevOld, dup := byNew[r.New]; dup {
			return nil, fmt.Errorf("%w: %q and %q -> %q", ErrDuplicateTarget, prevOld, r.Old, r.New)
		}
		byNew[r.New] = r.Old
	}
	var work []name.Rename
	for _, r := range reqs {
		note()
		if !ns.Has(r.Old) {
			return nil, fmt.Errorf("%w: %q", ErrSourceMissing, r.Old)
		}
		if r.Old == r.New {
			continue // 自环：合法无操作
		}
		note()
		if ns.Has(r.New) {
			note()
			if _, moved := byOld[r.New]; !moved {
				return nil, fmt.Errorf("%w: %q", ErrTargetExists, r.New)
			}
		}
		work = append(work, r)
	}
	p := &Plan{}
	rest := kahn(work, byOld, byNew, p)
	breakCycles(rest, byOld, byNew, ns, p)
	return p, nil
}

// kahn 对无环部分做拓扑排序，步骤追加进 p.Steps，
// 返回构成环的剩余请求（按 old 字典序，保证确定性）。
func kahn(work []name.Rename, byOld map[string]name.Rename, byNew map[string]string, p *Plan) []name.Rename {
	left := make(map[string]name.Rename, len(work))
	ready := &strHeap{}
	for _, r := range work {
		left[r.Old] = r
		note()
		if _, blocked := byOld[r.New]; !blocked {
			heap.Push(ready, r.Old)
		}
	}
	for ready.Len() > 0 {
		old := heap.Pop(ready).(string)
		note()
		r := left[old]
		note()
		delete(left, old)
		p.Steps = append(p.Steps, r)
		note()
		if u, ok := byNew[r.Old]; ok {
			note()
			if _, still := left[u]; still {
				heap.Push(ready, u)
			}
		}
	}
	rest := make([]name.Rename, 0, len(left))
	for _, r := range left {
		rest = append(rest, r)
	}
	slices.SortFunc(rest, func(a, b name.Rename) int { return strings.Compare(a.Old, b.Old) })
	return rest
}

// breakCycles 把剩余请求按环分组（从环上字典序最小 old 起走），逐环破解。
func breakCycles(rest []name.Rename, byOld map[string]name.Rename, byNew map[string]string, ns *name.Namespace, p *Plan) {
	temps := map[string]bool{}
	used := func(s string) bool {
		note()
		if ns.Has(s) {
			return true
		}
		note()
		if _, ok := byOld[s]; ok {
			return true
		}
		note()
		if _, ok := byNew[s]; ok {
			return true
		}
		note()
		return temps[s]
	}
	seen := map[string]bool{}
	for _, r := range rest {
		note()
		if seen[r.Old] {
			continue
		}
		var cyc []name.Rename
		for cur := r.Old; ; {
			note()
			seen[cur] = true
			note()
			cr := byOld[cur]
			cyc = append(cyc, cr)
			if cur = cr.New; cur == r.Old {
				break
			}
		}
		steps, tmp := cycle.Break(cyc, used)
		temps[tmp] = true
		p.Steps = append(p.Steps, steps...)
		p.Temps++
	}
}

// strHeap 是字典序小根堆，保证同层步骤的确定顺序。
type strHeap []string

func (h strHeap) Len() int           { return len(h) }
func (h strHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h strHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *strHeap) Push(x any)        { *h = append(*h, x.(string)) }

func (h *strHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
