// Package query 在 sam.Automaton 上实现三类查询：
// 不同子串数、子串出现次数、最长公共子串。
package query

import "ontology/sam"

// Queries 持有构建好的 SAM 与预计算的右端位置集合大小。
type Queries struct {
	a   *sam.Automaton
	cnt []int // cnt[v] = 状态 v 的右端位置集合大小
}

// New 预计算每个状态的右端位置集合大小：
// 对应某个前缀的状态记 1，按 len 降序沿后缀链向父累加。
func New(a *sam.Automaton) *Queries {
	n, maxLen := 0, 0
	a.Each(func(v int) {
		n++
		if l := a.Len(v); l > maxLen {
			maxLen = l
		}
	})
	bucket := make([]int, maxLen+1)
	a.Each(func(v int) { bucket[a.Len(v)]++ })
	for i := 1; i <= maxLen; i++ {
		bucket[i] += bucket[i-1]
	}
	order := make([]int, n) // 计数排序：状态按 len 升序
	a.Each(func(v int) {
		l := a.Len(v)
		bucket[l]--
		order[bucket[l]] = v
	})
	cnt := make([]int, n)
	a.Each(func(v int) {
		if a.Terminal(v) {
			cnt[v] = 1
		}
	})
	for i := n - 1; i > 0; i-- { // order[0] 必为根，跳过
		v := order[i]
		cnt[a.Link(v)] += cnt[v]
	}
	return &Queries{a: a, cnt: cnt}
}

// Distinct 返回不同子串总数：每个非根状态贡献 len[v]-len[link[v]]。
func (q *Queries) Distinct() int {
	sum := 0
	q.a.Each(func(v int) {
		if v != q.a.Root() {
			sum += q.a.Len(v) - q.a.Len(q.a.Link(v))
		}
	})
	return sum
}

// Occurrences 返回 sub 在构建串中的出现次数；sub 不存在时返回 0。
func (q *Queries) Occurrences(sub string) int {
	v := q.a.Root()
	for i := 0; i < len(sub); i++ {
		to, ok := q.a.Next(v, sub[i])
		if !ok {
			return 0
		}
		v = to
	}
	return q.cnt[v]
}

// LongestCommonSubstring 返回构建串与 t 的最长公共子串长度及其在 t 中的
// 右端点（不含）。当前状态无转移时沿 link 回退并把长度降为 len[link[v]]，
// 回退到根仍无转移则长度归 0。
func (q *Queries) LongestCommonSubstring(t string) (length, tEnd int) {
	v, l := q.a.Root(), 0
	for i := 0; i < len(t); i++ {
		c := t[i]
		for {
			if to, ok := q.a.Next(v, c); ok {
				v, l = to, l+1
				break
			}
			if v == q.a.Root() {
				l = 0
				break
			}
			v = q.a.Link(v)
			l = q.a.Len(v)
		}
		if l > length {
			length, tEnd = l, i+1
		}
	}
	return length, tEnd
}
