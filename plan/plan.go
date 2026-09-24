// Package plan 负责批量重命名的冲突检测与执行顺序编排。
package plan

import (
	"errors"
	"sort"
)

var (
	ErrSrcMissing = errors.New("plan: source name does not exist")
	ErrDestExists = errors.New("plan: destination exists and is not moved away")
	ErrDupDest    = errors.New("plan: two requests share a destination")
	ErrDupSrc     = errors.New("plan: one source has two destinations")
)

// Req 是一条重命名请求。
type Req struct{ From, To string }

// Step 是执行计划中的一个原子移动。
type Step struct{ From, To string }

// Cycle 是一个有向请求环（按依赖顺序排列）。
type Cycle struct{ Reqs []Req }

// Conflict 描述一个可判定的冲突错误。
type Conflict struct {
	Kind  error
	Name  string // 涉及的名字
	Other string // 配对的另一个旧名/新名
}

func (c *Conflict) Error() string { return "plan conflict: " + c.Kind.Error() }
func (c *Conflict) Unwrap() error { return c.Kind }

// Plan 是编译结果：链步骤（已拓扑排序）与环。
type Plan struct {
	Chains []Step
	Cycles []Cycle

	lookups int
}

// Lookups 返回检测与排序期间的 map 查找次数。
func (p *Plan) Lookups() int { return p.lookups }

type reqMap map[string]Req

func (m reqMap) get(p *Plan, k string) (Req, bool) {
	p.lookups++
	r, ok := m[k]
	return r, ok
}

type strSet map[string]struct{}

func (s strSet) has(p *Plan, k string) bool {
	p.lookups++
	_, ok := s[k]
	return ok
}

// Compile 在不修改命名空间的前提下检测冲突并分解出链与环。
// existing 必须是命名空间的快照。
func Compile(reqs []Req, existing []string) (*Plan, error) {
	p := &Plan{}
	bySrc := reqMap{}
	byDst := map[string]string{}
	srcSet := strSet{}
	exist := strSet{}
	for _, n := range existing {
		exist[n] = struct{}{}
	}

	for _, r := range reqs {
		if _, ok := bySrc.get(p, r.From); ok {
			return nil, &Conflict{Kind: ErrDupSrc, Name: r.From, Other: r.To}
		}
		bySrc[r.From] = r
		srcSet[r.From] = struct{}{}
		if prev, ok := byDst[r.To]; ok {
			return nil, &Conflict{Kind: ErrDupDest, Name: r.From, Other: prev}
		}
		byDst[r.To] = r.From
	}

	for _, r := range reqs {
		if r.From != r.To && !exist.has(p, r.From) {
			return nil, &Conflict{Kind: ErrSrcMissing, Name: r.From}
		}
		if r.From == r.To {
			continue
		}
		if exist.has(p, r.To) && !srcSet.has(p, r.To) {
			return nil, &Conflict{Kind: ErrDestExists, Name: r.To}
		}
	}

	// 图：from→Req；边为 r(To==r'.From) => r' 先于 r。
	// indeg[r']=1 当存在 r 使 r.To==r'.From（r' 是别人的前置）。
	indeg := map[string]int{}
	srcs := make([]string, 0, len(bySrc))
	for s := range bySrc {
		srcs = append(srcs, s)
	}
	for _, r := range bySrc {
		if r.From == r.To {
			continue
		}
		if _, ok := bySrc.get(p, r.To); ok && r.To != r.From {
			indeg[r.From]++
		}
	}

	var zero []string
	for _, s := range srcs {
		r := bySrc[s]
		if r.From != r.To && indeg[s] == 0 {
			zero = append(zero, s)
		}
	}
	sort.Strings(zero)
	visited := map[string]bool{}
	var chains []Step
	queue := append([]string{}, zero...)
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if visited[s] {
			continue
		}
		visited[s] = true
		r := bySrc[s]
		chains = append(chains, Step{r.From, r.To})
		// 释放后继：寻找 r.From == r.To 的请求 x（x 的目标是 s）。
		// 该后继即 byDst[s] 对应的请求源。
		if pred, ok := byDst[s]; ok && pred != s {
			pr := bySrc[pred]
			if pr.From != pr.To {
				indeg[pr.From]--
				if indeg[pr.From] == 0 {
					queue = insertSorted(queue, pr.From)
				}
			}
		}
	}

	// 未访问的非自环节点组成环，每个连通环收集一次。
	var cycSrcs []string
	for _, s := range srcs {
		r := bySrc[s]
		if r.From != r.To && !visited[s] {
			cycSrcs = append(cycSrcs, s)
		}
	}
	sort.Strings(cycSrcs)
	seenCycle := map[string]bool{}
	for _, s := range cycSrcs {
		if seenCycle[s] {
			continue
		}
		var cyc []Req
		cur := s
		for {
			seenCycle[cur] = true
			r := bySrc[cur]
			cyc = append(cyc, r)
			cur = r.To
			if cur == s {
				break
			}
		}
		p.Cycles = append(p.Cycles, Cycle{Reqs: cyc})
	}

	p.Chains = chains
	return p, nil
}

func insertSorted(q []string, v string) []string {
	i := sort.SearchStrings(q, v)
	q = append(q, "")
	copy(q[i+1:], q[i:])
	q[i] = v
	return q
}
