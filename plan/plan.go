// Package plan 负责批量重命名的冲突检测与确定性执行顺序编排。
package plan

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrTargetExists 目标名已存在且不在本批被搬走。
	ErrTargetExists = errors.New("plan: target already exists and is not moved")
	// ErrDupTarget 两个请求指向同一新名。
	ErrDupTarget = errors.New("plan: duplicate target name")
	// ErrDupSource 同一旧名出现两次。
	ErrDupSource = errors.New("plan: duplicate source name")
	// ErrMissingSource 旧名不存在。
	ErrMissingSource = errors.New("plan: source name missing")
)

// Req 是一条重命名请求。
type Req struct {
	Old string
	New string
}

// Step 是排序后的一个执行步骤。
type Step struct {
	Old string
	New string
}

// Conflict 携带可判定的冲突细节（可用 errors.Is 判断类别）。
type Conflict struct {
	Kind error
	Name string
	A, B string
}

func (c *Conflict) Error() string {
	switch c.Kind {
	case ErrTargetExists:
		return fmt.Sprintf("plan: target %q already exists and is not moved", c.Name)
	case ErrDupTarget:
		return fmt.Sprintf("plan: duplicate target %q from %q and %q", c.Name, c.A, c.B)
	case ErrDupSource:
		return fmt.Sprintf("plan: duplicate source %q to %q and %q", c.Name, c.A, c.B)
	default:
		return fmt.Sprintf("plan: missing source %q", c.Name)
	}
}
func (c *Conflict) Unwrap() error { return c.Kind }

// Plan 是分析结果：非自环请求的执行顺序（不含环，环交 cycle 包处理），
// 以及排序后待破环的纯环（每个环为其成员旧名）。
type Plan struct {
	Steps  []Step
	Cycles [][]string
	// lookups 统计 map 查找次数，用于线性复杂度断言。
	lookups int
}

type analyzer struct {
	existing map[string]struct{}
	next     map[string]string
	pred     map[string]string
	lookups  int
}

func (a *analyzer) has(m map[string]struct{}, k string) bool {
	a.lookups++
	_, ok := m[k]
	return ok
}

func (a *analyzer) getStr(m map[string]string, k string) (string, bool) {
	a.lookups++
	v, ok := m[k]
	return v, ok
}

// Analyze 仅基于初始快照做检测，绝不修改任何数据；有冲突时返回 Conflict。
func Analyze(snapshot []string, reqs []Req) (*Plan, error) {
	a := &analyzer{existing: make(map[string]struct{}, len(snapshot))}
	for _, n := range snapshot {
		a.existing[n] = struct{}{}
	}
	srcNew := make(map[string]string, len(reqs))
	tgtSrc := make(map[string]string, len(reqs))
	type edge struct{ src, dst string }
	var edges []edge

	for _, r := range reqs {
		if r.Old == r.New {
			continue // 自环：无操作
		}
		a.lookups++
		if prev, ok := srcNew[r.Old]; ok {
			return nil, &Conflict{Kind: ErrDupSource, Name: r.Old, A: prev, B: r.New}
		}
		a.lookups++
		if prev, ok := tgtSrc[r.New]; ok {
			return nil, &Conflict{Kind: ErrDupTarget, Name: r.New, A: prev, B: r.Old}
		}
		if !a.has(a.existing, r.Old) {
			return nil, &Conflict{Kind: ErrMissingSource, Name: r.Old}
		}
		srcNew[r.Old] = r.New
		tgtSrc[r.New] = r.Old
		edges = append(edges, edge{r.Old, r.New})
	}
	// 第二遍：目标已存在且不会被本批搬走 → 冲突。
	for _, e := range edges {
		a.lookups++
		_, movedAsSrc := srcNew[e.dst]
		if !movedAsSrc {
			if a.has(a.existing, e.dst) {
				return nil, &Conflict{Kind: ErrTargetExists, Name: e.dst}
			}
		}
	}

	// next[x] = x 的新名（仅出边）；pred[y] = 指向 y 的旧名。
	next := make(map[string]string, len(edges))
	pred := make(map[string]string, len(edges))
	for _, e := range edges {
		next[e.src] = e.dst
		pred[e.dst] = e.src
	}
	a.next, a.pred = next, pred

	// 识别纯环：每个顶点沿出边行走直到重复；重复点即所在环。
	seen := make(map[string]bool, len(next))
	onCycle := make(map[string]bool, len(next))
	var cycles [][]string
	for start := range next {
		if seen[start] {
			continue
		}
		path := []string{}
		idx := make(map[string]int)
		cur := start
		terminated := false
		for !seen[cur] {
			seen[cur] = true
			idx[cur] = len(path)
			path = append(path, cur)
			nxt, ok := a.getStr(next, cur)
			if !ok {
				terminated = true
				break
			}
			cur = nxt
		}
		if at, ok := idx[cur]; ok && !terminated { // 真正回到本次路径内 → 成环
			cyc := append([]string(nil), path[at:]...)
			anchor := minMember(cyc)
			rot := rotateTo(cyc, anchor)
			cycles = append(cycles, rot)
			for _, v := range rot {
				onCycle[v] = true
			}
		}
	}
	sort.Slice(cycles, func(i, j int) bool { return cycles[i][0] < cycles[j][0] })

	// 链（非环节点）：从汇点（其新名不是任何请求旧名）开始，沿前驱展开。
	var frontier []string
	for src := range next {
		if onCycle[src] {
			continue
		}
		dst, _ := a.getStr(next, src)
		if _, isSrc := a.getStr(next, dst); !isSrc {
			frontier = append(frontier, src)
		}
	}
	sort.Strings(frontier)
	var steps []Step
	for len(frontier) > 0 {
		src := frontier[0]
		frontier = frontier[1:]
		steps = append(steps, Step{Old: src, New: next[src]})
		if p, ok := a.getStr(pred, src); ok && !onCycle[p] {
			frontier = append(frontier, p)
			sort.Strings(frontier)
		}
	}
	return &Plan{Steps: steps, Cycles: cycles, lookups: a.lookups}, nil
}

// Lookups 返回检测期间的 map 查找次数。
func (p *Plan) Lookups() int { return p.lookups }

func minMember(cyc []string) string {
	m := cyc[0]
	for _, v := range cyc[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// rotateTo 把环旋转到以 anchor 开头，保持原循环方向。
func rotateTo(cyc []string, anchor string) []string {
	for i, v := range cyc {
		if v == anchor {
			return append(append([]string(nil), cyc[i:]...), cyc[:i]...)
		}
	}
	return cyc
}
