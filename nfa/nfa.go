// Package nfa compiles a parsed regexp to a Thompson NFA and matches
// strings by epsilon-closure simulation.
package nfa

import "ontology/reast"

// CharTr is one character transition From -Char-> To.
type CharTr struct {
	From, To int
	Char     byte
}

// NFA is a Thompson NFA over states 0..N-1; exported fields are immutable
// after Compile, so concurrent Match calls (read-only) are safe.
type NFA struct {
	N             int // number of states, numbered from 0
	Start, Accept int
	Eps           [][]int  // Eps[s]: epsilon-transition targets
	Char          []CharTr // all character transitions
	charFrom      []map[byte][]int
	touched       int // unexported: existing states read/changed by last append
}

func (n *NFA) newState() int {
	s := n.N
	n.N++
	n.Eps = append(n.Eps, nil)
	n.charFrom = append(n.charFrom, nil)
	return s
}

func (n *NFA) addEps(from, to int) { n.Eps[from] = append(n.Eps[from], to) }

func (n *NFA) addChar(from, to int, c byte) {
	n.Char = append(n.Char, CharTr{From: from, To: to, Char: c})
	if n.charFrom[from] == nil {
		n.charFrom[from] = map[byte][]int{}
	}
	n.charFrom[from][c] = append(n.charFrom[from][c], to)
}

type frag struct{ start, accept int }
type builder struct{ n *NFA }

// Compile builds the NFA; post-order allocation fixes the numbering.
func Compile(re *reast.RE) *NFA {
	n := &NFA{}
	f := (&builder{n}).build(re.Root)
	n.Start, n.Accept = f.start, f.accept
	return n
}

func (b *builder) build(nd *reast.Node) frag {
	n := b.n
	switch nd.Kind {
	case reast.KChar:
		s, f := n.newState(), n.newState()
		n.addChar(s, f, nd.Char)
		return frag{s, f}
	case reast.KConcat:
		f := b.build(nd.Subs[0])
		for _, sub := range nd.Subs[1:] {
			g := b.build(sub)
			n.addEps(f.accept, g.start)
			f.accept = g.accept
		}
		return f
	case reast.KAlt:
		gs := make([]frag, len(nd.Subs))
		for i, sub := range nd.Subs {
			gs[i] = b.build(sub)
		}
		s, f := n.newState(), n.newState()
		for _, g := range gs {
			n.addEps(s, g.start)
			n.addEps(g.accept, f)
		}
		return frag{s, f}
	case reast.KStar:
		g := b.build(nd.Subs[0])
		s, f := n.newState(), n.newState()
		n.addEps(s, g.start)
		n.addEps(s, f)
		n.addEps(g.accept, g.start)
		n.addEps(g.accept, f)
		return frag{s, f}
	case reast.KPlus: // A+ == A·A*, wired on a single copy of A
		g := b.build(nd.Subs[0])
		s, f := n.newState(), n.newState()
		n.addEps(s, g.start)
		n.addEps(g.accept, g.start)
		n.addEps(g.accept, f)
		return frag{s, f}
	case reast.KQuest: // A? == A|epsilon; the direct s->f edge is the eps branch
		g := b.build(nd.Subs[0])
		s, f := n.newState(), n.newState()
		n.addEps(s, g.start)
		n.addEps(s, f)
		n.addEps(g.accept, f)
		return frag{s, f}
	default:
		panic("nfa: unknown AST node kind")
	}
}

// AppendChar concatenates one literal: only the old accept is touched.
func (n *NFA) AppendChar(c byte) *NFA {
	old := n.Accept
	n.touched = 1
	s, f := n.newState(), n.newState()
	n.addChar(s, f, c)
	n.addEps(old, s)
	n.Accept = f
	return n
}

// closure adds every epsilon-reachable state to set.
func (n *NFA) closure(set map[int]struct{}) map[int]struct{} {
	stk := []int{}
	for st := range set {
		stk = append(stk, st)
	}
	for len(stk) > 0 {
		st := stk[len(stk)-1]
		stk = stk[:len(stk)-1]
		for _, to := range n.Eps[st] {
			if _, ok := set[to]; !ok {
				set[to] = struct{}{}
				stk = append(stk, to)
			}
		}
	}
	return set
}

// Match advances with epsilon closures and tests Accept membership.
func (n *NFA) Match(s string) bool {
	cur := n.closure(map[int]struct{}{n.Start: {}})
	for i := 0; i < len(s); i++ {
		nxt := map[int]struct{}{}
		for st := range cur {
			for _, to := range n.charFrom[st][s[i]] {
				nxt[to] = struct{}{}
			}
		}
		cur = n.closure(nxt)
	}
	_, ok := cur[n.Accept]
	return ok
}
