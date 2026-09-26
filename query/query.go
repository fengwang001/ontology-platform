// Package query 在 pal.Tree 上实现查询：Distinct、Total、Count、Longest。
package query

import (
	"sort"

	"ontology/pal"
)

// Querier 持有构建期计数经 link 传播后的出现次数，构造后只读。
type Querier struct {
	t   *pal.Tree
	occ []int // 各节点出现次数（含 link 传播），下标与 pal 节点一致
}

// New 在已建好的树上预计算传播后的出现次数：
// 从长到短把每个节点的命中数累加进它的 link（最长真回文后缀）。
func New(t *pal.Tree) *Querier {
	n := t.Distinct() + 2
	occ := make([]int, n)
	order := make([]int, 0, n-2)
	for i := 0; i < n; i++ {
		occ[i] = t.Cnt(i)
		if i >= 2 {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(a, b int) bool { return t.Len(order[a]) > t.Len(order[b]) })
	for _, v := range order { // link 严格更短，故被累加者尚未处理
		occ[t.Link(v)] += occ[v]
	}
	return &Querier{t: t, occ: occ}
}

// Distinct 返回不同回文子串个数。
func (q *Querier) Distinct() int { return q.t.Distinct() }

// Total 返回全部回文子串总个数（含重复）= 各节点出现次数之和。
func (q *Querier) Total() int {
	sum := 0
	for i := 2; i < len(q.occ); i++ {
		sum += q.occ[i]
	}
	return sum
}

// Count 返回回文 sub 在 s 中的出现次数；sub 不存在时返回 0。
// 调用方保证 sub 是回文。
func (q *Querier) Count(sub string) int {
	idx, ok := q.t.Find(sub)
	if !ok {
		return 0
	}
	return q.occ[idx]
}

// Longest 返回最长回文子串；并列最长时取构建中出现最早者
// （节点按下标即构建顺序，严格大于才更新）。
func (q *Querier) Longest() string {
	best := 1 // 偶根，len 0
	for i := 2; i < len(q.occ); i++ {
		if q.t.Len(i) > q.t.Len(best) {
			best = i
		}
	}
	return q.t.Text(best)
}

// CheckLinks 复核 link 树结构。
func (q *Querier) CheckLinks() bool { return q.t.CheckLinks() }
