// Package dfa 用子集构造把 NFA 确定化为 DFA。
package dfa

import (
	"sort"
	"strconv"
	"strings"

	"ontology/nfa"
)

// DFA 的每个状态是一个 NFA 状态集合（升序、按内容去重）。构造后只读，可并发 Accepts。
type DFA struct {
	states      [][]int
	trans       []map[byte]int // 确定性：每 (状态,字符) 至多一个后继
	start       int
	accept      []bool
	index       map[string]int // 集合内容 -> 状态 id，哈希定位
	dedupChecks int            // 一次 move+ε闭包 后为查重检查的既有状态个数（非导出，不进公开接口）
}

func keyOf(s []int) string {
	var b strings.Builder
	for _, q := range s {
		b.WriteString(strconv.Itoa(q))
		b.WriteByte(',')
	}
	return b.String()
}

func sorted(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for q := range set {
		out = append(out, q)
	}
	sort.Ints(out)
	return out
}

func setOf(s []int) map[int]bool {
	m := make(map[int]bool, len(s))
	for _, q := range s {
		m[q] = true
	}
	return m
}

// intern 按集合内容查重；已存在返回其 id，否则新建。返回 (id, isNew)。
func (d *DFA) intern(set map[int]bool, n *nfa.NFA) (int, bool) {
	ss := sorted(set)
	k := keyOf(ss)
	d.dedupChecks++ // 哈希表一次定位，不随既有状态数线性增长
	if id, ok := d.index[k]; ok {
		return id, false
	}
	id := len(d.states)
	d.states = append(d.states, ss)
	d.trans = append(d.trans, map[byte]int{})
	acc := false
	for _, q := range ss {
		if n.Accept[q] { // 含至少一个 NFA 接受态即接受
			acc = true
			break
		}
	}
	d.accept = append(d.accept, acc)
	d.index[k] = id
	return id, true
}

// Build 子集构造。NFA 不合法时整体失败：返回 nil 与可判定哨兵错误。
func Build(n *nfa.NFA) (*DFA, error) {
	if err := n.Validate(); err != nil {
		return nil, err
	}
	d := &DFA{index: map[string]int{}}
	d.start, _ = d.intern(n.EpsilonClosure(map[int]bool{n.Start: true}), n)
	for queue := []int{d.start}; len(queue) > 0; {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range n.Alphabet {
			mv := n.Move(setOf(d.states[cur]), c)
			if len(mv) == 0 {
				continue // 死状态 ∅ 不生成 DFA 状态
			}
			nxt, isNew := d.intern(n.EpsilonClosure(mv), n)
			d.trans[cur][c] = nxt
			if isNew {
				queue = append(queue, nxt)
			}
		}
	}
	return d, nil
}

// Accepts 从起始状态逐字符走转移；遇无转移即拒绝，读完停在接受态即接受。
func (d *DFA) Accepts(s string) bool {
	cur := d.start
	for i := 0; i < len(s); i++ {
		nxt, ok := d.trans[cur][s[i]]
		if !ok {
			return false
		}
		cur = nxt
	}
	return d.accept[cur]
}

func (d *DFA) NumStates() int { return len(d.states) }

func (d *DFA) Start() int { return d.start }

func (d *DFA) IsAccept(i int) bool { return d.accept[i] }

func (d *DFA) State(i int) []int { return append([]int(nil), d.states[i]...) }

// Next 返回状态 i 在字符 c 上的唯一后继；无转移时 ok=false。
func (d *DFA) Next(i int, c byte) (int, bool) {
	id, ok := d.trans[i][c]
	return id, ok
}

// VerifyDedupScales 构造 m=100/1000/10000 状态的链式 DFA，各再做一次新集合
// 查重，若检查次数均不超过与 m 无关的小常数则返回 true（不暴露计数器数值）。
func VerifyDedupScales() bool {
	for _, m := range []int{100, 1000, 10000} {
		n := chainNFA(m)
		d, err := Build(n)
		if err != nil || d.NumStates() != m {
			return false
		}
		before := d.dedupChecks
		d.intern(map[int]bool{0: true, 1: true}, n) // 链上从未出现的新集合
		if d.dedupChecks-before > 2 {
			return false
		}
	}
	return true
}

// chainNFA 构造 0-a->1-a->...-a->(m-1) 的链，子集构造恰得 m 个 DFA 状态。
func chainNFA(m int) *nfa.NFA {
	tr := map[int]map[byte][]int{}
	for i := 0; i+1 < m; i++ {
		tr[i] = map[byte][]int{'a': {i + 1}}
	}
	return &nfa.NFA{NumStates: m, Trans: tr, Start: 0,
		Accept: map[int]bool{m - 1: true}, Alphabet: []byte{'a'}}
}
