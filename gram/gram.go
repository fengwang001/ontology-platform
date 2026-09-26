// Package gram 表示上下文无关文法，并提供 FIRST 集计算。不依赖其他包。
package gram

import (
	"errors"
	"sort"
)

// Symbol 是终结符或非终结符。
type Symbol string

const (
	End = Symbol("$") // 输入结束符
	Eps = Symbol("ε") // 空串，只出现在 First 的结果里
)

var (
	// ErrUndeclaredSymbol 产生式引用了未声明的符号。
	ErrUndeclaredSymbol = errors.New("gram: production references undeclared symbol")
	// ErrStartNotNonTerm 开始符号不是已声明的非终结符。
	ErrStartNotNonTerm = errors.New("gram: start symbol is not a nonterminal")
)

// Production 是一条产生式 LHS -> RHS，RHS 为空切片表示 ε 产生式。
type Production struct {
	LHS Symbol
	RHS []Symbol
}

// Grammar 是只读文法，构造完成后不再修改，可并发使用。
type Grammar struct {
	Start    Symbol
	Prods    []Production
	terms    map[Symbol]bool
	nonTerms map[Symbol]bool
	first    map[Symbol]map[Symbol]bool // 含 Eps 表示可推出空串
	byLHS    map[Symbol][]int           // 非终结符 -> 产生式下标
}

// New 构造文法并校验：开始符号必须是非终结符，产生式引用的符号必须已声明。
func New(start Symbol, terminals, nonTerminals []Symbol, prods []Production) (*Grammar, error) {
	g := &Grammar{
		Start:    start,
		Prods:    prods,
		terms:    map[Symbol]bool{},
		nonTerms: map[Symbol]bool{},
		byLHS:    map[Symbol][]int{},
	}
	for _, t := range terminals {
		g.terms[t] = true
	}
	for _, n := range nonTerminals {
		g.nonTerms[n] = true
	}
	if !g.nonTerms[start] {
		return nil, ErrStartNotNonTerm
	}
	for i, p := range prods {
		if !g.nonTerms[p.LHS] {
			return nil, ErrUndeclaredSymbol
		}
		for _, s := range p.RHS {
			if !g.terms[s] && !g.nonTerms[s] {
				return nil, ErrUndeclaredSymbol
			}
		}
		g.byLHS[p.LHS] = append(g.byLHS[p.LHS], i)
	}
	g.computeFirst()
	return g, nil
}

// computeFirst 用不动点迭代计算所有符号的 FIRST 集（含 Eps 标记）。
func (g *Grammar) computeFirst() {
	g.first = map[Symbol]map[Symbol]bool{End: {End: true}}
	for t := range g.terms {
		g.first[t] = map[Symbol]bool{t: true}
	}
	for n := range g.nonTerms {
		g.first[n] = map[Symbol]bool{}
	}
	for changed := true; changed; {
		changed = false
		for _, p := range g.Prods {
			f := g.first[p.LHS]
			nullablePrefix := true
			for _, s := range p.RHS {
				for sym := range g.first[s] {
					if sym != Eps && !f[sym] {
						f[sym] = true
						changed = true
					}
				}
				if !g.first[s][Eps] {
					nullablePrefix = false
					break
				}
			}
			if nullablePrefix && !f[Eps] {
				f[Eps] = true
				changed = true
			}
		}
	}
}

// IsTerminal 报告 s 是否为终结符（不含 End）；IsNonTerm 报告是否为非终结符。
func (g *Grammar) IsTerminal(s Symbol) bool { return g.terms[s] }
func (g *Grammar) IsNonTerm(s Symbol) bool  { return g.nonTerms[s] }

// ProdsOf 返回左部为 lhs 的产生式下标（只读，勿修改）。
func (g *Grammar) ProdsOf(lhs Symbol) []int { return g.byLHS[lhs] }

// First 返回 sym 的 FIRST 集副本；若 sym 可推出空串则含 Eps。
func (g *Grammar) First(sym Symbol) map[Symbol]bool {
	out := map[Symbol]bool{}
	for s := range g.first[sym] {
		out[s] = true
	}
	return out
}

// FirstSeq 返回 FIRST(seq 后跟 follow) 的终结符集合（升序，不含 Eps）：
// 依次并入各符号 FIRST 的终结符，遇不可推空即停；全部可推空时并入 follow。
func (g *Grammar) FirstSeq(seq []Symbol, follow Symbol) []Symbol {
	set := map[Symbol]bool{}
	for _, s := range seq {
		for sym := range g.first[s] {
			if sym != Eps {
				set[sym] = true
			}
		}
		if !g.first[s][Eps] {
			return sorted(set)
		}
	}
	set[follow] = true
	return sorted(set)
}

func sorted(set map[Symbol]bool) []Symbol {
	out := make([]Symbol, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
