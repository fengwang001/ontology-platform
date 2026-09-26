// Package pal 实现回文树（Eertree）结构：两个根、next 转移、link 后缀链、
// len、cnt，以及逐字符 add 构建。不依赖其他包。
package pal

type node struct {
	next map[byte]int // 转移：c -> 两侧包上 c 得到的回文节点
	link int          // 最长真回文后缀节点
	len  int          // 该节点代表的回文长度
	cnt  int          // 构建时被「扩展命中」的次数
	end  int          // 首次出现时的右端点（开区间），用于取回原文
}

// Tree 是构建完成后的回文树，构建后只读。
type Tree struct {
	s     string
	nodes []node
	last  int
	total int // 非导出计数器：构建完成后的节点总数（线性性证明用，不导出）
}

// Build 对 s 逐字符构建回文树。下标 0 为奇根（len -1），1 为偶根（len 0）。
func Build(s string) *Tree {
	t := &Tree{s: s, last: 1}
	t.nodes = []node{
		{link: 0, len: -1}, // 奇根
		{link: 0, len: 0},  // 偶根
	}
	for i := 0; i < len(s); i++ {
		t.add(i)
	}
	t.total = len(t.nodes)
	return t
}

// add 把 s[pos] 并入树：找到可扩展的最长回文后缀，必要时新建节点。
func (t *Tree) add(pos int) {
	c := t.s[pos]
	cur := t.last
	for !t.canExtend(cur, pos) {
		cur = t.nodes[cur].link
	}
	if nxt, ok := t.nodes[cur].next[c]; ok { // 已存在，仅计一次命中
		t.last = nxt
		t.nodes[nxt].cnt++
		return
	}
	nl := t.nodes[cur].len + 2
	t.nodes = append(t.nodes, node{next: nil, len: nl, cnt: 1, end: pos + 1})
	ni := len(t.nodes) - 1
	if t.nodes[cur].next == nil {
		t.nodes[cur].next = make(map[byte]int)
	}
	t.nodes[cur].next[c] = ni
	if nl == 1 { // 单字符回文 link 指向偶根
		t.nodes[ni].link = 1
	} else { // 从 cur 的 link 继续找同字符扩展，即最长真回文后缀
		for cur = t.nodes[cur].link; !t.canExtend(cur, pos); cur = t.nodes[cur].link {
		}
		t.nodes[ni].link = t.nodes[cur].next[c]
	}
	t.last = ni
}

// canExtend 报告节点 cur 两侧包上 s[pos] 后是否仍是 s 的子串。
func (t *Tree) canExtend(cur, pos int) bool {
	l := t.nodes[cur].len
	return pos-1-l >= 0 && t.s[pos-1-l] == t.s[pos]
}

// Distinct 返回不同回文子串个数（节点总数 − 2）。
func (t *Tree) Distinct() int { return len(t.nodes) - 2 }

// Len 返回节点 i 代表的回文长度。
func (t *Tree) Len(i int) int { return t.nodes[i].len }

// Link 返回节点 i 的最长真回文后缀节点下标。
func (t *Tree) Link(i int) int { return t.nodes[i].link }

// Cnt 返回节点 i 构建时的扩展命中次数（未做 link 传播）。
func (t *Tree) Cnt(i int) int { return t.nodes[i].cnt }

// Find 返回回文 p 对应的节点下标；p 必须是回文，不存在时 ok=false。
// 从与 p 同奇偶的根出发，由中心向两端逐字符走 next 转移。
func (t *Tree) Find(p string) (idx int, ok bool) {
	cur := 1 // 偶根
	if len(p)%2 == 1 {
		cur = 0 // 奇根
	}
	for i := (len(p) - 1) / 2; i >= 0; i-- {
		nxt, ok := t.nodes[cur].next[p[i]]
		if !ok {
			return 0, false
		}
		cur = nxt
	}
	return cur, true
}

// Text 返回节点 i 代表的回文原文。
func (t *Tree) Text(i int) string {
	n := &t.nodes[i]
	return t.s[n.end-n.len : n.end]
}

// CheckLinks 复核 link 结构：每个非根节点指向更短的回文（长度严格
// 递减故无环），且 len 为 1 的节点指向偶根。
func (t *Tree) CheckLinks() bool {
	for i := 2; i < len(t.nodes); i++ {
		lk := t.nodes[i].link
		if lk < 0 || lk >= len(t.nodes) || t.nodes[lk].len >= t.nodes[i].len {
			return false
		}
		if t.nodes[i].len == 1 && lk != 1 {
			return false
		}
	}
	return true
}
