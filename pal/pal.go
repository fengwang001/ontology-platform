// Package pal 实现回文树（eertree）：两个根、next 转移、link 后缀链、
// len、cnt（出现次数）与逐字符 add 构建。不依赖其他包。
package pal

import "sort"

// MaxLen 是允许构建的最大串长。
const MaxLen = 1 << 20

type node struct {
	next   map[byte]int
	link   int // 最长真回文后缀节点
	length int // 该节点代表的回文长度
	cnt    int // 出现次数（构建时命中 +1，建完沿 link 传播）
	end    int // 该回文首次出现时的右端点下标
}

// Tree 是回文树。节点 0 是奇根（len -1），节点 1 是偶根（len 0）。
type Tree struct {
	s         []byte
	nodes     []node
	last      int
	nodeTotal int // 非导出计数器：构建完成后的节点总数
}

// New 以 s 构建回文树并完成 cnt 沿 link 的从长到短传播。
func New(s string) *Tree {
	t := &Tree{s: []byte(s), last: 0}
	t.nodes = append(t.nodes,
		node{next: map[byte]int{}, link: 0, length: -1}, // 奇根
		node{next: map[byte]int{}, link: 0, length: 0},  // 偶根
	)
	for i := range t.s {
		t.add(i)
	}
	// 从长到短沿 link 累加传播出现次数。
	ord := make([]int, len(t.nodes))
	for i := range ord {
		ord[i] = i
	}
	sort.Slice(ord, func(a, b int) bool { return t.nodes[ord[a]].length > t.nodes[ord[b]].length })
	for _, v := range ord {
		if v >= 2 {
			t.nodes[t.nodes[v].link].cnt += t.nodes[v].cnt
		}
	}
	t.nodeTotal = len(t.nodes)
	return t
}

// add 把 t.s[i] 追加进树，命中（新建或复用）的节点 cnt+1。
func (t *Tree) add(i int) {
	cur := t.last
	for !t.match(cur, i) {
		cur = t.nodes[cur].link
	}
	c := t.s[i]
	if nxt, ok := t.nodes[cur].next[c]; ok {
		t.last = nxt
		t.nodes[nxt].cnt++
		return
	}
	nw := len(t.nodes)
	t.nodes = append(t.nodes, node{
		next:   map[byte]int{},
		length: t.nodes[cur].length + 2,
		cnt:    1,
		end:    i,
	})
	t.nodes[cur].next[c] = nw
	if t.nodes[nw].length == 1 {
		t.nodes[nw].link = 1 // 长度为 1 的节点 link 指向偶根
	} else {
		v := t.nodes[cur].link
		for !t.match(v, i) {
			v = t.nodes[v].link
		}
		t.nodes[nw].link = t.nodes[v].next[c]
	}
	t.last = nw
}

// match 报告节点 v 能否向两侧各扩一个 t.s[i]。
func (t *Tree) match(v, i int) bool {
	j := i - t.nodes[v].length - 1
	return j >= 0 && t.s[j] == t.s[i]
}

// NumNodes 返回含两个根的节点总数。
func (t *Tree) NumNodes() int { return len(t.nodes) }

// Len 返回节点 v 代表的回文长度。
func (t *Tree) Len(v int) int { return t.nodes[v].length }

// Link 返回节点 v 的最长真回文后缀节点。
func (t *Tree) Link(v int) int { return t.nodes[v].link }

// Cnt 返回节点 v 的出现次数（已传播）。
func (t *Tree) Cnt(v int) int { return t.nodes[v].cnt }

// Next 返回节点 v 经字符 c 的转移目标及是否存在。
func (t *Tree) Next(v int, c byte) (int, bool) {
	nxt, ok := t.nodes[v].next[c]
	return nxt, ok
}

// Sub 返回节点 v 代表的回文子串。
func (t *Tree) Sub(v int) string {
	n := t.nodes[v]
	return string(t.s[n.end-n.length+1 : n.end+1])
}
