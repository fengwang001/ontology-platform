// Package api is the top layer: callable entry points plus SelfCheck. It
// depends on lr and gram; nothing it imports may depend on it.
package api

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"ontology/gram"
	"ontology/lr"
)

func ClosureOf(g *gram.Grammar, I []lr.Item) ([]lr.Item, error) { return lr.Closure(g, I) }
func GotoOf(g *gram.Grammar, I []lr.Item, X gram.Symbol) ([]lr.Item, error) {
	return lr.Goto(g, I, X)
}
func naiveClosure(g *gram.Grammar, seeds []lr.Item) []lr.Item {
	// Brute reference per spec: rescan the WHOLE set each pass; for every item
	// enumerate EVERY production and the admissible lookahead set FIRST(β a),
	// add [B->·γ,b], and repeat whole-set rescans until a fixed point.
	k := func(it lr.Item) string { return fmt.Sprintf("%v", it) }
	cur := map[string]lr.Item{}
	for _, it := range seeds {
		cur[k(it)] = it
	}
	for changed := true; changed; {
		changed = false
		for _, it := range cur { // full rescan until fixed point
			if it.Dot >= len(it.Body) || !g.IsNonTerminal(it.Body[it.Dot]) {
				continue
			}
			want := g.FirstSeq(it.Body[it.Dot+1:], it.Lookahead)
			for _, p := range g.Productions() { // every production…
				if p.Head != it.Body[it.Dot] {
					continue
				}
				for b := range want { // …and every b in FIRST(β a)
					nw := lr.Item{Head: p.Head, Body: p.Body, Lookahead: b}
					if _, ok := cur[k(nw)]; !ok {
						cur[k(nw)], changed = nw, true
					}
				}
			}
		}
	}
	return slices.Collect(maps.Values(cur))
}
func classicGrammar() *gram.Grammar {
	g, _ := gram.New("S'", []gram.Symbol{"c", "d"}, []gram.Symbol{"S'", "S", "C"}, []gram.Production{
		{Head: "S'", Body: []gram.Symbol{"S"}},
		{Head: "S", Body: []gram.Symbol{"C", "C"}},
		{Head: "C", Body: []gram.Symbol{"c", "C"}},
		{Head: "C", Body: []gram.Symbol{"d"}},
	})
	return g
}
func i0() []lr.Item { return []lr.Item{{Head: "S'", Body: []gram.Symbol{"S"}, Lookahead: "$"}} }
func SelfCheck() error {
	g := classicGrammar()
	cl, err := lr.Closure(g, i0())
	if err != nil || len(cl) != 6 {
		return fmt.Errorf("invariant2: expected 6 items, got %d (%v)", len(cl), err)
	}
	if !lr.Equal(cl, naiveClosure(g, i0())) { // (1) match naive reference
		return errors.New("invariant1: closure != naive reference")
	}
	if again, _ := lr.Closure(g, cl); !lr.Equal(cl, again) { // (2) idempotence
		return errors.New("invariant2: closure not idempotent")
	}
	if err := justified(g, i0(), cl); err != nil { // (2) LA provenance
		return err
	}
	var kernel []lr.Item // (3) goto == closure(advanced kernel)
	for _, it := range cl {
		if it.Dot < len(it.Body) && it.Body[it.Dot] == "C" {
			kernel = append(kernel, lr.Item{Head: it.Head, Body: it.Body, Dot: it.Dot + 1, Lookahead: it.Lookahead})
		}
	}
	if gc, e := lr.Goto(g, cl, "C"); e != nil {
		return e
	} else if kc, _ := lr.Closure(g, kernel); !lr.Equal(gc, kc) {
		return errors.New("invariant3: goto != closure(kernel)")
	}
	if err := fourErrors(); err != nil { // (4) distinct sentinels, nil results
		return err
	}
	if after, e := lr.Closure(g, i0()); e != nil || len(after) != 6 {
		return errors.New("invariant4: rejection disturbed later calls")
	}
	return nil
}
func justified(g *gram.Grammar, seeds, cl []lr.Item) error {
	k := func(it lr.Item) string { return fmt.Sprintf("%v", it) }
	seed := map[string]bool{}
	for _, it := range seeds {
		seed[k(it)] = true
	}
	for _, cand := range cl { // each generated dot-0 item needs LA in some FIRST(βa)
		if seed[k(cand)] || cand.Dot != 0 {
			continue
		}
		ok := false
		for _, d := range cl {
			if d.Dot < len(d.Body) && d.Body[d.Dot] == cand.Head {
				if _, h := g.FirstSeq(d.Body[d.Dot+1:], d.Lookahead)[cand.Lookahead]; h {
					ok = true
					break
				}
			}
		}
		if !ok {
			return fmt.Errorf("invariant2: %v lookahead unjustified", cand)
		}
	}
	return nil
}
func fourErrors() error {
	T, N := []gram.Symbol{"x"}, []gram.Symbol{"S"}
	if _, e := gram.New("S", T, N, []gram.Production{{Head: "S", Body: []gram.Symbol{"z"}}}); !errors.Is(e, gram.ErrUndeclaredSymbol) {
		return errors.New("invariant4: undeclared symbol not rejected")
	}
	if _, e := gram.New("x", T, N, nil); !errors.Is(e, gram.ErrInvalidStart) {
		return errors.New("invariant4: bad start not rejected")
	}
	g := classicGrammar()
	for _, c := range []struct {
		it  lr.Item
		err error
	}{
		{lr.Item{Head: "C", Body: []gram.Symbol{"d"}, Dot: 5, Lookahead: "$"}, lr.ErrDotOutOfRange},
		{lr.Item{Head: "C", Body: []gram.Symbol{"d"}, Lookahead: "C"}, lr.ErrInvalidLookahead},
	} {
		if r, e := lr.Closure(g, []lr.Item{c.it}); !errors.Is(e, c.err) || r != nil {
			return fmt.Errorf("invariant4: %v not rejected / non-nil", c.err)
		}
	}
	seen := map[string]bool{}
	for _, e := range []error{gram.ErrUndeclaredSymbol, gram.ErrInvalidStart, lr.ErrDotOutOfRange, lr.ErrInvalidLookahead} {
		if seen[e.Error()] {
			return errors.New("invariant4: sentinels not distinct")
		}
		seen[e.Error()] = true
	}
	return nil
}
