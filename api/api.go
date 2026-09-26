// Package api 是对外入口：带校验的闭包/goto，以及内置文法的自检。依赖 lr。
package api

import (
	"errors"
	"fmt"

	"ontology/gram"
	"ontology/lr"
)

// ClosureOf 校验输入项集后返回其 LR(1) 闭包；输入非法时整体失败，不返回项集。
func ClosureOf(g *gram.Grammar, I []lr.Item) ([]lr.Item, error) {
	if err := lr.Validate(g, I); err != nil {
		return nil, err
	}
	return lr.Closure(g, I), nil
}

// GotoOf 校验输入项集后返回 goto(I, X)；输入非法时整体失败，不返回项集。
func GotoOf(g *gram.Grammar, I []lr.Item, X gram.Symbol) ([]lr.Item, error) {
	if err := lr.Validate(g, I); err != nil {
		return nil, err
	}
	return lr.Goto(g, I, X), nil
}

// naiveClosure 朴素参照：反复全量重扫，暴力枚举所有产生式与所有终结符前瞻，直到不动点。
func naiveClosure(g *gram.Grammar, seed []lr.Item) []lr.Item {
	cur := lr.Dedup(seed)
	for {
		next := append([]lr.Item{}, cur...)
		for _, it := range cur {
			if it.Dot >= len(it.RHS) || !g.IsNonTerm(it.RHS[it.Dot]) {
				continue
			}
			B := it.RHS[it.Dot]
			first := map[gram.Symbol]bool{}
			for _, b := range g.FirstSeq(it.RHS[it.Dot+1:], it.Look) {
				first[b] = true
			}
			for _, pi := range g.ProdsOf(B) {
				p := g.Prods[pi]
				for b := range first {
					next = append(next, lr.Item{LHS: p.LHS, RHS: p.RHS, Dot: 0, Look: b})
				}
			}
		}
		next = lr.Dedup(next)
		if lr.Equal(next, cur) {
			return next
		}
		cur = next
	}
}

func must(g *gram.Grammar, err error) *gram.Grammar {
	if err != nil {
		panic(err)
	}
	return g
}

// specGrammar 是第三节的文法：S'->S, S->CC, C->cC|d。
func specGrammar() *gram.Grammar {
	return must(gram.New("S'", []gram.Symbol{"c", "d"}, []gram.Symbol{"S'", "S", "C"}, []gram.Production{
		{LHS: "S'", RHS: []gram.Symbol{"S"}},
		{LHS: "S", RHS: []gram.Symbol{"C", "C"}},
		{LHS: "C", RHS: []gram.Symbol{"c", "C"}},
		{LHS: "C", RHS: []gram.Symbol{"d"}},
	}))
}

// epsGrammar 是变体文法：S->Ab, A->ε|a。
func epsGrammar() *gram.Grammar {
	return must(gram.New("S", []gram.Symbol{"a", "b"}, []gram.Symbol{"S", "A"}, []gram.Production{
		{LHS: "S", RHS: []gram.Symbol{"A", "b"}},
		{LHS: "A", RHS: nil},
		{LHS: "A", RHS: []gram.Symbol{"a"}},
	}))
}

// SelfCheck 对内置文法核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	g := specGrammar()
	I0 := []lr.Item{{LHS: "S'", RHS: []gram.Symbol{"S"}, Dot: 0, Look: gram.End}}
	cl := lr.Closure(g, I0)
	// 不变量 1：与朴素参照逐项相同（闭包与 goto 都核）。
	if !lr.Equal(cl, naiveClosure(g, I0)) {
		return errors.New("selfcheck: closure != naive reference")
	}
	gt := lr.Goto(g, cl, "C")
	var kernel []lr.Item
	for _, it := range cl {
		if it.Dot < len(it.RHS) && it.RHS[it.Dot] == "C" {
			kernel = append(kernel, lr.Item{LHS: it.LHS, RHS: it.RHS, Dot: it.Dot + 1, Look: it.Look})
		}
	}
	if !lr.Equal(gt, naiveClosure(g, kernel)) {
		return errors.New("selfcheck: goto != naive reference")
	}
	// 不变量 2：闭包幂等，且每个闭包项的前瞻符都属于某个驱动项的 FIRST(βa)。
	if !lr.Equal(lr.Closure(g, cl), cl) {
		return errors.New("selfcheck: closure not idempotent")
	}
	for _, it := range cl {
		if it.Dot != 0 || lr.Equal(append(append([]lr.Item{}, I0...), it), I0) {
			continue // 非闭包项或种子项本身不需要驱动项
		}
		if !hasDriver(g, cl, it) {
			return fmt.Errorf("selfcheck: %v has no driver", it)
		}
	}
	// 不变量 3：goto 核中每项 · 前恰以 X 结尾，且整体等于核的闭包。
	for _, it := range gt {
		if it.Dot > 0 && it.RHS[it.Dot-1] != "C" {
			return errors.New("selfcheck: goto kernel item not advanced over C")
		}
	}
	// 不变量 4：被拒输入整体失败且不留痕，后续调用不受影响。
	bad := []lr.Item{{LHS: "S'", RHS: []gram.Symbol{"S"}, Dot: 2, Look: gram.End}}
	if got, err := ClosureOf(g, bad); !errors.Is(err, lr.ErrDotOutOfRange) || got != nil {
		return errors.New("selfcheck: rejected input not clean")
	}
	if got, err := ClosureOf(g, I0); err != nil || !lr.Equal(got, cl) {
		return errors.New("selfcheck: state changed after rejection")
	}
	// 变体文法：前瞻符穿透空串，[A->·, b] 与 [A->·a, b] 的前瞻符都是 b。
	ge := epsGrammar()
	ce := lr.Closure(ge, []lr.Item{{LHS: "S", RHS: []gram.Symbol{"A", "b"}, Look: gram.End}})
	for _, it := range ce {
		if it.LHS == "A" && it.Look != "b" {
			return errors.New("selfcheck: epsilon variant lookahead != b")
		}
	}
	return nil
}

func hasDriver(g *gram.Grammar, cl []lr.Item, it lr.Item) bool {
	for _, d := range cl {
		if d.Dot < len(d.RHS) && d.RHS[d.Dot] == it.LHS {
			for _, b := range g.FirstSeq(d.RHS[d.Dot+1:], d.Look) {
				if b == it.Look {
					return true
				}
			}
		}
	}
	return false
}
