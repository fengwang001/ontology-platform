// Package gram is the bottom layer: context-free grammar representation and
// FIRST sets. It must not import any other project package.
package gram

import "errors"

// Symbol is a grammar symbol: a terminal, a nonterminal, or the end marker EOI.
type Symbol string

// EOI ("$") is the end-of-input lookahead; it behaves like a terminal.
const EOI Symbol = "$"

// Production is Head -> Body. A nil/empty Body denotes the epsilon production.
type Production struct {
	Head Symbol
	Body []Symbol
}

// Sentinel errors (the two grammar-side failures required by the spec).
var (
	// ErrUndeclaredSymbol: a production references a symbol that is neither a
	// declared terminal nor a declared nonterminal (or is declared as both).
	ErrUndeclaredSymbol = errors.New("gram: production references an undeclared symbol")
	// ErrInvalidStart: the start symbol is not a declared nonterminal.
	ErrInvalidStart = errors.New("gram: start symbol is not a nonterminal")
)

// Grammar is read-only after New returns, so concurrent readers need no locks.
type Grammar struct {
	start  Symbol
	terms  map[Symbol]struct{}
	non    map[Symbol]struct{}
	prods  []Production
	byHead map[Symbol][]Production
	first  map[Symbol]map[Symbol]struct{} // terminals only; epsilon tracked separately
	nullab map[Symbol]bool
}

// New validates and builds a grammar. Validation failure is total: no partial
// grammar is returned ("failure leaves no trace").
func New(start Symbol, terminals, nonterminals []Symbol, prods []Production) (*Grammar, error) {
	g := &Grammar{
		start:  start,
		terms:  map[Symbol]struct{}{},
		non:    map[Symbol]struct{}{},
		byHead: map[Symbol][]Production{},
	}
	for _, t := range terminals {
		if t == EOI { // EOI is reserved
			return nil, ErrUndeclaredSymbol
		}
		g.terms[t] = struct{}{}
	}
	for _, n := range nonterminals {
		if _, isTerm := g.terms[n]; isTerm {
			return nil, ErrUndeclaredSymbol // declared as both terminal and nonterminal
		}
		g.non[n] = struct{}{}
	}
	if _, ok := g.non[start]; !ok {
		return nil, ErrInvalidStart
	}
	for _, p := range prods {
		if _, ok := g.non[p.Head]; !ok {
			return nil, ErrUndeclaredSymbol
		}
		for _, s := range p.Body {
			if _, t := g.terms[s]; t {
				continue
			}
			if _, n := g.non[s]; n {
				continue
			}
			return nil, ErrUndeclaredSymbol
		}
	}
	g.prods = append(g.prods, prods...)
	for _, p := range prods {
		g.byHead[p.Head] = append(g.byHead[p.Head], p)
	}
	g.computeFirst()
	return g, nil
}

// computeFirst derives nullable(X) and FIRST(X) for every symbol by naive
// fixed point over the productions. Built once at construction; afterwards the
// maps are only read, which is race-free.
func (g *Grammar) computeFirst() {
	g.first = map[Symbol]map[Symbol]struct{}{}
	g.nullab = map[Symbol]bool{}
	for t := range g.terms {
		g.first[t] = map[Symbol]struct{}{t: {}}
	}
	for n := range g.non {
		g.first[n] = map[Symbol]struct{}{}
	}
	for changed := true; changed; {
		changed = false
		for _, p := range g.prods {
			allNull := true
			for _, s := range p.Body {
				for t := range g.first[s] {
					if _, ok := g.first[p.Head][t]; !ok {
						g.first[p.Head][t] = struct{}{}
						changed = true
					}
				}
				if !g.nullab[s] {
					allNull = false
					break
				}
			}
			if allNull && !g.nullab[p.Head] {
				g.nullab[p.Head] = true
				changed = true
			}
		}
	}
}
func (g *Grammar) Start() Symbol                       { return g.start }
func (g *Grammar) IsTerminal(s Symbol) bool            { _, ok := g.terms[s]; return ok }
func (g *Grammar) IsNonTerminal(s Symbol) bool         { _, ok := g.non[s]; return ok }
func (g *Grammar) ProductionsOf(h Symbol) []Production { return g.byHead[h] }
func (g *Grammar) Productions() []Production           { return g.prods }
func (g *Grammar) First(sym Symbol) map[Symbol]struct{} {
	out := map[Symbol]struct{}{}
	for t := range g.first[sym] {
		out[t] = struct{}{}
	}
	return out
}

// FirstSeq: FIRST(seq follow) — add each FIRST(s) until a non-nullable one; if
// all are nullable, add follow (empty follow means add nothing).
func (g *Grammar) FirstSeq(seq []Symbol, follow Symbol) map[Symbol]struct{} {
	out := map[Symbol]struct{}{}
	for _, s := range seq {
		for t := range g.first[s] {
			out[t] = struct{}{}
		}
		if !g.nullab[s] {
			return out
		}
	}
	if follow != "" {
		out[follow] = struct{}{}
	}
	return out
}
