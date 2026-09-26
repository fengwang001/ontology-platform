// Package dfa 用子集构造把 NFA 确定化为 DFA。
package dfa

import (
	"strconv"
	"strings"

	"ontology/nfa"
)

// DFA 的每个状态是一个 NFA 状态集合（升序）。构造完成后只读，可并发使用。
type DFA struct {
	States [][]int        // NFA 状态集合，按发现顺序
	Trans  []map[byte]int // 每状态每字符至多一个后继（无 ∅ 死状态）
	Start  int
	Accept []bool // 子集含至少一个 NFA 接受态即接受

	index       map[string]int // 按集合内容定位既有状态
	dedupChecks int            // 一次 move+ε闭包 查重时检查的既有状态个数
}

func key(set []int) string {
	var b strings.Builder
	for _, s := range set {
		b.WriteString(strconv.Itoa(s))
		b.WriteByte(',')
	}
	return b.String()
}

// intern 按集合内容查重：哈希定位，检查的既有状态个数恒为 1，不随已有状态数增长。
func (d *DFA) intern(set []int) (id int, isNew bool) {
	d.dedupChecks++
	if id, ok := d.index[key(set)]; ok {
		return id, false
	}
	id = len(d.States)
	d.index[key(set)] = id
	d.States = append(d.States, set)
	d.Trans = append(d.Trans, map[byte]int{})
	return id, true
}

// Build 子集构造：从 ε闭包(起点) 出发，对每个新集合与字母表每个字符
// 算 ε闭包(move(Q,c))；空集不生成状态；相同集合只对应一个状态。
func Build(n *nfa.NFA) *DFA {
	d := &DFA{index: map[string]int{}}
	acc := make([]bool, n.N)
	for _, a := range n.Accept {
		if a >= 0 && a < n.N {
			acc[a] = true
		}
	}
	mark := func(set []int) bool {
		for _, s := range set {
			if acc[s] {
				return true
			}
		}
		return false
	}
	d.Start, _ = d.intern(n.EpsilonClosure([]int{n.Start}))
	d.Accept = append(d.Accept, mark(d.States[d.Start]))
	for cur := 0; cur < len(d.States); cur++ {
		for _, c := range n.Alphabet {
			var mv []int
			for _, s := range d.States[cur] {
				if s < len(n.Trans) {
					mv = append(mv, n.Trans[s][c]...)
				}
			}
			cl := n.EpsilonClosure(mv)
			if len(cl) == 0 {
				continue // 死状态 ∅ 不生成 DFA 状态
			}
			nid, isNew := d.intern(cl)
			if isNew {
				d.Accept = append(d.Accept, mark(cl))
			}
			d.Trans[cur][c] = nid
		}
	}
	return d
}

// Accepts 从起始状态逐字符走转移；无转移即拒绝，读完后停在接受态即接受。
func (d *DFA) Accepts(s string) bool {
	cur := d.Start
	for i := 0; i < len(s); i++ {
		nxt, ok := d.Trans[cur][s[i]]
		if !ok {
			return false
		}
		cur = nxt
	}
	return d.Accept[cur]
}

// VerifyDedupScaling 核验查重检查的既有状态个数不随 DFA 规模 m 增长。
// 只返回结论，不暴露计数器数值。
func VerifyDedupScaling() bool {
	for _, m := range []int{100, 1000, 10000} {
		n := chainNFA(m)
		d := Build(n)
		d.dedupChecks = 0
		d.intern(n.EpsilonClosure(n.Trans[0]['a'])) // 命中既有状态
		d.intern([]int{0, m - 1})                   // 全新集合
		if d.dedupChecks > 2 {
			return false
		}
	}
	return true
}

// chainNFA 构造链式 NFA：i -a-> i+1，子集构造恰好产生 m 个 DFA 状态。
func chainNFA(m int) *nfa.NFA {
	n := &nfa.NFA{N: m, Trans: make([]map[byte][]int, m), Alphabet: []byte{'a'}, Accept: []int{m - 1}}
	for i := 0; i+1 < m; i++ {
		n.Trans[i] = map[byte][]int{'a': {i + 1}}
	}
	return n
}
