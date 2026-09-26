// Package lr 实现 LR(1) 项、项集闭包与 goto。依赖 gram。
package lr

import (
	"errors"
	"sort"
	"strings"

	"ontology/gram"
)

var (
	// ErrDotOutOfRange 项的 · 位置越出右部长度。
	ErrDotOutOfRange = errors.New("lr: dot position out of range")
	// ErrBadLookahead 前瞻符不是终结符也不是 $。
	ErrBadLookahead = errors.New("lr: lookahead is not a terminal or $")
)

// Item 是 LR(1) 项 [LHS -> α·β, Look]，Dot 是 · 在 RHS 中的位置。
type Item struct {
	LHS  gram.Symbol
	RHS  []gram.Symbol
	Dot  int
	Look gram.Symbol
}

// key 是项的判等键：核心相同但前瞻符不同是不同的项。
func (it Item) key() string {
	var b strings.Builder
	b.WriteString(string(it.LHS) + "\x00")
	for _, s := range it.RHS {
		b.WriteString(string(s) + "\x01")
	}
	b.WriteString(string(rune(it.Dot)) + "\x02" + string(it.Look))
	return b.String()
}

// computer 是单次闭包计算的状态，rechecks 记录重复生成已在集合中的项的次数。
type computer struct {
	rechecks int
}

// Closure 用工作队列计算项集闭包，每个项至多入队处理一次，结果去重并排序。
func Closure(g *gram.Grammar, seed []Item) []Item {
	items, _ := closureStats(g, seed)
	return items
}

// closureStats 是非导出的带统计实现，仅供包内测试核验计数器。
func closureStats(g *gram.Grammar, seed []Item) ([]Item, int) {
	c := &computer{}
	seen := map[string]bool{}
	var queue []Item
	push := func(it Item) {
		if seen[it.key()] {
			c.rechecks++
			return
		}
		seen[it.key()] = true
		queue = append(queue, it)
	}
	for _, it := range seed {
		push(it)
	}
	var out []Item
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		out = append(out, it)
		if it.Dot >= len(it.RHS) || !g.IsNonTerm(it.RHS[it.Dot]) {
			continue
		}
		B := it.RHS[it.Dot]
		for _, b := range g.FirstSeq(it.RHS[it.Dot+1:], it.Look) {
			for _, pi := range g.ProdsOf(B) {
				p := g.Prods[pi]
				push(Item{LHS: p.LHS, RHS: p.RHS, Dot: 0, Look: b})
			}
		}
	}
	SortItems(out)
	return out, c.rechecks
}

// Goto 先把 I 中 · 后为 X 的项推进一格，再对核求闭包。
func Goto(g *gram.Grammar, items []Item, x gram.Symbol) []Item {
	var kernel []Item
	for _, it := range items {
		if it.Dot < len(it.RHS) && it.RHS[it.Dot] == x {
			kernel = append(kernel, Item{it.LHS, it.RHS, it.Dot + 1, it.Look})
		}
	}
	return Closure(g, kernel)
}

// SortItems 按判等键升序排序，保证输出确定。
func SortItems(items []Item) {
	sort.Slice(items, func(i, j int) bool { return items[i].key() < items[j].key() })
}

// Dedup 去重并排序。
func Dedup(items []Item) []Item {
	seen := map[string]bool{}
	var out []Item
	for _, it := range items {
		if !seen[it.key()] {
			seen[it.key()] = true
			out = append(out, it)
		}
	}
	SortItems(out)
	return out
}

// Equal 报告两个项集作为集合是否逐项相同。
func Equal(a, b []Item) bool {
	a, b = Dedup(a), Dedup(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].key() != b[i].key() {
			return false
		}
	}
	return true
}

// Validate 校验项集：· 位置不得越界，前瞻符必须是终结符或 $。
func Validate(g *gram.Grammar, items []Item) error {
	for _, it := range items {
		if it.Dot < 0 || it.Dot > len(it.RHS) {
			return ErrDotOutOfRange
		}
		if it.Look != gram.End && !g.IsTerminal(it.Look) {
			return ErrBadLookahead
		}
	}
	return nil
}
