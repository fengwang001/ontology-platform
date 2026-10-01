package itc

// This file contains a deliberately naive reference implementation written
// straight from the formulas in the specification. It uses a representation
// independent from the production code (interface trees) so that the random
// differential test cannot share a bug.

import "fmt"

type nid interface{ nidTag() }

type nidZero struct{}
type nidOne struct{}
type nidPair struct {
	l, r nid
}

func (nidZero) nidTag() {}
func (nidOne) nidTag()  {}
func (nidPair) nidTag() {}

type nev interface{ nevTag() }

type nevInt struct{ n int }
type nevNode struct {
	n    int
	l, r nev
}

func (nevInt) nevTag()  {}
func (nevNode) nevTag() {}

func nz() nid { return nidZero{} }
func no() nid { return nidOne{} }
func np(l, r nid) nid {
	if _, ok := l.(nidZero); ok {
		if _, ok := r.(nidZero); ok {
			return nidZero{}
		}
	}
	if _, ok := l.(nidOne); ok {
		if _, ok := r.(nidOne); ok {
			return nidOne{}
		}
	}
	return nidPair{l, r}
}

func ni(n int) nev { return nevInt{n} }

func nn(n int, l, r nev) nev {
	if li, lok := l.(nevInt); lok {
		if ri, rok := r.(nevInt); rok && li.n == ri.n {
			return nevInt{n + li.n}
		}
	}
	m := nmn(nminv(l), nminv(r))
	if m != 0 {
		return nevNode{n + m, nsub(l, m), nsub(r, m)}
	}
	return nevNode{n, l, r}
}

func nminv(e nev) int {
	switch t := e.(type) {
	case nevInt:
		return t.n
	case nevNode:
		l := nminv(t.l)
		r := nminv(t.r)
		if l < r {
			return t.n + l
		}
		return t.n + r
	}
	panic("bad event")
}

func nmaxv(e nev) int {
	switch t := e.(type) {
	case nevInt:
		return t.n
	case nevNode:
		l := nmaxv(t.l)
		r := nmaxv(t.r)
		if l > r {
			return t.n + l
		}
		return t.n + r
	}
	panic("bad event")
}

func nlift(m int, e nev) nev {
	switch t := e.(type) {
	case nevInt:
		return nevInt{t.n + m}
	case nevNode:
		return nevNode{t.n + m, t.l, t.r}
	}
	panic("bad event")
}

func nsub(e nev, m int) nev { return nlift(-m, e) }

func nmx(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func nmn(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func nfork(i nid) (nid, nid) {
	switch t := i.(type) {
	case nidZero:
		return nidZero{}, nidZero{}
	case nidOne:
		return np(nidOne{}, nidZero{}), np(nidZero{}, nidOne{})
	case nidPair:
		if _, ok := t.l.(nidZero); ok {
			l, r := nfork(t.r)
			return np(nidZero{}, l), np(nidZero{}, r)
		}
		if _, ok := t.r.(nidZero); ok {
			l, r := nfork(t.l)
			return np(l, nidZero{}), np(r, nidZero{})
		}
		return np(t.l, nidZero{}), np(nidZero{}, t.r)
	}
	panic("bad id")
}

func nsum(a, b nid) (nid, bool) {
	if _, ok := a.(nidZero); ok {
		return b, true
	}
	if _, ok := b.(nidZero); ok {
		return a, true
	}
	ap, aok := a.(nidPair)
	bp, bok := b.(nidPair)
	if aok && bok {
		l, ok1 := nsum(ap.l, bp.l)
		if !ok1 {
			return nil, false
		}
		r, ok2 := nsum(ap.r, bp.r)
		if !ok2 {
			return nil, false
		}
		return np(l, r), true
	}
	return nil, false
}

func nfill(i nid, e nev) nev {
	if _, ok := i.(nidZero); ok {
		return e
	}
	if _, ok := e.(nevInt); ok {
		return e
	}
	if _, ok := i.(nidOne); ok {
		return ni(nmaxv(e))
	}
	ip := i.(nidPair)
	en := e.(nevNode)
	if _, ok := ip.l.(nidOne); ok {
		er := nfill(ip.r, en.r)
		return nn(en.n, ni(nmx(nmaxv(en.l), nminv(er))), er)
	}
	if _, ok := ip.r.(nidOne); ok {
		el := nfill(ip.l, en.l)
		return nn(en.n, el, ni(nmx(nmaxv(en.r), nminv(el))))
	}
	return nn(en.n, nfill(ip.l, en.l), nfill(ip.r, en.r))
}

func ngrow(i nid, e nev) (nev, int) {
	if _, ok := i.(nidOne); ok {
		return ni(e.(nevInt).n + 1), 0
	}
	if ei, ok := e.(nevInt); ok {
		g, c := ngrowNode(i, nevNode{ei.n, ni(0), ni(0)})
		return g, c + 1000000
	}
	return ngrowNode(i, e)
}

func ngrowNode(i nid, e nev) (nev, int) {
	ip := i.(nidPair)
	en := e.(nevNode)
	if _, ok := ip.l.(nidZero); ok {
		er, c := ngrow(ip.r, en.r)
		return nn(en.n, en.l, er), c + 1
	}
	if _, ok := ip.r.(nidZero); ok {
		el, c := ngrow(ip.l, en.l)
		return nn(en.n, el, en.r), c + 1
	}
	el, cl := ngrow(ip.l, en.l)
	er, cr := ngrow(ip.r, en.r)
	if cl < cr {
		return nn(en.n, el, en.r), cl + 1
	}
	return nn(en.n, en.l, er), cr + 1
}

func njoin(a, b nev) nev {
	ai, aok := a.(nevInt)
	bi, bok := b.(nevInt)
	if aok && bok {
		return ni(nmx(ai.n, bi.n))
	}
	if aok {
		return njoin(nevNode{ai.n, ni(0), ni(0)}, b)
	}
	if bok {
		return njoin(a, nevNode{bi.n, ni(0), ni(0)})
	}
	an, bn := a.(nevNode), b.(nevNode)
	n1, l1, r1 := an.n, an.l, an.r
	n2, l2, r2 := bn.n, bn.l, bn.r
	if n1 > n2 {
		n1, n2 = n2, n1
		l1, l2 = l2, l1
		r1, r2 = r2, r1
	}
	d := n2 - n1
	return nn(n1, njoin(l1, nlift(d, l2)), njoin(r1, nlift(d, r2)))
}

func nleq(a, b nev) bool {
	ai, aok := a.(nevInt)
	bi, bok := b.(nevInt)
	if aok {
		bn := bi.n
		if !bok {
			bn = b.(nevNode).n
		}
		return ai.n <= bn
	}
	an := a.(nevNode)
	if bok {
		return an.n <= bi.n && nleq(nlift(an.n, an.l), b) && nleq(nlift(an.n, an.r), b)
	}
	bn := b.(nevNode)
	return an.n <= bn.n &&
		nleq(nlift(an.n, an.l), nlift(bn.n, bn.l)) &&
		nleq(nlift(an.n, an.r), nlift(bn.n, bn.r))
}

func nids(i nid) string {
	switch t := i.(type) {
	case nidZero:
		return "0"
	case nidOne:
		return "1"
	case nidPair:
		return "(" + nids(t.l) + "," + nids(t.r) + ")"
	}
	panic("bad id")
}

func nevs(e nev) string {
	switch t := e.(type) {
	case nevInt:
		return fmt.Sprintf("%d", t.n)
	case nevNode:
		return fmt.Sprintf("(%d,%s,%s)", t.n, nevs(t.l), nevs(t.r))
	}
	panic("bad event")
}

// nstamp is a naive-registry replica.
type nstamp struct {
	i nid
	e nev
}

func (s nstamp) String() string { return "(" + nids(s.i) + ";" + nevs(s.e) + ")" }

type nreg struct {
	m map[string]nstamp
}

type nresult struct {
	out1, out2 string
	err        string
}

func newNreg() *nreg { return &nreg{m: map[string]nstamp{}} }

func (r *nreg) seed(name string) nresult {
	if name == "" {
		return nresult{err: "empty"}
	}
	if _, ok := r.m[name]; ok {
		return nresult{err: "exists"}
	}
	r.m[name] = nstamp{nidOne{}, ni(0)}
	return nresult{out1: r.m[name].String()}
}

func (r *nreg) fork(name, child string) nresult {
	if name == "" || child == "" {
		return nresult{err: "empty"}
	}
	s, ok := r.m[name]
	if !ok {
		return nresult{err: "unknown1"}
	}
	if _, ok := r.m[child]; ok {
		return nresult{err: "exists"}
	}
	l, rr := nfork(s.i)
	s.i = l
	r.m[name] = s
	r.m[child] = nstamp{rr, s.e}
	return nresult{out1: s.String(), out2: r.m[child].String()}
}

func (r *nreg) event(name string) nresult {
	if name == "" {
		return nresult{err: "empty"}
	}
	s, ok := r.m[name]
	if !ok {
		return nresult{err: "unknown1"}
	}
	f := nfill(s.i, s.e)
	if nevs(f) != nevs(s.e) {
		s.e = f
	} else {
		s.e, _ = ngrow(s.i, s.e)
	}
	r.m[name] = s
	return nresult{out1: s.String()}
}

func (r *nreg) peek(name string) nresult {
	if name == "" {
		return nresult{err: "empty"}
	}
	s, ok := r.m[name]
	if !ok {
		return nresult{err: "unknown1"}
	}
	return nresult{out1: "(" + nids(nidZero{}) + ";" + nevs(s.e) + ")"}
}

func (r *nreg) join(a, b string) nresult {
	if a == "" || b == "" {
		return nresult{err: "empty"}
	}
	if a == b {
		return nresult{err: "same"}
	}
	sa, ok := r.m[a]
	if !ok {
		return nresult{err: "unknown1"}
	}
	sb, ok := r.m[b]
	if !ok {
		return nresult{err: "unknown2"}
	}
	sum, ok := nsum(sa.i, sb.i)
	if !ok {
		return nresult{err: "overlap"}
	}
	sa.i = sum
	sa.e = njoin(sa.e, sb.e)
	r.m[a] = sa
	delete(r.m, b)
	return nresult{out1: sa.String()}
}

func (r *nreg) compare(a, b string) nresult {
	if a == "" || b == "" {
		return nresult{err: "empty"}
	}
	sa, ok := r.m[a]
	if !ok {
		return nresult{err: "unknown1"}
	}
	sb, ok := r.m[b]
	if !ok {
		return nresult{err: "unknown2"}
	}
	ab := nleq(sa.e, sb.e)
	ba := nleq(sb.e, sa.e)
	rel := "Concurrent"
	switch {
	case ab && ba:
		rel = "Equal"
	case ab:
		rel = "Before"
	case ba:
		rel = "After"
	}
	return nresult{out1: rel}
}

func (r *nreg) str(name string) nresult {
	if name == "" {
		return nresult{err: "empty"}
	}
	s, ok := r.m[name]
	if !ok {
		return nresult{err: "unknown1"}
	}
	return nresult{out1: s.String()}
}

// nidSumAll sums every surviving identity; used for invariant checks.
func (r *nreg) sumAll() (nid, bool) {
	var ids []nid
	for _, s := range r.m {
		ids = append(ids, s.i)
	}
	if len(ids) == 0 {
		return nidZero{}, true
	}
	acc := ids[0]
	for _, x := range ids[1:] {
		sum, ok := nsum(acc, x)
		if !ok {
			return nil, false
		}
		acc = sum
	}
	return acc, true
}
