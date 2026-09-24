// Package plan 在不修改命名空间的前提下完成冲突检测、确定性拓扑排序与环识别。
package plan

import (
	"errors"
	"fmt"
	"sort"
)

// 四类冲突，均可用 errors.Is 判定。
var (
	ErrOldMissing   = errors.New("old name does not exist")
	ErrOldDup       = errors.New("duplicate old name")
	ErrNewDup       = errors.New("two requests target the same new name")
	ErrTargetExists = errors.New("target name exists and is not moved in this batch")
)

// Req 是一条重命名请求。
type Req struct{ Old, New string }

// Step 是一个有序执行步骤。
type Step struct{ Old, New string }

// Cycle 描述一个尚未破环的有向环，Nodes 按依赖顺序排列：
// 执行 Nodes[0]->Nodes[1]->...->Nodes[k-1]->Nodes[0]。
type Cycle struct{ Nodes []string }

// Conflict 携带冲突细节；Err 是四个哨兵之一。
type Conflict struct {
	Err    error
	Name   string
	Other  string
	Detail string
}

func (c *Conflict) Error() string {
	return fmt.Sprintf("%s: %s", c.Err, c.Detail)
}
func (c *Conflict) Unwrap() error { return c.Err }

// Plan 是非环步骤（拓扑序）与环的集合。步骤之间不包含环内节点。
type Plan struct {
	Steps  []Step
	Cycles []Cycle
	lookups int
}

// analyzer 用哈希表完成全部检测与排序；lookups 记录哈希查找次数。
type analyzer struct {
	exist   map[string]struct{}
	oldReq  map[string]Req
	newOwn  map[string]string
	lookups int
}

func (a *analyzer) has(m map[string]struct{}, k string) bool {
	a.lookups++
	_, ok := m[k]
	return ok
}

func (a *analyzer) getReq(m map[string]Req, k string) (Req, bool) {
	a.lookups++
	v, ok := m[k]
	return v, ok
}

func (a *analyzer) getStr(m map[string]string, k string) (string, bool) {
	a.lookups++
	v, ok := m[k]
	return v, ok
}

// Lookups 返回检测与排序期间的哈希查找次数（非导出计数器的读取口）。
func (p *Plan) Lookups() int { return p.lookups }

// Build 对请求做全量冲突检测并产出拓扑序计划；任何冲突都在修改前返回。
func Build(existing map[string]struct{}, reqs []Req) (*Plan, error) {
	a := &analyzer{
		exist:  existing,
		oldReq: make(map[string]Req, len(reqs)),
		newOwn: make(map[string]string, len(reqs)),
	}
	if err := a.detect(reqs); err != nil {
		return nil, err
	}
	return a.order(), nil
}

func (a *analyzer) detect(reqs []Req) error {
	for _, r := range reqs {
		if !a.has(a.exist, r.Old) {
			return &Conflict{Err: ErrOldMissing, Name: r.Old,
				Detail: "old name missing: " + r.Old}
		}
		if prev, dup := a.getReq(a.oldReq, r.Old); dup {
			return &Conflict{Err: ErrOldDup, Name: r.New, Other: prev.New,
				Detail: fmt.Sprintf("old %s -> %s and %s", r.Old, prev.New, r.New)}
		}
		a.oldReq[r.Old] = r
		if owner, used := a.getStr(a.newOwn, r.New); used && owner != r.Old {
			return &Conflict{Err: ErrNewDup, Name: owner, Other: r.Old,
				Detail: fmt.Sprintf("new %s <- %s and %s", r.New, owner, r.Old)}
		}
		a.newOwn[r.New] = r.Old
	}
	for _, r := range reqs {
		if r.Old == r.New {
			continue // 自环：无操作，不做目标占用判定
		}
		_, isOld := a.oldReq[r.New]
		if a.has(a.exist, r.New) && !isOld {
			return &Conflict{Err: ErrTargetExists, Name: r.New,
				Detail: "target exists and is not moved: " + r.New}
		}
	}
	return nil
}

func (a *analyzer) order() *Plan {
	p := &Plan{}
	// 非自环请求建图：old -> new；blocker[old]=new（当 new 也是某旧名）。
	blocker := make(map[string]string, len(a.oldReq))
	waiting := make(map[string][]string)
	ready := make([]string, 0)
	for old, r := range a.oldReq {
		if old == r.New {
			continue
		}
		if _, isOld := a.getReq(a.oldReq, r.New); isOld {
			blocker[old] = r.New
			waiting[r.New] = append(waiting[r.New], old)
		} else {
			ready = append(ready, old)
		}
	}
	sort.Strings(ready)
	done := make(map[string]struct{}, len(a.oldReq))
	for len(ready) > 0 {
		old := ready[0]
		ready = ready[1:]
		if _, seen := done[old]; seen {
			continue
		}
		done[old] = struct{}{}
		r := a.oldReq[old]
		p.Steps = append(p.Steps, Step{Old: old, New: r.New})
		wake := waiting[old]
		sort.Strings(wake)
		for _, w := range wake {
			if blocker[w] == old {
				delete(blocker, w)
				ready = append(ready, w)
			}
		}
	}
	p.Cycles = a.cycles(blocker)
	p.lookups = a.lookups
	return p

}

func (a *analyzer) cycles(blocker map[string]string) []Cycle {
	var out []Cycle
	seen := make(map[string]struct{})
	var ring []string
	for old := range blocker {
		if _, ok := seen[old]; ok {
			continue
		}
		path := map[string]int{}
		cur := old
		ring = ring[:0]
		for {
			if _, visited := seen[cur]; visited {
				break
			}
			if idx, repeat := path[cur]; repeat {
				nodes := append([]string(nil), ring[idx:]...)
				minIdx := 0
				for i := 1; i < len(nodes); i++ {
					if nodes[i] < nodes[minIdx] {
						minIdx = i
					}
				}
				rot := append([]string(nil), nodes[minIdx:]...)
				rot = append(rot, nodes[:minIdx]...)
				out = append(out, Cycle{Nodes: rot})
				break
			}
			path[cur] = len(ring)
			ring = append(ring, cur)
			nxt, ok := blocker[cur]
			if !ok {
				break
			}
			cur = nxt
		}
		for _, n := range ring {
			seen[n] = struct{}{}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nodes[0] < out[j].Nodes[0] })
	return out
}
