// Package lr implements LR(1) items, item sets, closure and goto; it depends on
// gram only, and that direction must never reverse.
package lr

import (
	"errors"
	"slices"
	"sort"
	"strings"

	"ontology/gram"
)

// Item is [Head -> Body[:Dot] · Body[Dot:], Lookahead]; same core with another
// lookahead is a different item; compare via key.
type Item struct {
	Head      gram.Symbol
	Body      []gram.Symbol
	Dot       int
	Lookahead gram.Symbol
}

// ItemSet is an unordered, deduplicated collection of items.
type ItemSet struct{ Items []Item }

var (
	ErrDotOutOfRange    = errors.New("lr: item dot position out of range")
	ErrInvalidLookahead = errors.New("lr: item lookahead is not a terminal or $")
)

// closureStats.rechecked counts items generated while already present; it is
// non-exported and reachable only via the unexported closureRun.
type closureStats struct{ rechecked int }
type itemKey struct {
	head, body gram.Symbol
	dot        int
	la         gram.Symbol
}

func key(it Item) itemKey {
	p := make([]string, len(it.Body))
	for i, s := range it.Body {
		p[i] = string(s)
	}
	return itemKey{it.Head, gram.Symbol(strings.Join(p, "\x00")), it.Dot, it.Lookahead}
}

// validateItem: any failure is total and the caller returns a nil item set.
func validateItem(g *gram.Grammar, it Item) error {
	if it.Dot < 0 || it.Dot > len(it.Body) {
		return ErrDotOutOfRange
	}
	if it.Lookahead != gram.EOI && !g.IsTerminal(it.Lookahead) {
		return ErrInvalidLookahead
	}
	if !g.IsNonTerminal(it.Head) {
		return gram.ErrUndeclaredSymbol
	}
	for _, s := range it.Body {
		if !g.IsTerminal(s) && !g.IsNonTerminal(s) {
			return gram.ErrUndeclaredSymbol
		}
	}
	return nil
}

// worker is one closure run on a work queue: each distinct item is processed
// exactly once; a regenerated item bumps rechecked instead of re-entering.
type worker struct {
	g       *gram.Grammar
	items   []Item
	present map[itemKey]struct{}
	st      *closureStats
}

func (w *worker) add(it Item) {
	if _, ok := w.present[key(it)]; ok {
		w.st.rechecked++
		return
	}
	w.present[key(it)] = struct{}{}
	w.items = append(w.items, it)
}
func closureCore(g *gram.Grammar, seeds []Item, st *closureStats) []Item {
	w := &worker{g: g, present: map[itemKey]struct{}{}, st: st}
	for _, it := range seeds {
		w.add(it)
	}
	for i := 0; i < len(w.items); i++ { // each item processed at most once
		it := w.items[i]
		if it.Dot >= len(it.Body) || !g.IsNonTerminal(it.Body[it.Dot]) {
			continue
		}
		B := it.Body[it.Dot]
		las := g.FirstSeq(it.Body[it.Dot+1:], it.Lookahead) // FIRST(β a)
		for _, p := range g.ProductionsOf(B) {
			for b := range las {
				w.add(Item{B, p.Body, 0, b})
			}
		}
	}
	return canonical(w.items)
}
func canonical(items []Item) []Item { // dedupe + sort: order-independent
	seen := map[itemKey]struct{}{}
	out := make([]Item, 0, len(items))
	for _, it := range items {
		k := key(it)
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := key(out[i]), key(out[j])
		return a.head < b.head || a.head == b.head && (a.body < b.body ||
			a.body == b.body && (a.dot < b.dot || a.dot == b.dot && a.la < b.la))
	})
	return out
}
func closureRun(g *gram.Grammar, I []Item) (out []Item, st closureStats, err error) {
	for _, it := range I {
		if e := validateItem(g, it); e != nil {
			return nil, closureStats{}, e
		}
	}
	st = closureStats{}
	return closureCore(g, I, &st), st, nil
}
func Closure(g *gram.Grammar, I []Item) ([]Item, error) {
	out, _, err := closureRun(g, I)
	return out, err
}

func Goto(g *gram.Grammar, I []Item, X gram.Symbol) ([]Item, error) {
	var kernel []Item
	for _, it := range I {
		if err := validateItem(g, it); err != nil {
			return nil, err
		}
		if it.Dot < len(it.Body) && it.Body[it.Dot] == X {
			kernel = append(kernel, Item{it.Head, it.Body, it.Dot + 1, it.Lookahead})
		}
	}
	return closureCore(g, kernel, &closureStats{}), nil
}
func Equal(a, b []Item) bool {
	a, b = canonical(a), canonical(b)
	return len(a) == len(b) && slices.EqualFunc(a, b, func(x, y Item) bool { return key(x) == key(y) })
}
