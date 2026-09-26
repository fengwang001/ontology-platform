// Package sam 实现后缀自动机（SAM）：状态、后缀链与逐字节 extend 构建。
// 构建完成后的 Automaton 只读，可被多 goroutine 并发查询。
package sam

type state struct {
	next map[byte]int // 按字节的转移
	link int          // 后缀链，根的 link 为 -1
	len  int          // 该状态代表的最长子串长度 maxLen
	term bool         // 构建中是否当过 cur（对应某个前缀）
}

// Automaton 是构建完成的后缀自动机。
type Automaton struct {
	st    []state
	last  int
	total int // 非导出计数器：构建完成后的状态总数
}

// Build 由字节串 s 构建 SAM。
func Build(s string) *Automaton {
	a := &Automaton{st: make([]state, 1, 2*len(s)+1)}
	a.st[0] = state{next: make(map[byte]int), link: -1}
	a.total = 1
	for i := 0; i < len(s); i++ {
		a.extend(s[i])
	}
	return a
}

func (a *Automaton) addState(l, link int, next map[byte]int) int {
	a.st = append(a.st, state{next: next, link: link, len: l})
	a.total++
	return len(a.st) - 1
}

// extend 读入一个字节 c，扩展自动机。
func (a *Automaton) extend(c byte) {
	cur := a.addState(a.st[a.last].len+1, 0, make(map[byte]int))
	a.st[cur].term = true
	p := a.last
	for p != -1 {
		if _, ok := a.st[p].next[c]; ok {
			break
		}
		a.st[p].next[c] = cur
		p = a.st[p].link
	}
	if p == -1 {
		a.st[cur].link = 0
	} else if q := a.st[p].next[c]; a.st[p].len+1 == a.st[q].len {
		a.st[cur].link = q
	} else {
		clone := a.addState(a.st[p].len+1, a.st[q].link, cloneNext(a.st[q].next))
		for p != -1 && a.st[p].next[c] == q {
			a.st[p].next[c] = clone
			p = a.st[p].link
		}
		a.st[q].link = clone
		a.st[cur].link = clone
	}
	a.last = cur
}

func cloneNext(src map[byte]int) map[byte]int {
	dst := make(map[byte]int, len(src))
	for b, to := range src {
		dst[b] = to
	}
	return dst
}

// Root 返回根状态编号。
func (a *Automaton) Root() int { return 0 }

// Len 返回状态 v 的 maxLen。
func (a *Automaton) Len(v int) int { return a.st[v].len }

// Link 返回状态 v 的后缀链目标，根返回 -1。
func (a *Automaton) Link(v int) int { return a.st[v].link }

// Terminal 报告 v 是否对应某个前缀（构建中当过 cur）。
func (a *Automaton) Terminal(v int) bool { return a.st[v].term }

// Next 返回状态 v 沿字节 b 的转移目标；ok 为 false 表示无此转移。
func (a *Automaton) Next(v int, b byte) (to int, ok bool) {
	to, ok = a.st[v].next[b]
	return to, ok
}

// Each 对每个状态编号调用 fn（含根）。
func (a *Automaton) Each(fn func(v int)) {
	for v := range a.st {
		fn(v)
	}
}

// CheckLinkTree 核验 link 树结构：每个非根状态 len[v]>len[link[v]]
// （长度严格递减蕴含无环），且转移目标 len[to] ≥ len[v]+1。
func (a *Automaton) CheckLinkTree() bool {
	for v := range a.st {
		if v != 0 {
			if p := a.st[v].link; p < 0 || a.st[v].len <= a.st[p].len {
				return false
			}
		}
		for _, to := range a.st[v].next {
			if a.st[to].len < a.st[v].len+1 {
				return false
			}
		}
	}
	return true
}

// LinearBoundOK 报告为 s 构建的 SAM 状态总数是否落在 [n+1, 2n]（n=len(s)）。
// 只给出判定结果，不暴露计数器数值。
func LinearBoundOK(s string) bool {
	a := Build(s)
	n := len(s)
	return a.total >= n+1 && a.total <= 2*n
}
